package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoProvidersIsAnonymousMode(t *testing.T) {
	m, err := New(Options{Secret: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatal("expected nil Manager when no provider configured")
	}
}

// Regression: anonymous mode (no providers AND no secret) must succeed —
// the secret is only required once a provider is enabled. An earlier version
// checked the secret first and crash-looped every anonymous deployment.
func TestNoProvidersNoSecretIsAnonymous(t *testing.T) {
	m, err := New(Options{})
	if err != nil {
		t.Fatalf("anonymous mode should not error, got %v", err)
	}
	if m != nil {
		t.Fatal("expected nil Manager with no providers and no secret")
	}
}

func TestProviderWithoutSecretErrors(t *testing.T) {
	_, err := New(Options{GitHubClientID: "id", GitHubClientSecret: "sec"})
	if err == nil {
		t.Fatal("expected an error when a provider is set but Secret is empty")
	}
}

func TestCookieSignRoundTripAndTamperReject(t *testing.T) {
	m := &Manager{secret: []byte("topsecret")}
	signed := m.sign("github:alice")
	if got, ok := m.verify(signed); !ok || got != "github:alice" {
		t.Fatalf("round trip failed: %q %v", got, ok)
	}
	// Tampered payload must be rejected.
	if _, ok := m.verify(signed + "x"); ok {
		t.Error("accepted a tampered cookie")
	}
	// Wrong key must be rejected.
	other := &Manager{secret: []byte("different")}
	if _, ok := other.verify(signed); ok {
		t.Error("accepted a cookie signed with a different key")
	}
}

func TestUserIDFromRequest(t *testing.T) {
	m := &Manager{secret: []byte("k")}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := m.UserID(r); got != "anonymous" {
		t.Errorf("no cookie should be anonymous, got %q", got)
	}
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: m.sign("google:bob@example.com")})
	if got := m.UserID(r); got != "google:bob@example.com" {
		t.Errorf("UserID = %q", got)
	}
}

func TestProvidersEnabledByCredentials(t *testing.T) {
	m, err := New(Options{
		Secret:             "s",
		BaseURL:            "https://t.example.com/",
		GitHubClientID:     "id",
		GitHubClientSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected a Manager with github enabled")
	}
	if _, ok := m.providers["github"]; !ok {
		t.Error("github not enabled despite credentials")
	}
	if _, ok := m.providers["google"]; ok {
		t.Error("google enabled without credentials")
	}
	// Redirect URL derives from BaseURL with the trailing slash trimmed.
	if got := m.providers["github"].cfg.RedirectURL; got != "https://t.example.com/auth/callback/github" {
		t.Errorf("redirect URL = %q", got)
	}
}
