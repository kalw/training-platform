package scoring

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAttemptFlow(t *testing.T) {
	salt := "s3cr3t"
	store := NewStore(nil)
	ch := ChallengeHash("What port does HTTP use?", "2019-http.md")
	_ = store.Upsert(Challenge{Hash: ch, Name: "http-port", Value: 10, Flags: []string{FlagHash("80", salt)}})

	srv := httptest.NewServer(Handler(store, func(*http.Request) string { return "u1" }))
	defer srv.Close()

	// Resolve by hash — must not leak flags.
	resp, err := http.Get(srv.URL + "/api/v1/challenges/hash/" + ch)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["success"] != true {
		t.Fatalf("hash lookup failed: %v", got)
	}
	if bytes.Contains(mustJSON(t, got), []byte("Flags")) {
		t.Error("hash lookup leaked flags to the client")
	}

	// Correct attempt (browser sends the salted hash, never plaintext).
	body, _ := json.Marshal(attemptRequest{ChallengeHash: ch, Submission: FlagHash("80", salt)})
	resp, err = http.Post(srv.URL+"/api/v1/challenges/attempt", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var att map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&att)
	resp.Body.Close()
	data, _ := att["data"].(map[string]any)
	if data["status"] != "correct" {
		t.Fatalf("expected correct, got %v", att)
	}
	if !store.Solved(ch, "u1") {
		t.Error("solve not recorded via HTTP path")
	}

	// Wrong attempt.
	body, _ = json.Marshal(attemptRequest{ChallengeHash: ch, Submission: FlagHash("8080", salt)})
	resp, _ = http.Post(srv.URL+"/api/v1/challenges/attempt", "application/json", bytes.NewReader(body))
	_ = json.NewDecoder(resp.Body).Decode(&att)
	resp.Body.Close()
	data, _ = att["data"].(map[string]any)
	if data["status"] != "incorrect" {
		t.Fatalf("expected incorrect, got %v", att)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
