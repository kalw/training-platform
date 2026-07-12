// Package server composes every training surface into one HTTP handler so
// the whole platform runs as a single process: lessons, scoring, terminals,
// and (optionally) the Docker-API shim for "play with docker" content.
package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kalw/training-platform/internal/auth"
	"github.com/kalw/training-platform/internal/dockershim"
	"github.com/kalw/training-platform/internal/lessons"
	"github.com/kalw/training-platform/internal/scoring"
	"github.com/kalw/training-platform/internal/session"
	"github.com/kalw/training-platform/internal/terminal"
)

//go:embed scoreboard.html
var scoreboardPage []byte

// Config controls which surfaces are mounted and how.
type Config struct {
	// LessonsDir is the static lesson site root ("" serves a placeholder).
	LessonsDir string
	// SessionNamespacePrefix / SessionTTL configure the k8s session engine.
	SessionNamespacePrefix string
	SessionTTL             time.Duration
	DefaultInstanceImage   string
	// TerminalNamespace is where instance Pods live for the terminal bridge.
	// For the single-namespace default it matches the shim namespace.
	TerminalNamespace string
	// EnableShim mounts the Docker-Engine-API shim under /docker/ so
	// Docker-based course content keeps working on a k8s-only deployment.
	EnableShim    bool
	ShimNamespace string
	// Salt is the scoring salt (also used by the lessons build; must match).
	Salt string
	// ChallengesFile, if set, seeds the scoring store at boot (JSON produced
	// by the lessons build). Missing file is logged and ignored.
	ChallengesFile string
	// Auth configures social login (GitHub/Google). Zero value = anonymous.
	Auth auth.Options
}

// New builds the composed handler. It also returns the session Engine (for a
// background GC loop) — callers own its lifecycle.
func New(cfg Config) (http.Handler, *session.Engine, error) {
	mux := http.NewServeMux()

	// Social login (GitHub/Google). When unconfigured, mgr is nil and every
	// user is "anonymous" — the platform still runs.
	var userID func(*http.Request) string
	mgr, err := auth.New(cfg.Auth)
	if err != nil {
		return nil, nil, err
	}
	if mgr != nil {
		mux.Handle("/auth/", mgr.Handler())
		userID = mgr.UserID
	}

	// Scoring API. Exercise challenges are graded by perceptual hash
	// (dHash + Hamming) of the submitted screenshot proof. Seed the store
	// from the lessons build's file if one is configured (idempotent,
	// stateless-content model).
	store := scoring.NewStore(scoring.PhashGrader(scoring.DefaultPhashThreshold))
	if cfg.ChallengesFile != "" {
		if n, err := store.LoadFile(cfg.ChallengesFile); err != nil {
			log.Printf("scoring: could not load challenges file %q: %v", cfg.ChallengesFile, err)
		} else {
			log.Printf("scoring: loaded %d challenge(s)", n)
		}
	}
	// Mount scoring across the whole /api/v1/ subtree (it owns
	// /challenges, /challenges/hash/, /challenges/attempt, /scoreboard). The
	// sessions routes below are registered as more-specific patterns, which
	// win under Go 1.22+ ServeMux precedence.
	mux.Handle("/api/v1/", scoring.Handler(store, userID))

	// Session engine + browser terminals.
	eng, err := session.New(session.Options{
		NamespacePrefix: cfg.SessionNamespacePrefix,
		TTL:             cfg.SessionTTL,
		DefaultImage:    cfg.DefaultInstanceImage,
	})
	if err != nil {
		return nil, nil, err
	}
	termNS := cfg.TerminalNamespace
	if termNS == "" {
		termNS = "training-sessions"
	}
	bridge, err := terminal.New(eng.RESTConfig(), termNS, "instance", nil)
	if err != nil {
		return nil, nil, err
	}
	mux.HandleFunc("/terminals/", func(w http.ResponseWriter, r *http.Request) {
		m := termPathRe.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		if err := bridge.Attach(w, r, m[1]); err != nil {
			log.Printf("terminal attach %s: %v", m[1], err)
		}
	})

	// Sessions API: boot / tear down an ephemeral instance Pod in the
	// terminal namespace, so a rendered lesson page (the PWD-SDK equivalent)
	// can open a live terminal. POST returns {"pod": "...","ip": "..."};
	// the page then connects to /terminals/{pod}.
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Image string `json:"image"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		inst, err := eng.NewEphemeralInstance(ctx, termNS, body.Image)
		if err != nil {
			log.Printf("sessions: create: %v", err)
			http.Error(w, "could not start session", http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"pod": inst.Name, "ip": inst.IP, "image": inst.Image})
	})
	mux.HandleFunc("/api/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		pod := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
		if !termPodRe.MatchString(pod) {
			http.NotFound(w, r)
			return
		}
		_ = eng.DeletePod(r.Context(), termNS, pod)
		w.WriteHeader(http.StatusNoContent)
	})

	// Scoreboard page: lists all challenges and every recorded solve.
	mux.HandleFunc("/scoreboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(scoreboardPage)
	})

	// Optional Docker-API shim ("play with docker" content).
	if cfg.EnableShim {
		shimH, err := dockershim.Handler(cfg.ShimNamespace)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/docker/", http.StripPrefix("/docker", shimH))
	}

	// Health + lessons (lessons last: it's the catch-all root).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("/", lessons.Handler(cfg.LessonsDir))

	return mux, eng, nil
}

var termPathRe = regexp.MustCompile(`^/terminals/([a-z0-9][a-z0-9-]*)$`)
var termPodRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// RunGC garbage-collects expired session namespaces and expired ephemeral
// instance Pods in termNS until ctx is done.
func RunGC(ctx context.Context, eng *session.Engine, termNS string, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := eng.GCExpired(ctx); err != nil {
				log.Printf("session GC: %v", err)
			} else if n > 0 {
				log.Printf("session GC: reaped %d expired session(s)", n)
			}
			if n, err := eng.GCExpiredPods(ctx, termNS); err != nil {
				log.Printf("pod GC: %v", err)
			} else if n > 0 {
				log.Printf("pod GC: reaped %d expired instance pod(s)", n)
			}
		}
	}
}
