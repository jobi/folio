package kosync

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/folio-books/folio/internal/config"
	"github.com/folio-books/folio/internal/db"
	"github.com/folio-books/folio/internal/tailscale"
)

// Handler implements both the KOReader Sync Server protocol (kosync) and CrossPoint Reader's /api/v1 extended sync API.
type Handler struct {
	Store *db.Store
	Cfg   *config.Config
}

func NewHandler(store *db.Store, cfg *config.Config) *Handler {
	return &Handler{Store: store, Cfg: cfg}
}

// RegisterRoutes attaches KOReader sync endpoints at both root (/) and (/kosync) prefixes, plus (/api/v1).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Root KOSync routes (byte-compatible with sync.koreader.rocks and CrossPoint)
	mux.HandleFunc("/users/create", h.handleCreateUser)
	mux.HandleFunc("/users/auth", h.handleAuthUser)
	mux.HandleFunc("/syncs/progress", h.handlePutProgress)
	mux.HandleFunc("/syncs/progress/", h.handleGetProgress)
	mux.HandleFunc("/healthz", h.handleHealthz)

	// Also mount under /kosync/* in case the user configures http://host:port/kosync as the Custom Sync Server
	mux.HandleFunc("/kosync/users/create", h.handleCreateUser)
	mux.HandleFunc("/kosync/users/auth", h.handleAuthUser)
	mux.HandleFunc("/kosync/syncs/progress", h.handlePutProgress)
	mux.HandleFunc("/kosync/syncs/progress/", h.handleGetProgress)

	// CrossPoint Reader Extended Sync API (/api/v1/*)
	mux.HandleFunc("/api/v1/progress", h.handleV1ProgressRoot)
	mux.HandleFunc("/api/v1/progress/batch", h.handleV1ProgressBatch)
	mux.HandleFunc("/api/v1/progress/changes", h.handleV1ProgressChanges)
	mux.HandleFunc("/api/v1/progress/", h.handleV1ProgressDoc)
	mux.HandleFunc("/api/v1/bookmarks/", h.handleV1Bookmarks)
	mux.HandleFunc("/api/v1/clippings/", h.handleV1Clippings)
}

func writeKOError(w http.ResponseWriter, status int, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": msg,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) authenticateRequest(r *http.Request) (*db.User, bool) {
	username := strings.TrimSpace(r.Header.Get("x-auth-user"))
	key := strings.TrimSpace(r.Header.Get("x-auth-key"))

	// 1. Standard KOReader / CrossPoint device credentials (x-auth-user + x-auth-key)
	if username != "" && key != "" {
		u, err := h.Store.AuthenticateUser(username, key)
		if err != nil || u == nil {
			return nil, false
		}
		return u, true
	}

	// 2. Tailscale-authenticated request (via Tailscale Serve headers or tailscaled WhoIs when X-Folio-Web or Tailscale-User-Login is present)
	if sess := tailscale.FromContext(r.Context()); sess != nil && sess.Authenticated && sess.User != nil {
		if r.Header.Get("X-Folio-Web") == "1" || sess.Source == "tailscale-serve" || sess.Source == "tailscaled-whois" {
			return sess.User, true
		}
	}

	// 3. Fallback for built-in Web Reader
	if username == "" && r.Header.Get("X-Folio-Web") == "1" {
		u, err := h.Store.GetDefaultUser()
		if err == nil && u != nil {
			return u, true
		}
	}

	return nil, false
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "folio-kosync"})
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
		return
	}
	if !h.Cfg.RegistrationEnabled {
		writeKOError(w, http.StatusForbidden, 2003, "Registration is disabled")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKOError(w, http.StatusForbidden, 2003, "Invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	if req.Username == "" || req.Password == "" {
		writeKOError(w, http.StatusForbidden, 2003, "Invalid username or password")
		return
	}

	u, err := h.Store.CreateUser(req.Username, req.Password)
	if err != nil {
		if err == db.ErrUserExists {
			writeKOError(w, http.StatusPaymentRequired, 2002, "Username is already registered.")
			return
		}
		writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"username": u.Username,
	})
}

func (h *Handler) handleAuthUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticateRequest(r); !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"authorized": "OK",
	})
}

type putProgressPayload struct {
	Document   string              `json:"document"`
	Progress   string              `json:"progress"`
	Percentage float64             `json:"percentage"`
	Device     string              `json:"device"`
	DeviceID   string              `json:"device_id"`
	BookID     int64               `json:"book_id,omitempty"`
	Timestamp  int64               `json:"timestamp,omitempty"`
	Position   *db.CompactPosition `json:"position,omitempty"`
	Metadata   *db.SyncMetadata    `json:"metadata,omitempty"`
}

func (h *Handler) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
		return
	}
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}

	var body putProgressPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeKOError(w, http.StatusForbidden, 2003, "Invalid request body")
		return
	}
	body.Document = strings.TrimSpace(body.Document)
	if body.Document == "" {
		writeKOError(w, http.StatusForbidden, 2004, "Missing document field")
		return
	}
	if body.Percentage < 0 {
		body.Percentage = 0
	}
	if body.Percentage > 1 {
		// In case client sent 0..100 percentage, normalize to 0..1
		if body.Percentage <= 100 {
			body.Percentage = body.Percentage / 100.0
		} else {
			body.Percentage = 1
		}
	}

	rp := &db.ReadingProgress{
		UserID:       user.ID,
		DocumentHash: body.Document,
		BookID:       body.BookID,
		Device:       body.Device,
		DeviceID:     body.DeviceID,
		Progress:     body.Progress,
		Percentage:   body.Percentage,
		Position:     body.Position,
		Metadata:     body.Metadata,
		Timestamp:    time.Now().Unix(),
	}

	saved, err := h.Store.UpsertProgress(rp)
	if err != nil {
		writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"document":  saved.DocumentHash,
		"timestamp": saved.Timestamp,
	})
}

func (h *Handler) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
		return
	}
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}

	docHash := extractLastSegment(r.URL.Path, "/syncs/progress/")
	if docHash == "" {
		writeKOError(w, http.StatusForbidden, 2004, "Missing document")
		return
	}

	rp, err := h.Store.GetLatestProgressForDocument(user.ID, docHash)
	if err != nil {
		if err == sql.ErrNoRows {
			// Stock KOReader quirk: return 200 {} when no progress exists
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
		return
	}

	resp := map[string]any{
		"document":   rp.DocumentHash,
		"progress":   rp.Progress,
		"percentage": rp.Percentage,
		"device":     rp.Device,
		"device_id":  rp.DeviceID,
		"timestamp":  rp.Timestamp,
	}
	if rp.Position != nil {
		resp["position"] = rp.Position
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- CrossPoint /api/v1 Extended Sync Endpoints ---

func (h *Handler) handleV1ProgressRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		h.handlePutProgress(w, r)
	case http.MethodGet:
		user, ok := h.authenticateRequest(r)
		if !ok {
			writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
			return
		}
		_ = user
		list, err := h.Store.ListAllProgress(100)
		if err != nil {
			writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
			return
		}
		items := make([]map[string]any, 0, len(list))
		for _, rp := range list {
			item := map[string]any{
				"document":   rp.DocumentHash,
				"title":      emptyToNil(rp.BookTitle),
				"author":     emptyToNil(rp.BookAuthors),
				"percentage": rp.Percentage,
				"progress":   rp.Progress,
				"device":     rp.Device,
				"device_id":  rp.DeviceID,
				"timestamp":  rp.Timestamp,
			}
			if rp.Position != nil {
				item["position"] = rp.Position
			}
			items = append(items, item)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"next":  nil,
		})
	default:
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
	}
}

func (h *Handler) handleV1ProgressBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
		return
	}
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}

	var req struct {
		Items []putProgressPayload `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 || len(req.Items) > 20 {
		writeKOError(w, http.StatusForbidden, 2003, "Invalid batch items (1..20 required)")
		return
	}

	var results []map[string]any
	now := time.Now().Unix()
	for _, it := range req.Items {
		if strings.TrimSpace(it.Document) == "" {
			writeKOError(w, http.StatusForbidden, 2004, "Missing document in batch")
			return
		}
		rp := &db.ReadingProgress{
			UserID:       user.ID,
			DocumentHash: strings.TrimSpace(it.Document),
			BookID:       it.BookID,
			Device:       it.Device,
			DeviceID:     it.DeviceID,
			Progress:     it.Progress,
			Percentage:   it.Percentage,
			Position:     it.Position,
			Metadata:     it.Metadata,
			Timestamp:    now,
		}
		saved, err := h.Store.UpsertProgress(rp)
		if err != nil {
			writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
			return
		}
		results = append(results, map[string]any{
			"document":  saved.DocumentHash,
			"timestamp": saved.Timestamp,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": len(results),
		"items":    results,
	})
}

func (h *Handler) handleV1ProgressChanges(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	_ = user
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	callerDevice := strings.TrimSpace(r.URL.Query().Get("device"))

	list, err := h.Store.ListAllProgress(50)
	if err != nil {
		writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
		return
	}

	var maxCursor int64 = since
	var changes []map[string]any
	seenDocs := make(map[string]bool)

	for _, rp := range list {
		if rp.Seq > maxCursor {
			maxCursor = rp.Seq
		}
		if rp.Seq <= since || seenDocs[rp.DocumentHash] {
			continue
		}
		seenDocs[rp.DocumentHash] = true
		if callerDevice != "" && rp.DeviceID == callerDevice {
			continue
		}
		changes = append(changes, map[string]any{
			"document":   rp.DocumentHash,
			"aliases":    []string{},
			"percentage": rp.Percentage,
			"progress":   rp.Progress,
			"position":   rp.Position,
			"device":     rp.Device,
			"device_id":  rp.DeviceID,
			"timestamp":  rp.Timestamp,
		})
	}
	if changes == nil {
		changes = []map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cursor":  maxCursor,
		"more":    false,
		"changes": changes,
	})
}

func (h *Handler) handleV1ProgressDoc(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}

	docHash := strings.TrimPrefix(r.URL.Path, "/api/v1/progress/")
	if docHash == "" {
		writeKOError(w, http.StatusForbidden, 2004, "Missing document")
		return
	}

	if r.Method == http.MethodGet {
		rows, err := h.Store.ListDeviceProgressForDocument(user.ID, docHash)
		if err != nil {
			writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
			return
		}
		devices := make([]map[string]any, 0, len(rows))
		for _, rp := range rows {
			devices = append(devices, map[string]any{
				"device_id":  rp.DeviceID,
				"device":     rp.Device,
				"percentage": rp.Percentage,
				"progress":   rp.Progress,
				"position":   rp.Position,
				"timestamp":  rp.Timestamp,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"document": docHash,
			"devices":  devices,
		})
		return
	}
	writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
}

func (h *Handler) handleV1Bookmarks(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	docHash := strings.TrimPrefix(r.URL.Path, "/api/v1/bookmarks/")
	if docHash == "" {
		writeKOError(w, http.StatusForbidden, 2004, "Missing document")
		return
	}

	switch r.Method {
	case http.MethodGet:
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		items, err := h.Store.ListBookmarks(user.ID, docHash, since)
		if err != nil {
			writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
			return
		}
		if items == nil {
			items = []*db.Bookmark{}
		}
		until := time.Now().Unix()
		for _, it := range items {
			if it.UpdatedAt > until {
				until = it.UpdatedAt
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"document": docHash,
			"until":    until,
			"more":     false,
			"items":    items,
		})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Items []db.Bookmark `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeKOError(w, http.StatusForbidden, 2003, "Invalid request body")
			return
		}
		accepted := 0
		for i := range req.Items {
			bm := req.Items[i]
			bm.UserID = user.ID
			bm.DocumentHash = docHash
			if bm.ID == "" {
				bm.ID = db.MD5Hex(bm.XPath)[:16]
			}
			if err := h.Store.UpsertBookmark(&bm); err == nil {
				accepted++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"until":    time.Now().Unix(),
			"accepted": accepted,
		})
	default:
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
	}
}

func (h *Handler) handleV1Clippings(w http.ResponseWriter, r *http.Request) {
	user, ok := h.authenticateRequest(r)
	if !ok {
		writeKOError(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	docHash := strings.TrimPrefix(r.URL.Path, "/api/v1/clippings/")
	if docHash == "" {
		writeKOError(w, http.StatusForbidden, 2004, "Missing document")
		return
	}

	switch r.Method {
	case http.MethodGet:
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		items, err := h.Store.ListClippings(user.ID, docHash, since)
		if err != nil {
			writeKOError(w, http.StatusInternalServerError, 2003, err.Error())
			return
		}
		if items == nil {
			items = []*db.Clipping{}
		}
		until := time.Now().Unix()
		writeJSON(w, http.StatusOK, map[string]any{
			"document":     docHash,
			"sync_version": 2,
			"until":        until,
			"cursor":       until,
			"more":         false,
			"items":        items,
		})
	case http.MethodPut, http.MethodPost:
		var req struct {
			Items []db.Clipping `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeKOError(w, http.StatusForbidden, 2003, "Invalid request body")
			return
		}
		accepted := 0
		for i := range req.Items {
			c := req.Items[i]
			c.UserID = user.ID
			c.DocumentHash = docHash
			if c.ID == "" {
				c.ID = db.MD5Hex(c.Text)[:16]
			}
			if err := h.Store.UpsertClipping(&c); err == nil {
				accepted++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"until":    time.Now().Unix(),
			"accepted": accepted,
		})
	default:
		writeKOError(w, http.StatusMethodNotAllowed, 2003, "Method not allowed")
	}
}

func extractLastSegment(urlPath, marker string) string {
	idx := strings.Index(urlPath, marker)
	if idx < 0 {
		return ""
	}
	return strings.Trim(urlPath[idx+len(marker):], "/")
}

func emptyToNil(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
