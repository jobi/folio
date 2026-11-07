package crosspoint

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// Simulator emulates an Xteink X3 running CrossPoint Reader v1.6.0 in File Transfer / Calibre Wireless mode.
type Simulator struct {
	SDCardDir   string
	HTTPPort    int
	WSPort      int
	UDPPort     int
	BootTime    time.Time
	mu          sync.Mutex
	OPDSServers []map[string]any
	Settings    map[string]any
}

func NewSimulator(sdCardDir string, httpPort, wsPort int) *Simulator {
	return &Simulator{
		SDCardDir: sdCardDir,
		HTTPPort:  httpPort,
		WSPort:    wsPort,
		UDPPort:   18134,
		BootTime:  time.Now(),
		OPDSServers: []map[string]any{
			{
				"index":       0,
				"name":        "Folio Local Catalog",
				"url":         "http://localhost:8080/opds",
				"username":    "reader",
				"hasPassword": false,
			},
		},
		Settings: map[string]any{
			"koServerUrl":     "http://localhost:8080",
			"fontSize":        1,
			"showHiddenFiles": 0,
			"tiltPageTurn":    1,
		},
	}
}

// Start launches the simulated Xteink X3 HTTP/WebDAV server, WebSocket Fast Upload server, and UDP discovery responder.
func (sim *Simulator) Start() error {
	booksDir := filepath.Join(sim.SDCardDir, "Books")
	if err := os.MkdirAll(booksDir, 0o755); err != nil {
		return err
	}

	// 1. Start HTTP + WebDAV server on HTTPPort
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/api/status", sim.handleStatus)
	httpMux.HandleFunc("/api/files", sim.handleFiles)
	httpMux.HandleFunc("/upload", sim.handleUpload)
	httpMux.HandleFunc("/download", sim.handleDownload)
	httpMux.HandleFunc("/mkdir", sim.handleMkdir)
	httpMux.HandleFunc("/delete", sim.handleDelete)
	httpMux.HandleFunc("/api/opds", sim.handleOPDS)
	httpMux.HandleFunc("/api/settings", sim.handleSettings)
	httpMux.HandleFunc("/", sim.handleWebDAVOrRoot)

	go func() {
		_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", sim.HTTPPort), corsWrap(httpMux))
	}()

	// 2. Start WebSocket Fast Upload server on WSPort (START:<filename>:<size>:<path> -> READY -> chunks -> DONE)
	wsMux := http.NewServeMux()
	wsMux.Handle("/", websocket.Handler(sim.handleWSUpload))
	go func() {
		_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", sim.WSPort), wsMux)
	}()

	// 3. Start UDP Discovery responder on UDPPort
	go sim.runUDPDiscovery()

	return nil
}

func corsWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PROPFIND, MKCOL")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Depth, Overwrite, Destination")
		if r.Method == http.MethodOptions {
			w.Header().Set("DAV", "1")
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (sim *Simulator) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := DeviceStatus{
		Version:            "1.6.0",
		FirmwareFamily:     "crosspoint",
		PluginAPI:          1,
		PluginCapabilities: "catalog.lists,catalog.notices,catalog.bundle_install",
		IP:                 "127.0.0.1",
		Mode:               "STA",
		RSSI:               -42,
		FreeHeap:           164320,
		Uptime:             int(time.Since(sim.BootTime).Seconds()) + 420,
		Device:             "X3",
	}
	_ = json.NewEncoder(w).Encode(status)
}

func (sim *Simulator) resolveSafePath(relPath string) (string, error) {
	cleaned := path.Clean("/" + strings.TrimSpace(relPath))
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("invalid path")
	}
	return filepath.Join(sim.SDCardDir, filepath.FromSlash(cleaned)), nil
}

func (sim *Simulator) handleFiles(w http.ResponseWriter, r *http.Request) {
	dirParam := r.URL.Query().Get("path")
	if dirParam == "" {
		dirParam = "/"
	}
	fullDir, err := sim.resolveSafePath(dirParam)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = os.MkdirAll(fullDir, 0o755)
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	out := make([]SDFileEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		out = append(out, SDFileEntry{
			Name:        e.Name(),
			Size:        info.Size(),
			IsDirectory: e.IsDir(),
			IsEpub:      ext == ".epub",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (sim *Simulator) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dirParam := r.URL.Query().Get("path")
	if dirParam == "" {
		dirParam = "/"
	}
	overwriteParam := strings.ToLower(r.URL.Query().Get("overwrite"))
	overwrite := overwriteParam == "1" || overwriteParam == "true"

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "Bad multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := filepath.Base(header.Filename)
	destDir, err := sim.resolveSafePath(dirParam)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = os.MkdirAll(destDir, 0o755)
	destPath := filepath.Join(destDir, fileName)

	if st, err := os.Stat(destPath); err == nil {
		if st.IsDir() {
			http.Error(w, "A folder with this name already exists: "+fileName, http.StatusBadRequest)
			return
		}
		if !overwrite {
			http.Error(w, "File already exists: "+fileName, http.StatusBadRequest)
			return
		}
	}

	partPath := destPath + ".part"
	out, err := os.Create(partPath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		_ = os.Remove(partPath)
		http.Error(w, "Write failed", http.StatusInternalServerError)
		return
	}
	out.Close()
	if err := os.Rename(partPath, destPath); err != nil {
		http.Error(w, "Rename failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "File uploaded successfully: %s", fileName)
}

func (sim *Simulator) handleDownload(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	full, err := sim.resolveSafePath(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.HasSuffix(strings.ToLower(full), ".epub") {
		w.Header().Set("Content-Type", "application/epub+zip")
	}
	http.ServeFile(w, r, full)
}

func (sim *Simulator) handleMkdir(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	parent := strings.TrimSpace(r.FormValue("path"))
	if parent == "" {
		parent = "/"
	}
	full, err := sim.resolveSafePath(path.Join(parent, name))
	if err != nil || name == "" {
		http.Error(w, "Invalid folder name", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(full, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (sim *Simulator) handleDelete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	p := strings.TrimSpace(r.FormValue("path"))
	if p == "" {
		http.Error(w, "Missing path", http.StatusBadRequest)
		return
	}
	full, err := sim.resolveSafePath(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Remove(full); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Deleted"))
}

func (sim *Simulator) handleOPDS(w http.ResponseWriter, r *http.Request) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sim.OPDSServers)
		return
	}
	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		name, _ := body["name"].(string)
		u, _ := body["url"].(string)
		user, _ := body["username"].(string)
		pass, _ := body["password"].(string)

		// Update if URL or name already exists, else append
		updated := false
		for i, srv := range sim.OPDSServers {
			if srv["url"] == u || srv["name"] == name {
				sim.OPDSServers[i] = map[string]any{
					"index":       i,
					"name":        name,
					"url":         u,
					"username":    user,
					"hasPassword": pass != "",
				}
				updated = true
				break
			}
		}
		if !updated {
			sim.OPDSServers = append(sim.OPDSServers, map[string]any{
				"index":       len(sim.OPDSServers),
				"name":        name,
				"url":         u,
				"username":    user,
				"hasPassword": pass != "",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (sim *Simulator) handleSettings(w http.ResponseWriter, r *http.Request) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	if r.Method == http.MethodGet {
		items := []map[string]any{
			{
				"key":      "koServerUrl",
				"name":     "KOReader Sync Server URL",
				"category": "Sync",
				"type":     "string",
				"value":    sim.Settings["koServerUrl"],
			},
			{
				"key":      "tiltPageTurn",
				"name":     "Tilt Page Turn (Gyroscope)",
				"category": "Reader",
				"type":     "toggle",
				"value":    sim.Settings["tiltPageTurn"],
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
		return
	}
	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		for k, v := range body {
			sim.Settings[k] = v
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Applied %d setting(s)", len(body))
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (sim *Simulator) handleWebDAVOrRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		full, err := sim.resolveSafePath(r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		tmp := full + ".davtmp"
		f, err := os.Create(tmp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(f, r.Body)
		f.Close()
		if err != nil {
			_ = os.Remove(tmp)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = os.Rename(tmp, full)
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "CrossPoint Reader v1.6.0 (Simulated) — File Transfer Active\n")
}

// handleWSUpload implements the exact CrossPoint Reader WebSocket Fast Upload server protocol on port 81/18081:
// Client -> START:<filename>:<size>:<path>
// Server -> READY
// Client -> [binary chunks]
// Server -> PROGRESS:<received>:<total>
// Server -> DONE
func (sim *Simulator) handleWSUpload(ws *websocket.Conn) {
	defer ws.Close()

	var startMsg string
	if err := websocket.Message.Receive(ws, &startMsg); err != nil {
		return
	}
	startMsg = strings.TrimSpace(startMsg)
	if !strings.HasPrefix(startMsg, "START:") {
		_ = websocket.Message.Send(ws, "ERROR:Invalid START format")
		return
	}

	// Parse START:<filename>:<size>:<path>
	rest := strings.TrimPrefix(startMsg, "START:")
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		_ = websocket.Message.Send(ws, "ERROR:Invalid START format")
		return
	}
	fileName := filepath.Base(parts[0])
	expectedSize, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || expectedSize <= 0 {
		_ = websocket.Message.Send(ws, "ERROR:Invalid START format")
		return
	}
	targetDir := parts[2]
	if targetDir == "" {
		targetDir = "/"
	}

	destDir, err := sim.resolveSafePath(targetDir)
	if err != nil {
		_ = websocket.Message.Send(ws, "ERROR:Failed to create file")
		return
	}
	_ = os.MkdirAll(destDir, 0o755)
	destPath := filepath.Join(destDir, fileName)

	// Note: CrossPoint WebSocket server returns ERROR:File already exists:<name> if file already exists
	if _, err := os.Stat(destPath); err == nil {
		_ = websocket.Message.Send(ws, "ERROR:File already exists: "+fileName)
		return
	}

	f, err := os.Create(destPath)
	if err != nil {
		_ = websocket.Message.Send(ws, "ERROR:Failed to create file")
		return
	}

	if err := websocket.Message.Send(ws, "READY"); err != nil {
		f.Close()
		_ = os.Remove(destPath)
		return
	}

	var received int64
	var lastReported int64
	for received < expectedSize {
		var chunk []byte
		if err := websocket.Message.Receive(ws, &chunk); err != nil {
			f.Close()
			_ = os.Remove(destPath)
			return
		}
		n, err := f.Write(chunk)
		if err != nil {
			f.Close()
			_ = os.Remove(destPath)
			_ = websocket.Message.Send(ws, "ERROR:Write failed - disk full?")
			return
		}
		received += int64(n)
		if received > expectedSize {
			f.Close()
			_ = os.Remove(destPath)
			_ = websocket.Message.Send(ws, "ERROR:Upload overflow")
			return
		}
		if received-lastReported >= 65536 || received == expectedSize {
			lastReported = received
			_ = websocket.Message.Send(ws, fmt.Sprintf("PROGRESS:%d:%d", received, expectedSize))
		}
	}

	f.Close()
	_ = websocket.Message.Send(ws, "DONE")
}

func (sim *Simulator) runUDPDiscovery() {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("127.0.0.1:%d", sim.UDPPort))
	if err != nil {
		return
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return
	}
	defer conn.Close()

	buf := make([]byte, 256)
	for {
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg := strings.TrimSpace(string(buf[:n]))
		if msg == "hello" {
			reply := fmt.Sprintf("crosspoint (on crosspoint.local);%d", sim.WSPort)
			_, _ = conn.WriteToUDP([]byte(reply), raddr)
		}
	}
}
