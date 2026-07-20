package scoring

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

// Handler exposes the scoring API. It mirrors the two endpoints the lessons
// JS relies on (historically served by the patched CTFd fork), so quiz.js /
// exercise-verify.js can talk to it unchanged in shape:
//
//	GET  /api/v1/challenges/hash/{hash} -> resolve a challenge by its hash
//	POST /api/v1/challenges/attempt     -> submit a (pre-hashed) answer
//
// userIDFunc extracts the current user id from a request (e.g. from an OIDC
// session cookie); pass a func returning a constant for anonymous/dev use.
// PodFetcher GETs path on port of a learner's session Pod and returns the
// response status and body. Implementations MUST confirm the pod is one the
// platform manages before dialing — the pod name is the one client-supplied
// input on the verify path.
type PodFetcher interface {
	FetchPod(ctx context.Context, pod string, port int, path string) (status int, body []byte, err error)
}

// Option configures Handler.
type Option func(*handlerCfg)

type handlerCfg struct{ fetcher PodFetcher }

// WithPodFetcher enables POST /api/v1/challenges/verify: server-side content
// grading of exercises that declare a VerifySpec.
func WithPodFetcher(f PodFetcher) Option {
	return func(c *handlerCfg) { c.fetcher = f }
}

func Handler(store *Store, userIDFunc func(*http.Request) string, opts ...Option) http.Handler {
	if userIDFunc == nil {
		userIDFunc = func(*http.Request) string { return "anonymous" }
	}
	var cfg handlerCfg
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	// List all challenges (never exposes flags) — powers the scoreboard page.
	mux.HandleFunc("/api/v1/challenges", func(w http.ResponseWriter, r *http.Request) {
		type pub struct {
			Hash  string `json:"hash"`
			Name  string `json:"name"`
			Value int    `json:"value"`
		}
		list := store.List()
		out := make([]pub, len(list))
		for i, c := range list {
			out[i] = pub{Hash: c.Hash, Name: c.Name, Value: c.Value}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
	})
	// Scoreboard: every recorded solve (the per-learner view).
	mux.HandleFunc("/api/v1/scoreboard", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": store.Results()})
	})
	// Standings: the global view — ranking by points, and how many learners
	// have solved each challenge.
	mux.HandleFunc("/api/v1/standings", func(w http.ResponseWriter, r *http.Request) {
		ranking, challenges, total := store.Standings()
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data": map[string]any{
				"ranking":      ranking,
				"challenges":   challenges,
				"total_points": total,
			},
		})
	})
	mux.HandleFunc("/api/v1/challenges/hash/", func(w http.ResponseWriter, r *http.Request) {
		m := hashPathRe.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		c, ok := store.Get(m[1])
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false})
			return
		}
		// Never disclose flags to the client.
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    map[string]any{"hash": c.Hash, "name": c.Name, "value": c.Value},
		})
	})
	mux.HandleFunc("/api/v1/challenges/attempt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req attemptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false})
			return
		}
		correct, known := store.Grade(req.ChallengeHash, req.Submission, userIDFunc(r))
		if !known {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false})
			return
		}
		status := "incorrect"
		if correct {
			status = "correct"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    map[string]any{"status": status},
		})
	})

	// Server-side content verification. The learner asks "check my session";
	// the platform fetches their result page itself and asserts what it
	// contains. Unlike the screenshot proof this can't be produced by the
	// browser — the only way to pass is to actually serve the right page.
	mux.HandleFunc("/api/v1/challenges/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cfg.fetcher == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"success": false, "error": "verification unavailable (no cluster)"})
			return
		}
		var req verifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false})
			return
		}
		c, ok := store.Get(req.ChallengeHash)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false})
			return
		}
		if !c.Verify.Assertive() {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false, "error": "challenge has no content assertion"})
			return
		}
		// Port and path come from the challenge, never the request — the
		// client only chooses which of its own pods to check.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		status, body, err := cfg.fetcher.FetchPod(ctx, req.Pod, c.Verify.Port, c.Verify.Path)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data":    map[string]any{"status": "unreachable", "detail": err.Error()},
			})
			return
		}
		if status != http.StatusOK || !c.Verify.Matches(string(body)) {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data":    map[string]any{"status": "incorrect", "http_status": status},
			})
			return
		}
		store.RecordSolve(c.Hash, userIDFunc(r))
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "data": map[string]any{"status": "correct"},
		})
	})

	return withCORS(mux)
}

// withCORS lets the exercise result page — served from the learner's session
// Pod (a different origin than the platform, reached via the exposed-port
// router) — POST its screenshot proof to the scoring API. It reflects the
// request Origin (with credentials, so an authenticated solve still
// attributes) and answers the preflight. Scoring exposes no secrets and
// grades by hash, so reflecting any origin is safe here.
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

var hashPathRe = regexp.MustCompile(`^/api/v1/challenges/hash/([0-9a-fA-F]+)$`)

type verifyRequest struct {
	ChallengeHash string `json:"challenge_hash"`
	// Pod is the learner's session instance to check. The fetcher validates
	// it is a managed instance Pod before dialing.
	Pod string `json:"pod"`
}

type attemptRequest struct {
	ChallengeHash string `json:"challenge_hash"`
	// Submission is the salted answer hash for quizzes, or a capture for
	// exercises — never a plaintext answer.
	Submission string `json:"submission"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
