package crosspoint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// DeviceStatus mirrors GET /api/status from CrossPoint Reader firmware.
type DeviceStatus struct {
	Version            string `json:"version"`
	FirmwareFamily     string `json:"firmwareFamily"`
	PluginAPI          int    `json:"pluginApi"`
	PluginCapabilities string `json:"pluginCapabilities,omitempty"`
	IP                 string `json:"ip"`
	Mode               string `json:"mode"` // "STA" or "AP"
	RSSI               int    `json:"rssi"`
	FreeHeap           int    `json:"freeHeap"`
	Uptime             int    `json:"uptime"`
	Device             string `json:"device"` // "X3" or "X4"
}

// SDFileEntry mirrors GET /api/files?path=... from CrossPoint Reader firmware.
type SDFileEntry struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	IsDirectory bool   `json:"isDirectory"`
	IsEpub      bool   `json:"isEpub"`
}

// DiscoveredDevice represents a CrossPoint Reader found via UDP port 8134 broadcast.
type DiscoveredDevice struct {
	Host     string `json:"host"`
	Hostname string `json:"hostname"`
	HTTPPort int    `json:"http_port"`
	WSPort   int    `json:"ws_port"`
	RawReply string `json:"raw_reply"`
}

// PushResult describes the outcome of pushing an EPUB to a CrossPoint Reader device.
type PushResult struct {
	Protocol   string   `json:"protocol"`
	RemotePath string   `json:"remote_path"`
	BytesSent  int64    `json:"bytes_sent"`
	DurationMs int64    `json:"duration_ms"`
	Steps      []string `json:"steps"`
	Message    string   `json:"message"`
}

// Client communicates with an Xteink X3/X4 running CrossPoint Reader firmware.
type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "ws://")
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.IndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}
	return host
}

// GetStatus queries GET http://<host>:<httpPort>/api/status on the CrossPoint device.
func (c *Client) GetStatus(host string, httpPort int) (*DeviceStatus, string, error) {
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	endpoint := fmt.Sprintf("http://%s:%d/api/status", host, httpPort)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("connect to %s: %w (ensure Xteink X3 is in File Transfer or Calibre Wireless mode)", endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("device returned HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var st DeviceStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, string(raw), fmt.Errorf("decode /api/status JSON: %w", err)
	}
	return &st, string(raw), nil
}

// ListFiles queries GET http://<host>:<httpPort>/api/files?path=<dirPath> on the CrossPoint device.
func (c *Client) ListFiles(host string, httpPort int, dirPath string) ([]SDFileEntry, error) {
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	if dirPath == "" {
		dirPath = "/"
	}
	endpoint := fmt.Sprintf("http://%s:%d/api/files?path=%s", host, httpPort, url.QueryEscape(dirPath))

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("list files on %s: %w", host, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var entries []SDFileEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("decode /api/files JSON: %w", err)
	}
	return entries, nil
}

// EnsureFolder calls POST /mkdir on the CrossPoint device to ensure the target folder exists.
func (c *Client) EnsureFolder(host string, httpPort int, targetDir string) error {
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	targetDir = strings.TrimSpace(targetDir)
	if targetDir == "" || targetDir == "/" {
		return nil
	}
	parent := path.Dir(targetDir)
	name := path.Base(targetDir)
	if parent == "." {
		parent = "/"
	}

	form := url.Values{}
	form.Set("name", name)
	form.Set("path", parent)

	endpoint := fmt.Sprintf("http://%s:%d/mkdir", host, httpPort)
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// DeleteRemoteFile deletes a file or empty folder on the CrossPoint SD card via POST /delete.
func (c *Client) DeleteRemoteFile(host string, httpPort int, remotePath string) error {
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	form := url.Values{}
	form.Set("path", remotePath)

	endpoint := fmt.Sprintf("http://%s:%d/delete", host, httpPort)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed (HTTP %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// PushBook uploads an EPUB file to the Xteink X3 / CrossPoint Reader using the selected protocol:
// - "websocket": CrossPoint WebSocket Fast Upload on port 81 (START:<filename>:<size>:<path> -> READY -> binary chunks -> DONE)
// - "http": CrossPoint HTTP Multipart Upload on port 80 (POST /upload?path=<dir>&overwrite=1)
// - "webdav": CrossPoint WebDAV PUT on port 80 (PUT <dir>/<filename>)
// - "auto": Tries WebSocket Fast Upload first, falling back to HTTP Multipart Upload if needed.
func (c *Client) PushBook(host string, httpPort, wsPort int, targetDir, remoteFileName, localEPUBPath, protocol string, overwrite bool) (*PushResult, error) {
	start := time.Now()
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	if wsPort <= 0 {
		wsPort = 81
	}
	if targetDir == "" {
		targetDir = "/Books"
	}
	if !strings.HasPrefix(targetDir, "/") {
		targetDir = "/" + targetDir
	}
	if remoteFileName == "" {
		remoteFileName = filepath.Base(localEPUBPath)
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "auto"
	}

	// Ensure destination directory exists on SD card
	_ = c.EnsureFolder(host, httpPort, targetDir)

	switch protocol {
	case "websocket", "ws":
		res, err := c.pushViaWebSocket(host, wsPort, targetDir, remoteFileName, localEPUBPath)
		if err != nil && overwrite && strings.Contains(err.Error(), "File already exists") {
			// Delete existing file and retry WebSocket, or fallback to HTTP overwrite
			_ = c.DeleteRemoteFile(host, httpPort, path.Join(targetDir, remoteFileName))
			res, err = c.pushViaWebSocket(host, wsPort, targetDir, remoteFileName, localEPUBPath)
		}
		if res != nil {
			res.DurationMs = time.Since(start).Milliseconds()
		}
		return res, err

	case "http":
		res, err := c.pushViaHTTPMultipart(host, httpPort, targetDir, remoteFileName, localEPUBPath, overwrite)
		if res != nil {
			res.DurationMs = time.Since(start).Milliseconds()
		}
		return res, err

	case "webdav":
		res, err := c.pushViaWebDAV(host, httpPort, targetDir, remoteFileName, localEPUBPath)
		if res != nil {
			res.DurationMs = time.Since(start).Milliseconds()
		}
		return res, err

	default: // "auto"
		res, wsErr := c.pushViaWebSocket(host, wsPort, targetDir, remoteFileName, localEPUBPath)
		if wsErr == nil {
			res.DurationMs = time.Since(start).Milliseconds()
			return res, nil
		}
		// Fallback to HTTP multipart with overwrite=1
		httpRes, httpErr := c.pushViaHTTPMultipart(host, httpPort, targetDir, remoteFileName, localEPUBPath, overwrite)
		if httpErr == nil {
			httpRes.Steps = append([]string{fmt.Sprintf("WebSocket port %d note (%v) → switched to HTTP Multipart upload", wsPort, wsErr)}, httpRes.Steps...)
			httpRes.DurationMs = time.Since(start).Milliseconds()
			return httpRes, nil
		}
		return nil, fmt.Errorf("WebSocket (%v) and HTTP fallback (%v) both failed", wsErr, httpErr)
	}
}

// pushViaWebSocket implements CrossPoint Reader's port 81 binary upload protocol:
// 1. Send text: START:<filename>:<size>:<path>
// 2. Receive text: READY
// 3. Send binary chunks (16 KB)
// 4. Receive PROGRESS:<received>:<total> and final DONE
func (c *Client) pushViaWebSocket(host string, wsPort int, targetDir, fileName, localPath string) (*PushResult, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("read epub file: %w", err)
	}
	fileSize := len(data)

	wsURL := fmt.Sprintf("ws://%s:%d/", host, wsPort)
	origin := fmt.Sprintf("http://%s/", host)

	cfg, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		return nil, err
	}
	cfg.Dialer = &net.Dialer{Timeout: 6 * time.Second}

	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dial CrossPoint WebSocket %s: %w", wsURL, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	var steps []string
	startCmd := fmt.Sprintf("START:%s:%d:%s", fileName, fileSize, targetDir)
	steps = append(steps, "TX -> "+startCmd)

	if err := websocket.Message.Send(conn, startCmd); err != nil {
		return nil, fmt.Errorf("send START command: %w", err)
	}

	var reply string
	if err := websocket.Message.Receive(conn, &reply); err != nil {
		return nil, fmt.Errorf("wait for READY response: %w", err)
	}
	reply = strings.TrimSpace(reply)
	steps = append(steps, "RX <- "+reply)

	if strings.HasPrefix(reply, "ERROR:") {
		return nil, fmt.Errorf("device rejected upload: %s", reply)
	}
	if reply != "READY" {
		return nil, fmt.Errorf("unexpected WebSocket handshake reply: %s", reply)
	}

	// Stream binary chunks (16 KB each, matching CrossPoint's buffer preferences)
	const chunkSize = 16 * 1024
	for offset := 0; offset < fileSize; offset += chunkSize {
		end := offset + chunkSize
		if end > fileSize {
			end = fileSize
		}
		if err := websocket.Message.Send(conn, data[offset:end]); err != nil {
			return nil, fmt.Errorf("send binary chunk at %d: %w", offset, err)
		}
	}

	// Read PROGRESS:<received>:<total> messages until DONE or ERROR:<msg>
	for {
		var msg string
		if err := websocket.Message.Receive(conn, &msg); err != nil {
			return nil, fmt.Errorf("read upload completion status: %w", err)
		}
		msg = strings.TrimSpace(msg)
		steps = append(steps, "RX <- "+msg)
		if msg == "DONE" {
			break
		}
		if strings.HasPrefix(msg, "ERROR:") {
			return nil, fmt.Errorf("device upload error: %s", msg)
		}
	}

	remotePath := path.Join(targetDir, fileName)
	return &PushResult{
		Protocol:   "websocket",
		RemotePath: remotePath,
		BytesSent:  int64(fileSize),
		Steps:      steps,
		Message:    fmt.Sprintf("Uploaded %s (%d bytes) to %s via CrossPoint WebSocket Fast Upload (port %d)", fileName, fileSize, remotePath, wsPort),
	}, nil
}

// pushViaHTTPMultipart implements POST http://<host>:<httpPort>/upload?path=<targetDir>&overwrite=1
func (c *Client) pushViaHTTPMultipart(host string, httpPort int, targetDir, fileName, localPath string, overwrite bool) (*PushResult, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		return nil, err
	}
	written, err := io.Copy(part, f)
	if err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	overwriteVal := "0"
	if overwrite {
		overwriteVal = "1"
	}
	endpoint := fmt.Sprintf("http://%s:%d/upload?path=%s&overwrite=%s",
		host, httpPort, url.QueryEscape(targetDir), overwriteVal)

	req, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /upload to %s: %w", host, err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	respText := strings.TrimSpace(string(respBytes))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d from CrossPoint /upload: %s", resp.StatusCode, respText)
	}

	remotePath := path.Join(targetDir, fileName)
	return &PushResult{
		Protocol:   "http",
		RemotePath: remotePath,
		BytesSent:  written,
		Steps: []string{
			fmt.Sprintf("POST %s (%d bytes)", endpoint, written),
			fmt.Sprintf("RX <- HTTP %d: %s", resp.StatusCode, respText),
		},
		Message: fmt.Sprintf("Uploaded %s (%d bytes) to %s via CrossPoint HTTP Multipart", fileName, written, remotePath),
	}, nil
}

// pushViaWebDAV implements PUT http://<host>:<httpPort><targetDir>/<fileName>
func (c *Client) pushViaWebDAV(host string, httpPort int, targetDir, fileName, localPath string) (*PushResult, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, err
	}
	remotePath := path.Join(targetDir, fileName)
	endpoint := fmt.Sprintf("http://%s:%d%s/%s",
		host, httpPort, strings.TrimRight(targetDir, "/"), url.PathEscape(fileName))

	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/epub+zip")
	req.ContentLength = int64(len(data))

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("WebDAV PUT to %s: %w", host, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WebDAV PUT failed (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	return &PushResult{
		Protocol:   "webdav",
		RemotePath: remotePath,
		BytesSent:  int64(len(data)),
		Steps: []string{
			fmt.Sprintf("PUT %s (%d bytes)", endpoint, len(data)),
			fmt.Sprintf("RX <- HTTP %d", resp.StatusCode),
		},
		Message: fmt.Sprintf("Uploaded %s (%d bytes) to %s via CrossPoint WebDAV", fileName, len(data), remotePath),
	}, nil
}

// RegisterOPDSOnDevice calls POST http://<host>:<httpPort>/api/opds on the CrossPoint Reader
// to save this Folio library's OPDS feed directly onto the Xteink X3.
func (c *Client) RegisterOPDSOnDevice(host string, httpPort int, catalogName, opdsURL, username, password string) error {
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	payload := map[string]any{
		"name":     catalogName,
		"url":      opdsURL,
		"username": username,
		"password": password,
	}
	b, _ := json.Marshal(payload)

	endpoint := fmt.Sprintf("http://%s:%d/api/opds", host, httpPort)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("device returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ConfigureKOSyncOnDevice calls POST http://<host>:<httpPort>/api/settings on the CrossPoint Reader
// to set koServerUrl to this Folio server.
func (c *Client) ConfigureKOSyncOnDevice(host string, httpPort int, syncServerURL string) error {
	host = normalizeHost(host)
	if httpPort <= 0 {
		httpPort = 80
	}
	payload := map[string]any{
		"koServerUrl": syncServerURL,
	}
	b, _ := json.Marshal(payload)

	endpoint := fmt.Sprintf("http://%s:%d/api/settings", host, httpPort)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("device returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// DiscoverDevices sends a UDP "hello" broadcast to port 8134 (and optional custom port)
// and parses CrossPoint Reader responses: "crosspoint (on <hostname>);81".
func DiscoverDevices(timeout time.Duration, extraPorts ...int) []DiscoveredDevice {
	if timeout <= 0 {
		timeout = 1200 * time.Millisecond
	}
	ports := []int{8134}
	ports = append(ports, extraPorts...)

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	targets := []string{"255.255.255.255", "127.0.0.1"}
	for _, t := range targets {
		for _, p := range ports {
			addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", t, p))
			if err == nil {
				_, _ = conn.WriteTo([]byte("hello"), addr)
			}
		}
	}

	var found []DiscoveredDevice
	seen := make(map[string]bool)
	buf := make([]byte, 512)

	for {
		n, raddr, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		reply := strings.TrimSpace(string(buf[:n]))
		if !strings.HasPrefix(strings.ToLower(reply), "crosspoint") {
			continue
		}
		udpAddr, ok := raddr.(*net.UDPAddr)
		if !ok {
			continue
		}
		hostIP := udpAddr.IP.String()
		wsPort := 81
		hostname := "crosspoint.local"

		// Parse "crosspoint (on <hostname>);81"
		if semi := strings.LastIndexByte(reply, ';'); semi >= 0 {
			if p, err := strconv.Atoi(strings.TrimSpace(reply[semi+1:])); err == nil && p > 0 {
				wsPort = p
			}
		}
		if onIdx := strings.Index(reply, "(on "); onIdx >= 0 {
			endIdx := strings.Index(reply[onIdx:], ")")
			if endIdx > 4 {
				hostname = reply[onIdx+4 : onIdx+endIdx]
			}
		}

		httpPort := 80
		if wsPort == 18081 {
			httpPort = 18080
		}

		key := fmt.Sprintf("%s:%d", hostIP, httpPort)
		if seen[key] {
			continue
		}
		seen[key] = true
		found = append(found, DiscoveredDevice{
			Host:     hostIP,
			Hostname: hostname,
			HTTPPort: httpPort,
			WSPort:   wsPort,
			RawReply: reply,
		})
	}
	return found
}
