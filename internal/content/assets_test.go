package content

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// VerifyHandler must serve the exercise proof client at /js/exercise-verify.js
// (the result page loads it to screenshot itself and submit).
func TestVerifyHandlerServesScript(t *testing.T) {
	srv := httptest.NewServer(VerifyHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/js/exercise-verify.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /js/exercise-verify.js = %d, want 200", resp.StatusCode)
	}
}

// WriteAssets must emit both the vendored assets and the verify client so a
// built site is self-contained when hosted without the binary.
func TestWriteAssetsIncludesVerifyClientAndHtml2canvas(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAssets(dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"js/exercise-verify.js",
		"assets/html2canvas.min.js",
		"assets/xterm.js",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("built site missing %s: %v", p, err)
		}
	}
}
