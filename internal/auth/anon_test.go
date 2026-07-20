package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// serve runs one request through the middleware and reports the identity it
// resolved plus any cookie it set.
func serve(t *testing.T, req *http.Request) (id string, setCookie *http.Cookie) {
	t.Helper()
	h := AnonymousMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id = LearnerID(r)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == anonCookie {
			setCookie = c
		}
	}
	return id, setCookie
}

func TestAnonymousNameAssignedAndStable(t *testing.T) {
	// First visit: gets a name and a cookie to keep it.
	id1, c := serve(t, httptest.NewRequest("GET", "/", nil))
	if c == nil {
		t.Fatal("no learner cookie set on first visit")
	}
	if !nameRe.MatchString(id1) {
		t.Fatalf("assigned name %q doesn't match the expected shape", id1)
	}
	if id1 == "anonymous" {
		t.Error("learner still collapsed into the shared anonymous identity")
	}

	// Return visit with that cookie: same identity, and no pointless re-set.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(&http.Cookie{Name: anonCookie, Value: c.Value})
	id2, c2 := serve(t, r2)
	if id2 != id1 {
		t.Errorf("identity changed across requests: %q -> %q", id1, id2)
	}
	if c2 != nil {
		t.Error("re-issued a cookie for a learner that already had a valid one")
	}
}

func TestAnonymousNamesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, _ := serve(t, httptest.NewRequest("GET", "/", nil))
		seen[id] = true
	}
	// Two fresh browsers must not share a row on the scoreboard. Allow a few
	// collisions (the space is finite) but demand overwhelming distinctness.
	if len(seen) < 190 {
		t.Errorf("only %d distinct names from 200 fresh visitors", len(seen))
	}
}

// A learner controls their own cookie, so it must never carry arbitrary text
// into the shared scoreboard.
func TestTamperedCookieIsReplaced(t *testing.T) {
	for _, bad := range []string{
		`<script>alert(1)</script>`,
		"admin",
		"brisk-otter",           // right words, wrong shape
		"brisk-otter-zzz",       // non-hex suffix
		"BRISK-OTTER-4f2",       // wrong case
		"brisk-otter-4f2-extra", // trailing junk
		"",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: anonCookie, Value: bad})
		id, c := serve(t, r)
		if id == bad {
			t.Errorf("accepted tampered identity %q", bad)
		}
		if !nameRe.MatchString(id) {
			t.Errorf("replacement for %q is not a valid name: %q", bad, id)
		}
		if c == nil {
			t.Errorf("no fresh cookie issued to replace %q", bad)
		}
	}
}

// A signed social-login session outranks the anonymous name; without one the
// anonymous name is used.
func TestUserIDFuncPrefersLoggedInUser(t *testing.T) {
	m, err := New(Options{Secret: "s3cr3t", GitHubClientID: "id", GitHubClientSecret: "sec", BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	resolve := UserIDFunc(m)

	// Anonymous: falls back to the learner name from the middleware.
	var anonID string
	AnonymousMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anonID = resolve(r)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !nameRe.MatchString(anonID) {
		t.Errorf("anonymous request resolved to %q, want a learner name", anonID)
	}

	// Logged in: the signed session wins.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: m.sign("github:42")})
	var loggedIn string
	AnonymousMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loggedIn = resolve(r)
	})).ServeHTTP(httptest.NewRecorder(), r)
	if loggedIn != "github:42" {
		t.Errorf("logged-in request resolved to %q, want github:42", loggedIn)
	}
}

// Without the middleware (e.g. a scripted API call) nothing panics.
func TestLearnerIDWithoutMiddleware(t *testing.T) {
	if got := LearnerID(httptest.NewRequest("GET", "/", nil)); got != "anonymous" {
		t.Errorf("LearnerID = %q, want anonymous", got)
	}
}
