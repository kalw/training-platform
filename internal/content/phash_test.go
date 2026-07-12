package content

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default result page must always resolve to a phash flag when a browser
// is available (the real challenge-creation path). Skips on CI images with no
// Chrome/Chromium rather than failing.
func TestResolveDefaultResultWithChrome(t *testing.T) {
	chrome := FindChrome()
	if chrome == "" {
		t.Skip("no headless Chrome/Chromium found; skipping the real render path")
	}
	r := NewPhashResolver(t.TempDir(), chrome)
	flag, err := r.Resolve("", 12) // "" -> embedded default result page
	if err != nil {
		t.Fatalf("resolve default result: %v", err)
	}
	if !strings.HasPrefix(flag, "phash$") || !strings.HasSuffix(flag, ":12") {
		t.Errorf("unexpected flag format: %q", flag)
	}
	// Determinism: the same page renders to the same hash.
	flag2, err := r.Resolve("", 12)
	if err != nil {
		t.Fatal(err)
	}
	if flag != flag2 {
		t.Errorf("non-deterministic render: %q vs %q", flag, flag2)
	}
}

// An image reference is hashed directly with no browser needed.
func TestResolveImageReference(t *testing.T) {
	dir := t.TempDir()
	// A valid small PNG with a left-dark/right-light split so the dHash is
	// non-trivial; the resolver decodes it and hashes it directly (no browser).
	img := image.NewGray(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(20)
			if x > 16 {
				v = 220
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	f, err := os.Create(filepath.Join(dir, "ref.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	r := NewPhashResolver(dir, "") // no chrome needed for images
	flag, err := r.Resolve("ref.png", 10)
	if err != nil {
		t.Fatalf("resolve image ref: %v", err)
	}
	if !strings.HasPrefix(flag, "phash$") || !strings.HasSuffix(flag, ":10") {
		t.Errorf("unexpected flag: %q", flag)
	}
}

func TestResolveHTMLWithoutBrowserErrors(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "r.html"), []byte("<html><body>x</body></html>"), 0o644)
	r := NewPhashResolver(dir, "none") // force "no usable browser"
	// "none" isn't a real binary; the render will fail. Either way it must
	// return an error, not a bogus flag.
	if _, err := r.Resolve("r.html", 12); err == nil {
		t.Error("expected an error rendering HTML with no working browser")
	}
}
