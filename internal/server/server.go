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
	"github.com/kalw/training-platform/internal/content"
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
	// SessionNamespacePrefix / SessionTTL / SessionIdleTTL configure the k8s
	// session engine (TTL = hard cap; IdleTTL = keepalive sliding window).
	SessionNamespacePrefix string
	SessionTTL             time.Duration
	SessionIdleTTL         time.Duration
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
	// RouterHost is the public suffix exposed-port links are built on (e.g.
	// "direct.training.example.com"). Served to lesson pages via
	// GET /api/v1/config; empty leaves {:data-port=} links inert.
	RouterHost string
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

	termNS := cfg.TerminalNamespace
	if termNS == "" {
		termNS = "training-sessions"
	}

	// Session engine + browser terminals. If no cluster is reachable (no
	// in-cluster SA and no kubeconfig), the platform still serves lessons,
	// scoring and the scoreboard — only the terminal/session endpoints go
	// dark (503). This keeps `serve` usable for content authoring and CI
	// (e.g. Playwright) without a Kubernetes cluster.
	eng, err := session.New(session.Options{
		NamespacePrefix: cfg.SessionNamespacePrefix,
		TTL:             cfg.SessionTTL,
		IdleTTL:         cfg.SessionIdleTTL,
		DefaultImage:    cfg.DefaultInstanceImage,
		HostFQDN:        cfg.RouterHost,
	})
	if err != nil {
		log.Printf("sessions/terminals disabled: no Kubernetes cluster available: %v", err)
		eng = nil
	}

	var bridge *terminal.Bridge
	if eng != nil {
		bridge, err = terminal.New(eng.RESTConfig(), termNS, "instance", nil)
		if err != nil {
			return nil, nil, err
		}
	}
	mux.HandleFunc("/terminals/", func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			http.Error(w, "sessions unavailable (no cluster)", http.StatusServiceUnavailable)
			return
		}
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
		if eng == nil {
			http.Error(w, "sessions unavailable (no cluster)", http.StatusServiceUnavailable)
			return
		}
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
			// Surface the pod-lifecycle reason (ImagePullBackOff & co) so the
			// page can show why the session didn't come up.
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"pod": inst.Name, "ip": inst.IP, "image": inst.Image, "expires_at": inst.ExpiresAt})
	})
	// Per-instance lifecycle: status (GET), teardown (DELETE) and TTL
	// keepalive (POST …/keepalive) — what a lesson page needs to reattach
	// after a reload, stop a session, and stay alive while open.
	mux.HandleFunc("/api/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if eng == nil {
			http.Error(w, "sessions unavailable (no cluster)", http.StatusServiceUnavailable)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
		pod, action, _ := strings.Cut(rest, "/")
		if !termPodRe.MatchString(pod) {
			http.NotFound(w, r)
			return
		}
		switch {
		case action == "keepalive" && r.Method == http.MethodPost:
			exp, err := eng.Extend(r.Context(), termNS, pod)
			if err != nil {
				http.Error(w, "no such session", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"pod": pod, "expires_at": exp})
		case action == "" && r.Method == http.MethodGet:
			st, err := eng.Status(r.Context(), termNS, pod)
			if err != nil {
				http.Error(w, "no such session", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, st)
		case action == "" && r.Method == http.MethodDelete:
			_ = eng.DeletePod(r.Context(), termNS, pod)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Runtime page config: everything is configured at container start (see
	// the repo conventions), and lesson pages are pre-rendered static HTML —
	// so deployment-specific values reach them here, not at build time.
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"router_host": cfg.RouterHost})
	})

	// Vendored front-end assets (xterm.js & co) baked into the binary; lesson
	// builds also copy them next to the site so it can be hosted standalone.
	mux.Handle("/assets/", content.AssetsHandler())

	// Scoreboard page: lists all challenges and every recorded solve.
	mux.HandleFunc("/scoreboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(scoreboardPage)
	})

	// Optional Docker-API shim ("play with docker" content). Also degrades
	// gracefully without a cluster.
	if cfg.EnableShim {
		if shimH, err := dockershim.Handler(cfg.ShimNamespace); err != nil {
			log.Printf("docker shim disabled: no Kubernetes cluster available: %v", err)
		} else {
			mux.Handle("/docker/", http.StripPrefix("/docker", shimH))
		}
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
	if eng == nil {
		return // no cluster; nothing to GC
	}
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
