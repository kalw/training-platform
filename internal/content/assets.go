package content

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// assetsFS holds the vendored front-end assets served under /assets/ — the
// xterm.js terminal emulator + fit addon that render the in-browser console.
// Bundled into the binary (no runtime CDN) so the platform stays
// self-contained and CSP-friendly.
//
//go:embed assets/xterm.js assets/xterm.css assets/xterm-addon-fit.js
var assetsFS embed.FS

// AssetsHandler serves the embedded /assets/* files. Mount it at "/assets/".
func AssetsHandler() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embedded path is a compile-time constant
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}

// WriteAssets copies the embedded assets into outDir/assets so a built site
// is self-contained even when hosted by something other than this binary.
func WriteAssets(outDir string) error {
	dst := filepath.Join(outDir, "assets")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(assetsFS, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := assetsFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, filepath.Base(path)), b, 0o644)
	})
}
