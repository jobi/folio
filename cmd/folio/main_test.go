package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/folio-books/folio/internal/api"
	"github.com/folio-books/folio/internal/config"
	"github.com/folio-books/folio/internal/crosspoint"
	"github.com/folio-books/folio/internal/db"
	"github.com/folio-books/folio/internal/epub"
	"github.com/folio-books/folio/internal/kosync"
	"github.com/folio-books/folio/internal/opds"
)

func TestFolioEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Port:                8080,
		DBDriver:            "sqlite",
		DBDSN:               filepath.Join(tmpDir, "test.db"),
		DataDir:             tmpDir,
		DefaultSyncUser:     "reader",
		DefaultSyncPassword: "secretpassword",
		RegistrationEnabled: true,
		SeedSampleBooks:     true,
		EnableMockX3:        true,
		MockX3HTTPPort:      19080,
		MockX3WSPort:        19081,
	}

	// Verify PostgreSQL Rebind helper
	pgStore := &db.Store{Driver: "postgres"}
	rebound := pgStore.Rebind("SELECT * FROM books WHERE id = ? AND language = ?")
	if rebound != "SELECT * FROM books WHERE id = $1 AND language = $2" {
		t.Fatalf("unexpected postgres rebind: %s", rebound)
	}

	store, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer store.Close()

	if err := store.EnsureDefaultUser(cfg.DefaultSyncUser, cfg.DefaultSyncPassword); err != nil {
		t.Fatalf("ensure default user: %v", err)
	}

	// Start CrossPoint Reader Simulator on test ports
	simDir := filepath.Join(tmpDir, "mock-x3")
	sim := crosspoint.NewSimulator(simDir, cfg.MockX3HTTPPort, cfg.MockX3WSPort)
	if err := sim.Start(); err != nil {
		t.Fatalf("start simulator: %v", err)
	}

	cfg.TailscaleAuthMode = "auto"
	cfg.TailscaleTrustHeaders = true

	ensureDefaultDevices(store, cfg)
	apiSrv := api.NewServer(store, cfg)
	seedSampleLibraryIfNeeded(store, apiSrv, cfg)

	mux := http.NewServeMux()
	kosync.NewHandler(store, cfg).RegisterRoutes(mux)
	opds.NewHandler(store, cfg).RegisterRoutes(mux)
	apiSrv.RegisterRoutes(mux)

	ts := httptest.NewServer(apiSrv.TSAuth.Middleware(mux))
	defer ts.Close()

	// 1. Verify books were seeded with cover images and KOReader hashes
	resp, err := http.Get(ts.URL + "/api/books")
	if err != nil {
		t.Fatalf("GET /api/books: %v", err)
	}
	defer resp.Body.Close()
	var booksResp struct {
		Books []*db.Book `json:"books"`
		Total int        `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&booksResp); err != nil {
		t.Fatalf("decode books: %v", err)
	}
	if booksResp.Total != 4 {
		t.Fatalf("expected 4 seeded books, got %d", booksResp.Total)
	}
	firstBook := booksResp.Books[0]
	if firstBook.KOMD5Partial == "" || firstBook.KOMD5Filename == "" {
		t.Fatalf("expected KOReader hashes on book, got partial=%q filename=%q", firstBook.KOMD5Partial, firstBook.KOMD5Filename)
	}

	// 2. Verify cover image endpoint returns valid PNG
	coverResp, err := http.Get(ts.URL + fmt.Sprintf("/api/books/%d/cover", firstBook.ID))
	if err != nil || coverResp.StatusCode != http.StatusOK {
		t.Fatalf("GET cover failed: %v", err)
	}
	coverResp.Body.Close()

	// 3. Verify Web Reader manifest & chapter endpoints
	manResp, err := http.Get(ts.URL + fmt.Sprintf("/api/books/%d/manifest", firstBook.ID))
	if err != nil || manResp.StatusCode != http.StatusOK {
		t.Fatalf("GET manifest failed: %v", err)
	}
	var manifest epub.BookManifest
	_ = json.NewDecoder(manResp.Body).Decode(&manifest)
	manResp.Body.Close()
	if len(manifest.Chapters) == 0 {
		t.Fatalf("expected chapters in manifest")
	}

	chResp, err := http.Get(ts.URL + fmt.Sprintf("/api/books/%d/chapter/0", firstBook.ID))
	if err != nil || chResp.StatusCode != http.StatusOK {
		t.Fatalf("GET chapter/0 failed: %v", err)
	}
	chResp.Body.Close()

	// 4. Verify KOReader Sync (auth + PUT /syncs/progress + GET /syncs/progress/{doc})
	passKey := db.MD5Hex(cfg.DefaultSyncPassword)
	reqAuth, _ := http.NewRequest(http.MethodGet, ts.URL+"/users/auth", nil)
	reqAuth.Header.Set("x-auth-user", cfg.DefaultSyncUser)
	reqAuth.Header.Set("x-auth-key", passKey)
	authResp, err := http.DefaultClient.Do(reqAuth)
	if err != nil || authResp.StatusCode != http.StatusOK {
		t.Fatalf("KOSync auth failed: %v", err)
	}
	authResp.Body.Close()

	putBody := map[string]any{
		"document":   firstBook.KOMD5Partial,
		"progress":   "/body/DocFragment[2]/body/p[4]/text().0",
		"percentage": 0.62,
		"device":     "CrossPoint Reader",
		"device_id":  "crosspoint-test",
	}
	putBytes, _ := json.Marshal(putBody)
	reqPut, _ := http.NewRequest(http.MethodPut, ts.URL+"/syncs/progress", bytes.NewReader(putBytes))
	reqPut.Header.Set("Content-Type", "application/json")
	reqPut.Header.Set("x-auth-user", cfg.DefaultSyncUser)
	reqPut.Header.Set("x-auth-key", passKey)
	putResp, err := http.DefaultClient.Do(reqPut)
	if err != nil || putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /syncs/progress failed: %v", err)
	}
	putResp.Body.Close()

	reqGetProg, _ := http.NewRequest(http.MethodGet, ts.URL+"/syncs/progress/"+firstBook.KOMD5Partial, nil)
	reqGetProg.Header.Set("x-auth-user", cfg.DefaultSyncUser)
	reqGetProg.Header.Set("x-auth-key", passKey)
	getProgResp, err := http.DefaultClient.Do(reqGetProg)
	if err != nil || getProgResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /syncs/progress failed: %v", err)
	}
	var syncedProg map[string]any
	_ = json.NewDecoder(getProgResp.Body).Decode(&syncedProg)
	getProgResp.Body.Close()
	if syncedProg["percentage"] != 0.62 {
		t.Fatalf("expected synced percentage 0.62, got %v", syncedProg["percentage"])
	}

	// 5. Verify OPDS 1.2 XML and OPDS 2.0 JSON catalogs
	opdsResp, err := http.Get(ts.URL + "/opds/books")
	if err != nil || opdsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /opds/books failed: %v", err)
	}
	var opdsBuf bytes.Buffer
	_, _ = opdsBuf.ReadFrom(opdsResp.Body)
	opdsResp.Body.Close()
	if !strings.Contains(opdsBuf.String(), "http://opds-spec.org/acquisition") {
		t.Fatalf("expected OPDS acquisition link in /opds/books")
	}

	opds2Resp, err := http.Get(ts.URL + "/opds/v2")
	if err != nil || opds2Resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /opds/v2 failed: %v", err)
	}
	opds2Resp.Body.Close()

	// 6. Verify pushing book to CrossPoint Reader via WebSocket Fast Upload (port 19081) runs CrossPoint EPUB Optimizer
	devices, _ := store.ListDevices()
	if len(devices) == 0 {
		t.Fatalf("expected default devices")
	}
	simDev := devices[0]

	pushPayload, _ := json.Marshal(map[string]any{
		"book_id":       firstBook.ID,
		"target_path":   "/Books",
		"protocol":      "websocket",
		"optimize":      true,
		"device_target": "X3",
	})
	pushResp, err := http.Post(ts.URL+fmt.Sprintf("/api/devices/%d/push", simDev.ID), "application/json", bytes.NewReader(pushPayload))
	if err != nil || pushResp.StatusCode != http.StatusOK {
		t.Fatalf("WebSocket push to CrossPoint simulator failed: status=%v err=%v", pushResp.StatusCode, err)
	}
	var pushResultData struct {
		OK           bool                  `json:"ok"`
		Optimization *epub.OptimizeSummary `json:"optimization"`
	}
	_ = json.NewDecoder(pushResp.Body).Decode(&pushResultData)
	pushResp.Body.Close()
	if pushResultData.Optimization == nil || pushResultData.Optimization.Profile != "X3" {
		t.Fatalf("expected CrossPoint X3 optimization summary in push response, got %+v", pushResultData.Optimization)
	}

	// Verify the pushed EPUB exists on the simulated CrossPoint Reader SD card and its MD5 alias resolves back to firstBook
	pushedPath := filepath.Join(simDir, "Books", firstBook.FileName)
	if _, err := os.Stat(pushedPath); err != nil {
		t.Fatalf("expected pushed EPUB on CrossPoint SD card at %s: %v", pushedPath, err)
	}
	pushedMD5, err := epub.ComputeKOReaderPartialMD5(pushedPath)
	if err != nil || pushedMD5 == "" {
		t.Fatalf("compute partial MD5 of pushed EPUB: %v", err)
	}
	matchedBook, err := store.FindBookByDocumentHash(pushedMD5, nil)
	if err != nil || matchedBook == nil || matchedBook.ID != firstBook.ID {
		t.Fatalf("expected optimized EPUB MD5 (%s) to resolve to book ID %d via book_hash_aliases, got err=%v book=%+v", pushedMD5, firstBook.ID, err, matchedBook)
	}

	// Verify OPDS / download with ?optimize=crosspoint serves an optimized EPUB with X-CrossPoint-Optimized header
	dlResp, err := http.Get(ts.URL + fmt.Sprintf("/api/books/%d/download/%s?optimize=crosspoint&profile=x3", firstBook.ID, firstBook.FileName))
	if err != nil || dlResp.StatusCode != http.StatusOK {
		t.Fatalf("GET optimized download failed: %v", err)
	}
	if dlResp.Header.Get("X-CrossPoint-Optimized") != "X3" {
		t.Fatalf("expected X-CrossPoint-Optimized: X3 header, got %q", dlResp.Header.Get("X-CrossPoint-Optimized"))
	}
	dlResp.Body.Close()

	// 7. Verify Tailscale Authentication (Serve identity headers, user auto-provisioning, and strict mode)
	reqTS, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/auth/tailscale", nil)
	reqTS.Header.Set("Tailscale-User-Login", "alice@example.ts.net")
	reqTS.Header.Set("Tailscale-User-Name", "Alice Tailnet")
	reqTS.Header.Set("X-Tailscale-Node", "alice-macbook.example.ts.net")
	tsResp, err := http.DefaultClient.Do(reqTS)
	if err != nil || tsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/auth/tailscale with headers failed: %v", err)
	}
	var tsSess struct {
		Authenticated bool     `json:"authenticated"`
		Source        string   `json:"source"`
		LoginName     string   `json:"login_name"`
		User          *db.User `json:"user"`
	}
	_ = json.NewDecoder(tsResp.Body).Decode(&tsSess)
	tsResp.Body.Close()
	if !tsSess.Authenticated || tsSess.Source != "tailscale-serve" || tsSess.LoginName != "alice@example.ts.net" || tsSess.User == nil {
		t.Fatalf("unexpected tailscale session: %+v", tsSess)
	}

	// Switch to strict "required" mode and verify unauthenticated requests are blocked while Tailscale header requests succeed
	cfg.TailscaleAuthMode = "required"
	unauthReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/books", nil)
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil || unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 in strict Tailscale mode without identity, got %v", unauthResp.StatusCode)
	}
	unauthResp.Body.Close()

	authReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/books", nil)
	authReq.Header.Set("Tailscale-User-Login", "alice@example.ts.net")
	authBooksResp, err := http.DefaultClient.Do(authReq)
	if err != nil || authBooksResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 in strict Tailscale mode with Tailscale-User-Login header, got %v", authBooksResp.StatusCode)
	}
	authBooksResp.Body.Close()
}
