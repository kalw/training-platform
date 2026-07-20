package scoring

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubFetcher stands in for the session engine: it returns whatever the
// learner's page is supposed to be serving, and records what it was asked for.
type stubFetcher struct {
	body      string
	status    int
	err       error
	gotPod    string
	gotPort   int
	gotPath   string
	callCount int
}

func (s *stubFetcher) FetchPod(_ context.Context, pod string, port int, path string) (int, []byte, error) {
	s.callCount++
	s.gotPod, s.gotPort, s.gotPath = pod, port, path
	if s.err != nil {
		return 0, nil, s.err
	}
	st := s.status
	if st == 0 {
		st = 200
	}
	return st, []byte(s.body), nil
}

func verifyStatus(t *testing.T, srv *httptest.Server, hash, pod string) string {
	t.Helper()
	body, _ := json.Marshal(verifyRequest{ChallengeHash: hash, Pod: pod})
	resp, err := http.Post(srv.URL+"/api/v1/challenges/verify", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	data, _ := out["data"].(map[string]any)
	s, _ := data["status"].(string)
	return s
}

// The point of server-side verification: a page whose CONTENT changed must
// fail, which a perceptual hash cannot detect.
func TestVerifyGradesOnContent(t *testing.T) {
	store := NewStore(nil)
	ch := ChallengeHash("fix the server", "03-fix")
	_ = store.Upsert(Challenge{
		Hash: ch, Name: "exercise-fix", Value: 20,
		Verify: &VerifySpec{Port: 8888, Path: "/03-fix-nginx-result.html", Expect: "The service is running correctly"},
	})

	f := &stubFetcher{body: `<h1>Success</h1><p>The service is running correctly.</p>`}
	srv := httptest.NewServer(Handler(store, func(*http.Request) string { return "u1" }, WithPodFetcher(f)))
	defer srv.Close()

	if got := verifyStatus(t, srv, ch, "i-abc123"); got != "correct" {
		t.Fatalf("correct page graded %q, want correct", got)
	}
	if !store.Solved(ch, "u1") {
		t.Error("solve not recorded")
	}
	// The target came from the challenge, not the request.
	if f.gotPort != 8888 || f.gotPath != "/03-fix-nginx-result.html" || f.gotPod != "i-abc123" {
		t.Errorf("fetched %s:%d%s, want the challenge's port/path on the requested pod", f.gotPod, f.gotPort, f.gotPath)
	}

	// The reported bug: the learner's page says something else. phash gave
	// this distance 0; the content check must reject it.
	f.body = `<h1>Broken</h1><p>Everything is on fire and nothing works.</p>`
	if got := verifyStatus(t, srv, ch, "i-abc123"); got != "incorrect" {
		t.Errorf("changed-text page graded %q, want incorrect", got)
	}

	// A non-200 from the learner's service is not a pass either.
	f.body, f.status = "The service is running correctly", 503
	if got := verifyStatus(t, srv, ch, "i-abc123"); got != "incorrect" {
		t.Errorf("503 page graded %q, want incorrect", got)
	}

	// Unreachable session reports distinctly (so the UI can say "not up yet").
	f.status, f.err = 0, fmt.Errorf("dial tcp: connection refused")
	if got := verifyStatus(t, srv, ch, "i-abc123"); got != "unreachable" {
		t.Errorf("unreachable session graded %q, want unreachable", got)
	}
}

func TestVerifyRegexAndGuards(t *testing.T) {
	store := NewStore(nil)
	reHash := ChallengeHash("regex", "l")
	_ = store.Upsert(Challenge{Hash: reHash, Name: "re", Value: 5,
		Verify: &VerifySpec{Port: 80, Path: "/", ExpectRegex: `Success|healthy`}})
	// A challenge with no assertion must never be gradable this way.
	plainHash := ChallengeHash("plain", "l")
	_ = store.Upsert(Challenge{Hash: plainHash, Name: "plain", Value: 5,
		Flags: []string{"phash$0000000000000001:12"}})

	f := &stubFetcher{body: "all healthy here"}
	srv := httptest.NewServer(Handler(store, nil, WithPodFetcher(f)))
	defer srv.Close()

	if got := verifyStatus(t, srv, reHash, "i-1"); got != "correct" {
		t.Errorf("regex match graded %q, want correct", got)
	}

	// No VerifySpec -> 400, and crucially no fetch is attempted.
	before := f.callCount
	body, _ := json.Marshal(verifyRequest{ChallengeHash: plainHash, Pod: "i-1"})
	resp, err := http.Post(srv.URL+"/api/v1/challenges/verify", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("verify on a phash-only challenge = %d, want 400", resp.StatusCode)
	}
	if f.callCount != before {
		t.Error("fetched a pod for a challenge with no content assertion")
	}
}

// Without a fetcher (no cluster) the endpoint degrades instead of panicking.
func TestVerifyWithoutFetcher(t *testing.T) {
	store := NewStore(nil)
	h := ChallengeHash("x", "l")
	_ = store.Upsert(Challenge{Hash: h, Verify: &VerifySpec{Port: 80, Path: "/", Expect: "hi"}})
	srv := httptest.NewServer(Handler(store, nil))
	defer srv.Close()

	body, _ := json.Marshal(verifyRequest{ChallengeHash: h, Pod: "i-1"})
	resp, err := http.Post(srv.URL+"/api/v1/challenges/verify", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when no cluster is wired", resp.StatusCode)
	}
}
