package content

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// assetsFS holds the vendored front-end assets served under /assets/ — the
// xterm.js terminal emulator + fit addon that render the in-browser console,
// and html2canvas which exercise-verify.js uses to capture the learner's
// result page as screenshot proof. Bundled into the binary (no runtime CDN)
// so the platform stays self-contained and CSP-friendly.
//
//go:embed assets/xterm.js assets/xterm.css assets/xterm-addon-fit.js assets/html2canvas.min.js
var assetsFS embed.FS

// verifyFS holds exercise-verify.js — the platform's own client (not
// vendored): the exercise result page loads it to screenshot itself and
// submit the proof to the scoring API. Served under /js/.
//
//go:embed exercise-verify.js
var verifyFS embed.FS

// VerifyHandler serves /js/exercise-verify.js (and any future /js/* helpers).
func VerifyHandler() http.Handler {
	return http.StripPrefix("/js/", http.FileServer(http.FS(verifyFS)))
}

// AssetsHandler serves the embedded /assets/* files. Mount it at "/assets/".
func AssetsHandler() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embedded path is a compile-time constant
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}

// WriteAssets copies the embedded assets into outDir/assets (and the verify
// client into outDir/js) so a built site is self-contained even when hosted
// by something other than this binary.
func WriteAssets(outDir string) error {
	dst := filepath.Join(outDir, "assets")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := fs.WalkDir(assetsFS, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := assetsFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, filepath.Base(path)), b, 0o644)
	}); err != nil {
		return err
	}
	jsDir := filepath.Join(outDir, "js")
	if err := os.MkdirAll(jsDir, 0o755); err != nil {
		return err
	}
	b, err := verifyFS.ReadFile("exercise-verify.js")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(jsDir, "exercise-verify.js"), b, 0o644)
}
