// Package auth provides social login (GitHub / Google OAuth2) — a simpler
// alternative to a generic OIDC provider. It issues a signed session cookie
// after login and exposes UserID(r) so the scoring API can attribute solves
// to a real user.
//
// Providers are enabled purely by presence of their client id/secret env
// vars, so a deployment can turn on whichever social logins it wants with no
// code change. When no provider is configured the platform still runs; users
// are just "anonymous".
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

const (
	sessionCookie = "training_session"
	stateCookie   = "training_oauth_state"
)

type provider struct {
	name        string
	cfg         *oauth2.Config
	userInfoURL string
	idField     string // JSON field from userinfo used as the stable user id
}

// Manager holds the configured social providers and signs session cookies.
type Manager struct {
	providers map[string]*provider
	secret    []byte
	secure    bool
}

// Options configures the Manager.
type Options struct {
	// BaseURL is the platform's externally-reachable origin (e.g.
	// https://training.example.com); OAuth redirect URLs derive from it.
	BaseURL string
	// Secret signs session cookies (any non-empty string; keep it stable
	// across replicas or logins won't survive a pod hop).
	Secret string
	// Secure marks cookies Secure (set true behind HTTPS).
	Secure bool
	// GitHub / Google credentials — empty pair disables that provider.
	GitHubClientID, GitHubClientSecret string
	GoogleClientID, GoogleClientSecret string
}

// New builds a Manager. It returns (nil, nil) when no provider is configured,
// letting callers treat "no social login" as a valid, anonymous mode.
func New(o Options) (*Manager, error) {
	// Decide up front whether any provider is even configured; with none, the
	// platform runs anonymously and no Secret is needed (checked below).
	hasGitHub := o.GitHubClientID != "" && o.GitHubClientSecret != ""
	hasGoogle := o.GoogleClientID != "" && o.GoogleClientSecret != ""
	if !hasGitHub && !hasGoogle {
		return nil, nil
	}
	if o.Secret == "" {
		return nil, fmt.Errorf("auth: Secret is required when a provider is set")
	}
	m := &Manager{providers: map[string]*provider{}, secret: []byte(o.Secret), secure: o.Secure}
	base := strings.TrimRight(o.BaseURL, "/")

	if o.GitHubClientID != "" && o.GitHubClientSecret != "" {
		m.providers["github"] = &provider{
			name:        "github",
			userInfoURL: "https://api.github.com/user",
			idField:     "login",
			cfg: &oauth2.Config{
				ClientID:     o.GitHubClientID,
				ClientSecret: o.GitHubClientSecret,
				Endpoint:     github.Endpoint,
				RedirectURL:  base + "/auth/callback/github",
				Scopes:       []string{"read:user"},
			},
		}
	}
	if o.GoogleClientID != "" && o.GoogleClientSecret != "" {
		m.providers["google"] = &provider{
			name:        "google",
			userInfoURL: "https://www.googleapis.com/oauth2/v2/userinfo",
			idField:     "email",
			cfg: &oauth2.Config{
				ClientID:     o.GoogleClientID,
				ClientSecret: o.GoogleClientSecret,
				Endpoint:     google.Endpoint,
				RedirectURL:  base + "/auth/callback/google",
				Scopes:       []string{"openid", "email"},
			},
		}
	}
	if len(m.providers) == 0 {
		return nil, nil
	}
	return m, nil
}

// Handler mounts /auth/providers, /auth/login/{provider},
// /auth/callback/{provider}, /auth/logout and /auth/me.
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/providers", m.handleProviders)
	mux.HandleFunc("/auth/login/", m.handleLogin)
	mux.HandleFunc("/auth/callback/", m.handleCallback)
	mux.HandleFunc("/auth/logout", m.handleLogout)
	mux.HandleFunc("/auth/me", m.handleMe)
	return mux
}

// UserID returns the logged-in user id ("<provider>:<id>") from the signed
// session cookie, or "anonymous" if not logged in / invalid.
func (m *Manager) UserID(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "anonymous"
	}
	if v, ok := m.verify(c.Value); ok {
		return v
	}
	return "anonymous"
}

func (m *Manager) handleProviders(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(m.providers))
	for n := range m.providers {
		names = append(names, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": names})
}

func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
	p, ok := m.providers[strings.TrimPrefix(r.URL.Path, "/auth/login/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	state := randToken()
	http.SetCookie(w, m.cookie(stateCookie, state, 10*time.Minute))
	http.Redirect(w, r, p.cfg.AuthCodeURL(state), http.StatusFound)
}

func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request) {
	p, ok := m.providers[strings.TrimPrefix(r.URL.Path, "/auth/callback/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	// CSRF: the state we set must match the one returned.
	sc, err := r.Cookie(stateCookie)
	if err != nil || sc.Value == "" || sc.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := p.cfg.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "oauth exchange failed", http.StatusBadGateway)
		return
	}
	id, err := p.fetchUserID(ctx, tok)
	if err != nil {
		http.Error(w, "could not read user profile", http.StatusBadGateway)
		return
	}
	userID := p.name + ":" + id
	http.SetCookie(w, m.cookie(sessionCookie, m.sign(userID), 12*time.Hour))
	http.SetCookie(w, m.cookie(stateCookie, "", -time.Hour)) // clear state
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, m.cookie(sessionCookie, "", -time.Hour))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Manager) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": m.UserID(r)})
}

func (p *provider) fetchUserID(ctx context.Context, tok *oauth2.Token) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.userInfoURL, nil)
	resp, err := p.cfg.Client(ctx, tok).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	if v, ok := info[p.idField].(string); ok && v != "" {
		return v, nil
	}
	// GitHub numeric id fallback keeps a stable key even if login is empty.
	if v, ok := info["id"]; ok {
		return fmt.Sprintf("%v", v), nil
	}
	return "", fmt.Errorf("no usable id field %q in userinfo", p.idField)
}

// --- cookie signing (HMAC-SHA256; value is "<payload>.<mac>") ---

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

func (m *Manager) verify(v string) (string, bool) {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(payload)
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return "", false
	}
	return string(payload), true
}

func (m *Manager) cookie(name, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	}
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
