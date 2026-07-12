// Package lessons serves the static lesson site (pre-rendered HTML/JS/CSS).
//
// Lesson authoring/rendering stays a build-time concern of the content repo
// (the legacy platform used Jekyll; anything that emits static files works).
// The all-in-one binary only serves the built output and, at boot, ingests
// the challenges file that build produced so the scoring engine and the
// pages share one source of truth for the hash contract.
package lessons

import (
	"net/http"
	"os"
	"path/filepath"
)

// Handler serves the static site rooted at dir. Requests that don't resolve
// to a file fall back to index.html (so client-side routing works); if dir
// is empty or missing, it serves a small placeholder so the process still
// starts (useful when lessons are hosted separately).
func Handler(dir string) http.Handler {
	if dir == "" {
		return placeholder("no lesson directory configured (set lessons.dir)")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return placeholder("lesson directory not found: " + dir)
	}
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(index); err == nil {
			http.ServeFile(w, r, index)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func placeholder(msg string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("training-platform: " + msg + "\n"))
	})
}
