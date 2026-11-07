package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds the runtime configuration for the Folio server.
type Config struct {
	Port                int
	DBDriver            string // "sqlite" (default) or "postgres"
	DBDSN               string // Path for SQLite or connection URI for PostgreSQL
	DataDir             string // Directory for stored EPUBs and extracted covers
	BaseURL             string // Optional public base URL for OPDS links
	DefaultSyncUser     string // Default KOReader sync username created on first boot
	DefaultSyncPassword string // Default KOReader sync plaintext password (stored as MD5)
	RegistrationEnabled bool   // Whether POST /users/create is open
	SeedSampleBooks     bool   // Generate sample public-domain EPUBs if library is empty
	EnableMockX3        bool   // Enable built-in simulated Xteink X3 CrossPoint listener for testing
	MockX3HTTPPort      int    // Simulated X3 HTTP port (default 18080)
	MockX3WSPort        int    // Simulated X3 WebSocket upload port (default 18081)

	// Tailscale Authentication Settings
	TailscaleAuthMode     string   // "auto" (default: Tailscale WhoIs/Headers + dev fallback), "required" (strict Tailscale only), or "off"
	TailscaleSocket       string   // Path to local tailscaled unix socket (default: /var/run/tailscale/tailscaled.sock)
	TailscaleTrustHeaders bool     // Trust Tailscale-User-Login / Tailscale-User-Name headers from tailscale serve proxy
	TailscaleAllowedUsers []string // Optional comma-separated allowlist of Tailscale login emails
	TailscaleProtectOPDS  bool     // Require Tailscale or Basic Auth on /opds feeds
}

// Load reads configuration from environment variables with sensible defaults for Docker and Linux.
func Load() *Config {
	dataDir := getEnv("DATA_DIR", "./data")
	dbDriver := strings.ToLower(strings.TrimSpace(getEnv("DB_DRIVER", "sqlite")))
	if dbDriver == "postgresql" || dbDriver == "pgx" {
		dbDriver = "postgres"
	}
	if dbDriver == "sqlite3" || dbDriver == "" {
		dbDriver = "sqlite"
	}

	defaultDSN := filepath.Join(dataDir, "folio.db")
	if dbDriver == "postgres" {
		defaultDSN = "postgres://folio:folio@localhost:5432/folio?sslmode=disable"
	}

	var allowedUsers []string
	if raw := getEnv("TAILSCALE_ALLOWED_USERS", ""); raw != "" {
		for _, u := range strings.Split(raw, ",") {
			if trimmed := strings.ToLower(strings.TrimSpace(u)); trimmed != "" {
				allowedUsers = append(allowedUsers, trimmed)
			}
		}
	}

	return &Config{
		Port:                  getEnvInt("PORT", 8080),
		DBDriver:              dbDriver,
		DBDSN:                 getEnv("DB_DSN", defaultDSN),
		DataDir:               dataDir,
		BaseURL:               strings.TrimRight(getEnv("BASE_URL", ""), "/"),
		DefaultSyncUser:       getEnv("DEFAULT_SYNC_USER", "reader"),
		DefaultSyncPassword:   getEnv("DEFAULT_SYNC_PASSWORD", "folio123"),
		RegistrationEnabled:   getEnvBool("REGISTRATION_ENABLED", true),
		SeedSampleBooks:       getEnvBool("SEED_SAMPLE_BOOKS", true),
		EnableMockX3:          getEnvBool("ENABLE_MOCK_X3", true),
		MockX3HTTPPort:        getEnvInt("MOCK_X3_HTTP_PORT", 18080),
		MockX3WSPort:          getEnvInt("MOCK_X3_WS_PORT", 18081),
		TailscaleAuthMode:     strings.ToLower(getEnv("TAILSCALE_AUTH_MODE", "auto")),
		TailscaleSocket:       getEnv("TS_SOCKET", getEnv("TAILSCALE_SOCKET", "/var/run/tailscale/tailscaled.sock")),
		TailscaleTrustHeaders: getEnvBool("TAILSCALE_TRUST_HEADERS", true),
		TailscaleAllowedUsers: allowedUsers,
		TailscaleProtectOPDS:  getEnvBool("TAILSCALE_PROTECT_OPDS", false),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		v = strings.ToLower(strings.TrimSpace(v))
		switch v {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}
