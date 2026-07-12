// Package server composes every training surface into one HTTP handler so
// the whole platform runs as a single process: lessons, scoring, terminals,
// and (optionally) the Docker-API shim for "play with docker" content.
package server

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/kalw/training-platform/internal/dockershim"
	"github.com/kalw/training-platform/internal/lessons"
	"github.com/kalw/training-platform/internal/scoring"
	"github.com/kalw/training-platform/internal/session"
	"github.com/kalw/training-platform/internal/terminal"
)

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
}

// New builds the composed handler. It also returns the session Engine (for a
// background GC loop) — callers own its lifecycle.
func New(cfg Config) (http.Handler, *session.Engine, error) {
	mux := http.NewServeMux()

	// Scoring API (challenge store seeded elsewhere / via Store()).
	store := scoring.NewStore(nil)
	mux.Handle("/api/v1/challenges/", scoring.Handler(store, nil))

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

// RunGC runs the session-namespace garbage collector until ctx is done.
func RunGC(ctx context.Context, eng *session.Engine, every time.Duration) {
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
		}
	}
}
