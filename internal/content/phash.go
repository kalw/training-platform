package content

import (
	_ "embed"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kalw/training-platform/internal/scoring"
)

// defaultResultHTML is the reference result page used when a lesson's exercise
// doesn't name its own via `exercise_result:`. It is rendered and hashed at
// build time, exactly like a lesson-specific page.
//
//go:embed default-result.html
var defaultResultHTML []byte

// PhashResolver computes an exercise's reference flag at challenge-creation
// time (the "build" step) from its expected result page — mirroring the
// legacy exportChallenges.sh chromium step. Given the front-matter
// `exercise_result` path (relative to the lessons dir; "" -> the embedded
// default) and a Hamming threshold, it returns "phash$<hex>:<threshold>".
type PhashResolver struct {
	lessonsDir string
	chrome     string // headless Chrome/Chromium binary; "" -> images only
}

// NewPhashResolver builds a resolver rooted at lessonsDir. chromePath may be
// empty to auto-detect; if no browser is found, HTML references error but
// image references (.png/.jpg) still work.
func NewPhashResolver(lessonsDir, chromePath string) *PhashResolver {
	if chromePath == "" {
		chromePath = FindChrome()
	}
	return &PhashResolver{lessonsDir: lessonsDir, chrome: chromePath}
}

// Resolve returns the phash flag for an exercise reference.
func (p *PhashResolver) Resolve(resultRef string, threshold int) (string, error) {
	if threshold <= 0 {
		threshold = scoring.DefaultPhashThreshold
	}

	var htmlPath string
	cleanup := func() {}
	switch {
	case resultRef == "":
		// Write the embedded default to a temp file to render it.
		f, err := os.CreateTemp("", "default-result-*.html")
		if err != nil {
			return "", err
		}
		_, _ = f.Write(defaultResultHTML)
		_ = f.Close()
		htmlPath = f.Name()
		cleanup = func() { _ = os.Remove(htmlPath) }
	default:
		ref := resultRef
		if !filepath.IsAbs(ref) {
			ref = filepath.Join(p.lessonsDir, ref)
		}
		ext := strings.ToLower(filepath.Ext(ref))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
			// Reference is already an image — hash it directly, no browser.
			img, err := decodeImageFile(ref)
			if err != nil {
				return "", fmt.Errorf("exercise_result image %q: %w", resultRef, err)
			}
			return scoring.PhashFlag(scoring.DHash(img), threshold), nil
		}
		htmlPath = ref
	}
	defer cleanup()

	if p.chrome == "" {
		return "", fmt.Errorf("exercise_result %q is HTML but no headless Chrome/Chromium was found (set CHROME_BIN, or reference a .png)", resultRef)
	}
	img, err := p.renderHTML(htmlPath)
	if err != nil {
		return "", err
	}
	return scoring.PhashFlag(scoring.DHash(img), threshold), nil
}

// renderHTML screenshots an HTML file at 1024x768 with headless Chrome and
// decodes the PNG — the same frame the client-side verify script captures at.
func (p *PhashResolver) renderHTML(htmlPath string) (image.Image, error) {
	abs, err := filepath.Abs(htmlPath)
	if err != nil {
		return nil, err
	}
	outDir, err := os.MkdirTemp("", "phash-shot-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)
	out := filepath.Join(outDir, "shot.png")

	// A fresh --user-data-dir isolates from any running Chrome;
	// --virtual-time-budget makes headless render then finish promptly.
	tmpProfile, _ := os.MkdirTemp("", "phash-profile-")
	defer os.RemoveAll(tmpProfile)
	cmd := exec.Command(p.chrome,
		"--headless=new", "--disable-gpu", "--no-sandbox",
		// --disable-dev-shm-usage: containers give /dev/shm only 64MB by
		// default, which makes Chrome hang/crash mid-render (the screenshot
		// never lands). Writing shared memory to /tmp instead fixes it.
		"--disable-dev-shm-usage", "--disable-software-rasterizer",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--hide-scrollbars", "--force-device-scale-factor=1",
		"--window-size=1024,768", "--virtual-time-budget=3000",
		"--user-data-dir="+tmpProfile,
		"--screenshot="+out, "file://"+abs,
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting chrome: %w", err)
	}
	// Chrome writes the screenshot within a second or two but doesn't always
	// exit cleanly, so poll for the output file and then kill the process
	// rather than blocking on Wait().
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
			// Give the write a beat to flush fully.
			time.Sleep(150 * time.Millisecond)
			return decodeImageFile(out)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("chrome screenshot timed out (no output at %s)", out)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// FindChrome locates a headless-capable Chrome/Chromium binary: CHROME_BIN
// first, then common PATH names, then the macOS app bundle.
func FindChrome() string {
	if b := os.Getenv("CHROME_BIN"); b != "" {
		return b
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
