package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/folio-books/folio/internal/api"
	"github.com/folio-books/folio/internal/config"
	"github.com/folio-books/folio/internal/crosspoint"
	"github.com/folio-books/folio/internal/db"
	"github.com/folio-books/folio/internal/epub"
	"github.com/folio-books/folio/internal/kosync"
	"github.com/folio-books/folio/internal/opds"
)

func main() {
	cfg := config.Load()

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "books"), 0o755); err != nil {
		log.Fatalf("failed to create books dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "covers"), 0o755); err != nil {
		log.Fatalf("failed to create covers dir: %v", err)
	}

	store, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		log.Fatalf("database initialization failed (%s): %v", cfg.DBDriver, err)
	}
	defer store.Close()

	if err := store.EnsureDefaultUser(cfg.DefaultSyncUser, cfg.DefaultSyncPassword); err != nil {
		log.Printf("warning: ensure default user: %v", err)
	}

	// Start optional simulated Xteink X3 CrossPoint listener for local testing
	if cfg.EnableMockX3 {
		simDir := filepath.Join(cfg.DataDir, "mock-x3-sdcard")
		sim := crosspoint.NewSimulator(simDir, cfg.MockX3HTTPPort, cfg.MockX3WSPort)
		if err := sim.Start(); err != nil {
			log.Printf("warning: mock X3 simulator start: %v", err)
		} else {
			log.Printf("Xteink X3 CrossPoint Simulator listening on HTTP 127.0.0.1:%d, WebSocket 127.0.0.1:%d", cfg.MockX3HTTPPort, cfg.MockX3WSPort)
		}
	}

	// Ensure default Xteink X3 device entries exist
	ensureDefaultDevices(store, cfg)

	apiSrv := api.NewServer(store, cfg)

	// Seed sample classic EPUBs if library is empty
	if cfg.SeedSampleBooks {
		seedSampleLibraryIfNeeded(store, apiSrv, cfg)
	}

	mux := http.NewServeMux()
	kosync.NewHandler(store, cfg).RegisterRoutes(mux)
	opds.NewHandler(store, cfg).RegisterRoutes(mux)
	apiSrv.RegisterRoutes(mux)

	// Serve frontend assets from ./web
	webDir := "./web"
	if _, err := os.Stat(webDir); err != nil {
		if _, err2 := os.Stat("/app/web"); err2 == nil {
			webDir = "/app/web"
		}
	}
	fs := http.FileServer(http.Dir(webDir))
	mux.Handle("/", fs)

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	log.Printf("Folio EPUB Library Server listening on http://localhost:%d (DB: %s)", cfg.Port, store.Driver)
	log.Printf("  -> Tailscale Auth  : mode=%s (socket=%s, trust_headers=%v)", cfg.TailscaleAuthMode, cfg.TailscaleSocket, cfg.TailscaleTrustHeaders)
	log.Printf("  -> Web UI & Reader : http://localhost:%d", cfg.Port)
	log.Printf("  -> OPDS 1.2 Feed   : http://localhost:%d/opds", cfg.Port)
	log.Printf("  -> OPDS 2.0 Feed   : http://localhost:%d/opds/v2", cfg.Port)
	log.Printf("  -> KOReader Sync   : http://localhost:%d (user: %s)", cfg.Port, cfg.DefaultSyncUser)

	server := &http.Server{
		Addr:              addr,
		Handler:           withCORS(apiSrv.TSAuth.Middleware(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, x-auth-user, x-auth-key, X-Folio-Web, Tailscale-User-Login, Tailscale-User-Name, Tailscale-User-Profile-Pic, X-Tailscale-Node")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ensureDefaultDevices(store *db.Store, cfg *config.Config) {
	existing, err := store.ListDevices()
	if err != nil || len(existing) > 0 {
		return
	}

	if cfg.EnableMockX3 {
		_, _ = store.SaveDevice(&db.Device{
			Name:              "Xteink X3 (Local CrossPoint Simulator)",
			DeviceModel:       "Xteink X3",
			Host:              "127.0.0.1",
			HTTPPort:          cfg.MockX3HTTPPort,
			WSPort:            cfg.MockX3WSPort,
			TargetPath:        "/Books",
			PreferredProtocol: "auto",
			IsDefault:         true,
		})
	}

	_, _ = store.SaveDevice(&db.Device{
		Name:              "My Xteink X3 (crosspoint.local)",
		DeviceModel:       "Xteink X3",
		Host:              "crosspoint.local",
		HTTPPort:          80,
		WSPort:            81,
		TargetPath:        "/Books",
		PreferredProtocol: "auto",
		IsDefault:         !cfg.EnableMockX3,
	})
}

func seedSampleLibraryIfNeeded(store *db.Store, apiSrv *api.Server, cfg *config.Config) {
	books, err := store.ListBooks("", "", "", "", "", "recent")
	if err != nil || len(books) > 0 {
		return
	}

	log.Println("Seeding initial classic EPUB 3 volumes...")
	specs := epub.DefaultSampleSpecs()
	booksDir := filepath.Join(cfg.DataDir, "books")
	var firstBook *db.Book

	for i, spec := range specs {
		dest := filepath.Join(booksDir, spec.FileName)
		if err := epub.WriteSampleEPUB(dest, spec); err != nil {
			log.Printf("warning: write sample epub %s: %v", spec.FileName, err)
			continue
		}
		b, err := apiSrv.ImportEPUBFile(dest, spec.FileName, 0)
		if err != nil {
			log.Printf("warning: import sample epub %s: %v", spec.FileName, err)
			continue
		}
		if i == 0 {
			firstBook = b
		}
	}

	// Seed an initial KOReader / CrossPoint progress entry on the first book
	if firstBook != nil {
		if u, err := store.GetDefaultUser(); err == nil && u != nil {
			_, _ = store.UpsertProgress(&db.ReadingProgress{
				UserID:       u.ID,
				DocumentHash: firstBook.KOMD5Partial,
				BookID:       firstBook.ID,
				Device:       "Xteink X3 (CrossPoint)",
				DeviceID:     "crosspoint-x3",
				Progress:     "/body/DocFragment[1]/body/section[1]/p[3]/text().0",
				Percentage:   0.34,
				Position: &db.CompactPosition{
					PctQ:  340000,
					Spine: 0,
					Page:  3,
					Pages: 9,
					Para:  3,
					XPath: "/body/DocFragment[1]/body/section[1]/p[3]/text().0",
				},
				Metadata: &db.SyncMetadata{
					Filename: firstBook.FileName,
					Title:    firstBook.Title,
					Authors:  firstBook.Authors,
					Language: firstBook.Language,
				},
				Timestamp: time.Now().Unix() - 120,
			})
		}
	}
}
