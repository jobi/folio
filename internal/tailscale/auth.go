package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/folio-books/folio/internal/config"
	"github.com/folio-books/folio/internal/db"
)

type contextKey string

const sessionContextKey contextKey = "folio_tailscale_session"

// Session represents the resolved Tailscale identity and linked Folio user account for an HTTP request.
type Session struct {
	Authenticated   bool     `json:"authenticated"`
	AuthMode        string   `json:"auth_mode"`        // "auto", "required", "off"
	Source          string   `json:"source"`           // "tailscale-serve", "tailscaled-whois", "tailscale-auto-dev", "kosync-key", "anonymous"
	LoginName       string   `json:"login_name"`       // e.g. "reader@tailnet.ts.net"
	DisplayName     string   `json:"display_name"`     // e.g. "Folio Reader"
	ProfilePicURL   string   `json:"profile_pic_url"`  // optional avatar URL
	NodeName        string   `json:"node_name"`        // e.g. "macbook-pro.tailnet.ts.net"
	TailscaleIP     string   `json:"tailscale_ip"`     // e.g. "100.101.102.103"
	Tailnet         string   `json:"tailnet"`          // e.g. "tailnet.ts.net"
	SocketPath      string   `json:"socket_path"`      // e.g. "/var/run/tailscale/tailscaled.sock"
	SocketConnected bool     `json:"socket_connected"` // true if local tailscaled socket exists
	TrustHeaders    bool     `json:"trust_headers"`
	ProtectOPDS     bool     `json:"protect_opds"`
	AllowedUsers    []string `json:"allowed_users,omitempty"`
	User            *db.User `json:"user,omitempty"`
}

// SimulatorConfig holds an optional runtime-configurable Tailscale identity for local/dev testing when tailscaled is not mounted.
type SimulatorConfig struct {
	LoginName   string `json:"login_name"`
	DisplayName string `json:"display_name"`
	NodeName    string `json:"node_name"`
	TailscaleIP string `json:"tailscale_ip"`
}

// Authenticator resolves Tailscale identities via `tailscale serve` headers or the local `tailscaled` Unix socket WhoIs API.
type Authenticator struct {
	Store *db.Store
	Cfg   *config.Config

	mu          sync.RWMutex
	simIdentity SimulatorConfig
}

func NewAuthenticator(store *db.Store, cfg *config.Config) *Authenticator {
	return &Authenticator{
		Store: store,
		Cfg:   cfg,
		simIdentity: SimulatorConfig{
			LoginName:   "reader@tailnet.ts.net",
			DisplayName: "Reader (Tailnet Admin)",
			NodeName:    "folio-reader.tailnet.ts.net",
			TailscaleIP: "100.101.102.103",
		},
	}
}

// FromContext extracts the active Tailscale Session from an HTTP request context.
func FromContext(ctx context.Context) *Session {
	if v, ok := ctx.Value(sessionContextKey).(*Session); ok && v != nil {
		return v
	}
	return nil
}

// UpdateSimIdentity updates the local dev fallback Tailscale identity and persists/links the user in the DB.
func (a *Authenticator) UpdateSimIdentity(login, displayName, nodeName, tailscaleIP, authMode string) (*Session, error) {
	a.mu.Lock()
	if strings.TrimSpace(login) != "" {
		a.simIdentity.LoginName = strings.ToLower(strings.TrimSpace(login))
	}
	if strings.TrimSpace(displayName) != "" {
		a.simIdentity.DisplayName = strings.TrimSpace(displayName)
	}
	if strings.TrimSpace(nodeName) != "" {
		a.simIdentity.NodeName = strings.TrimSpace(nodeName)
	}
	if strings.TrimSpace(tailscaleIP) != "" {
		a.simIdentity.TailscaleIP = strings.TrimSpace(tailscaleIP)
	}
	if authMode == "auto" || authMode == "required" || authMode == "off" {
		a.Cfg.TailscaleAuthMode = authMode
	}
	sim := a.simIdentity
	a.mu.Unlock()

	u, err := a.Store.UpsertTailscaleUser(
		sim.LoginName,
		sim.DisplayName,
		"",
		sim.NodeName,
		sim.TailscaleIP,
		"tailscale",
		"",
	)
	if err != nil {
		return nil, err
	}

	return &Session{
		Authenticated:   true,
		AuthMode:        a.Cfg.TailscaleAuthMode,
		Source:          "tailscale-auto-dev",
		LoginName:       sim.LoginName,
		DisplayName:     sim.DisplayName,
		NodeName:        sim.NodeName,
		TailscaleIP:     sim.TailscaleIP,
		Tailnet:         extractTailnet(sim.LoginName, sim.NodeName),
		SocketPath:      a.Cfg.TailscaleSocket,
		SocketConnected: a.socketExists(),
		TrustHeaders:    a.Cfg.TailscaleTrustHeaders,
		ProtectOPDS:     a.Cfg.TailscaleProtectOPDS,
		AllowedUsers:    a.Cfg.TailscaleAllowedUsers,
		User:            u,
	}, nil
}

func (a *Authenticator) socketExists() bool {
	if a.Cfg.TailscaleSocket == "" {
		return false
	}
	info, err := os.Stat(a.Cfg.TailscaleSocket)
	return err == nil && !info.IsDir()
}

// Resolve inspects the request for Tailscale identity headers or queries the local tailscaled WhoIs API.
func (a *Authenticator) Resolve(r *http.Request) *Session {
	a.mu.RLock()
	authMode := a.Cfg.TailscaleAuthMode
	sim := a.simIdentity
	a.mu.RUnlock()

	socketConnected := a.socketExists()
	sess := &Session{
		Authenticated:   false,
		AuthMode:        authMode,
		Source:          "anonymous",
		SocketPath:      a.Cfg.TailscaleSocket,
		SocketConnected: socketConnected,
		TrustHeaders:    a.Cfg.TailscaleTrustHeaders,
		ProtectOPDS:     a.Cfg.TailscaleProtectOPDS,
		AllowedUsers:    a.Cfg.TailscaleAllowedUsers,
	}

	// 1. Check Tailscale Identity Headers injected by `tailscale serve` or reverse proxy
	if a.Cfg.TailscaleTrustHeaders {
		hdrLogin := strings.TrimSpace(r.Header.Get("Tailscale-User-Login"))
		if hdrLogin != "" {
			hdrName := strings.TrimSpace(r.Header.Get("Tailscale-User-Name"))
			if hdrName == "" {
				hdrName = hdrLogin
			}
			hdrPic := strings.TrimSpace(r.Header.Get("Tailscale-User-Profile-Pic"))
			hdrNode := strings.TrimSpace(r.Header.Get("X-Tailscale-Node"))
			if hdrNode == "" {
				hdrNode = "tailscale-serve.tailnet.ts.net"
			}
			hdrIP := extractRemoteIP(r)
			if !isTailscaleIP(hdrIP) {
				hdrIP = "100.64.0.1"
			}

			sess.Authenticated = true
			sess.Source = "tailscale-serve"
			sess.LoginName = strings.ToLower(hdrLogin)
			sess.DisplayName = hdrName
			sess.ProfilePicURL = hdrPic
			sess.NodeName = hdrNode
			sess.TailscaleIP = hdrIP
			sess.Tailnet = extractTailnet(sess.LoginName, sess.NodeName)
			if u, err := a.Store.UpsertTailscaleUser(sess.LoginName, sess.DisplayName, sess.ProfilePicURL, sess.NodeName, sess.TailscaleIP, "tailscale-serve", ""); err == nil {
				sess.User = u
			}
			return sess
		}
	}

	// 2. Query local tailscaled Unix socket WhoIs API if socket exists
	if socketConnected {
		if whois, err := a.queryTailscaledWhoIs(r.Context(), r.RemoteAddr); err == nil && whois != nil && whois.UserProfile.LoginName != "" {
			nodeName := strings.TrimSuffix(whois.Node.ComputedName, ".")
			if nodeName == "" {
				nodeName = strings.TrimSuffix(whois.Node.Name, ".")
			}
			nodeIP := extractRemoteIP(r)
			if len(whois.Node.Addresses) > 0 {
				if pfx, _, ok := strings.Cut(whois.Node.Addresses[0], "/"); ok {
					nodeIP = pfx
				}
			}

			sess.Authenticated = true
			sess.Source = "tailscaled-whois"
			sess.LoginName = strings.ToLower(whois.UserProfile.LoginName)
			sess.DisplayName = whois.UserProfile.DisplayName
			if sess.DisplayName == "" {
				sess.DisplayName = sess.LoginName
			}
			sess.ProfilePicURL = whois.UserProfile.ProfilePicURL
			sess.NodeName = nodeName
			sess.TailscaleIP = nodeIP
			sess.Tailnet = extractTailnet(sess.LoginName, sess.NodeName)
			if u, err := a.Store.UpsertTailscaleUser(sess.LoginName, sess.DisplayName, sess.ProfilePicURL, sess.NodeName, sess.TailscaleIP, "tailscaled-whois", ""); err == nil {
				sess.User = u
			}
			return sess
		}
	}

	// 3. In "auto" mode when no tailscaled socket is present, provide the local/dev Tailscale identity
	if authMode == "auto" {
		sess.Authenticated = true
		sess.Source = "tailscale-auto-dev"
		sess.LoginName = sim.LoginName
		sess.DisplayName = sim.DisplayName
		sess.NodeName = sim.NodeName
		sess.TailscaleIP = sim.TailscaleIP
		sess.Tailnet = extractTailnet(sim.LoginName, sim.NodeName)
		if u, err := a.Store.UpsertTailscaleUser(sess.LoginName, sess.DisplayName, "", sess.NodeName, sess.TailscaleIP, "tailscale", ""); err == nil {
			sess.User = u
		}
		return sess
	}

	// 4. In "off" mode, bind the default user without enforcing Tailscale
	if authMode == "off" {
		sess.Authenticated = true
		sess.Source = "off"
		if u, err := a.Store.GetDefaultUser(); err == nil && u != nil {
			sess.User = u
			sess.LoginName = u.TailscaleLogin
			sess.DisplayName = u.DisplayName
		}
	}

	return sess
}

// Middleware wraps the HTTP mux, attaches the Tailscale session to context, and enforces Tailscale access policy.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := a.Resolve(r)
		ctx := context.WithValue(r.Context(), sessionContextKey, sess)
		r = r.WithContext(ctx)

		// Add informative response headers so clients can inspect Tailscale auth state
		w.Header().Set("X-Folio-Auth-Mode", sess.AuthMode)
		if sess.LoginName != "" {
			w.Header().Set("X-Tailscale-User", sess.LoginName)
		}

		// Always allow healthcheck and KOReader / CrossPoint API endpoints that carry their own x-auth-key
		path := r.URL.Path
		if path == "/healthz" || isDeviceSyncPath(path) {
			next.ServeHTTP(w, r)
			return
		}

		// OPDS catalog routes are protected only if TAILSCALE_PROTECT_OPDS=true
		if strings.HasPrefix(path, "/opds") && !sess.ProtectOPDS {
			next.ServeHTTP(w, r)
			return
		}

		// Check user allowlist if configured
		if sess.Authenticated && len(sess.AllowedUsers) > 0 && sess.LoginName != "" {
			if !isAllowedUser(sess.LoginName, sess.AllowedUsers) {
				writeTailscaleDenied(w, r, http.StatusForbidden,
					fmt.Sprintf("Tailscale user %q is not in TAILSCALE_ALLOWED_USERS", sess.LoginName), sess)
				return
			}
		}

		// In "required" mode, block unauthenticated requests to Web UI and API
		if sess.AuthMode == "required" && !sess.Authenticated {
			writeTailscaleDenied(w, r, http.StatusUnauthorized,
				"Tailscale authentication required. Access Folio via your Tailnet (tailscale serve / tailscaled WhoIs).", sess)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type tailscaledWhoIsResponse struct {
	Node struct {
		Name         string   `json:"Name"`
		ComputedName string   `json:"ComputedName"`
		Addresses    []string `json:"Addresses"`
	} `json:"Node"`
	UserProfile struct {
		ID            int64  `json:"ID"`
		LoginName     string `json:"LoginName"`
		DisplayName   string `json:"DisplayName"`
		ProfilePicURL string `json:"ProfilePicURL"`
	} `json:"UserProfile"`
}

func (a *Authenticator) queryTailscaledWhoIs(ctx context.Context, remoteAddr string) (*tailscaledWhoIsResponse, error) {
	sockPath := a.Cfg.TailscaleSocket
	if sockPath == "" {
		return nil, fmt.Errorf("no tailscaled socket configured")
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   1500 * time.Millisecond,
	}

	reqURL := "http://local-tailscaled.sock/localapi/v0/whois?addr=" + url.QueryEscape(remoteAddr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Sec-Tailscale", "localapi")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tailscaled whois status %d", resp.StatusCode)
	}

	var out tailscaledWhoIsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func isDeviceSyncPath(path string) bool {
	return strings.HasPrefix(path, "/users/") ||
		strings.HasPrefix(path, "/syncs/") ||
		strings.HasPrefix(path, "/kosync/") ||
		strings.HasPrefix(path, "/api/v1/")
}

func isAllowedUser(login string, allowed []string) bool {
	login = strings.ToLower(strings.TrimSpace(login))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == login || a == "*" {
			return true
		}
		// Support matching @domain
		if strings.HasPrefix(a, "@") && strings.HasSuffix(login, a) {
			return true
		}
	}
	return false
}

func extractRemoteIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func isTailscaleIP(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	// Tailscale CGNAT 100.64.0.0/10 or IPv6 fd7a:115c:a1e0::/48
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	_, ts6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return (cgnat != nil && cgnat.Contains(ip)) || (ts6 != nil && ts6.Contains(ip))
}

func extractTailnet(login, node string) string {
	if _, domain, ok := strings.Cut(node, "."); ok && domain != "" {
		return domain
	}
	if _, domain, ok := strings.Cut(login, "@"); ok && domain != "" {
		return domain
	}
	return "tailnet.ts.net"
}

func writeTailscaleDenied(w http.ResponseWriter, r *http.Request, status int, msg string, sess *Session) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     msg,
			"auth_mode": sess.AuthMode,
			"tailscale": sess,
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Tailscale Authentication Required — Folio</title>
<style>
  body { font-family: system-ui, sans-serif; background: #141210; color: #f5efe6; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
  .card { max-width: 460px; padding: 32px; background: #1e1b18; border: 1px solid #352f2a; border-radius: 14px; box-shadow: 0 20px 50px rgba(0,0,0,0.45); }
  .badge { display: inline-flex; align-items: center; gap: 8px; font-size: 12px; text-transform: uppercase; letter-spacing: 0.08em; color: #d97742; background: rgba(217,119,66,0.12); padding: 5px 10px; border-radius: 999px; margin-bottom: 16px; }
  h1 { margin: 0 0 12px; font-size: 22px; }
  p { color: #b5aba0; line-height: 1.55; font-size: 14px; }
  code { background: #12100e; padding: 2px 6px; border-radius: 5px; color: #e8b89a; font-size: 13px; }
</style>
</head>
<body>
  <div class="card">
    <div class="badge">Tailscale Zero-Trust Auth</div>
    <h1>Tailscale Connection Required</h1>
    <p>%s</p>
    <p>Connect to your tailnet and open Folio via your Tailscale MagicDNS hostname or configure <code>tailscale serve</code> to inject <code>Tailscale-User-Login</code> headers.</p>
  </div>
</body>
</html>`, msg)
}
