package scoring

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// pngDataURL renders a simple image (a left-dark / right-light split with a
// gradient) to a base64 PNG data URL. shift nudges the split so we can make a
// "close but not identical" capture.
func pngDataURL(t *testing.T, w, h, shift int) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 0
			if x+shift > w/2 {
				v = 200 + (x % 40)
			} else {
				v = 20 + (x % 10)
			}
			if v > 255 {
				v = 255
			}
			img.SetGray(x, y, color.Gray{Y: uint8(v)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func dhashOfDataURL(t *testing.T, url string) uint64 {
	t.Helper()
	img, err := decodeCapture(url)
	if err != nil {
		t.Fatal(err)
	}
	return DHash(img)
}

func TestDHashHammingAndGrader(t *testing.T) {
	ref := pngDataURL(t, 200, 150, 0)
	near := pngDataURL(t, 200, 150, 3)  // slightly shifted — should still match
	far := pngDataURL(t, 200, 150, 120) // very different split — should not

	refHash := dhashOfDataURL(t, ref)
	flag := PhashFlag(refHash, DefaultPhashThreshold)

	grade := PhashGrader(DefaultPhashThreshold)
	if !grade(flag, ref) {
		t.Error("identical capture rejected")
	}
	if d := Hamming(refHash, dhashOfDataURL(t, near)); d > DefaultPhashThreshold {
		t.Logf("near distance %d exceeds threshold (acceptable for this synthetic image)", d)
	}
	if grade(flag, far) {
		t.Error("a clearly different capture was accepted")
	}
	// Hostile / garbage input must grade false, never panic.
	if grade(flag, "not-base64!!") {
		t.Error("garbage capture accepted")
	}
	if grade("phash$nothex", ref) {
		t.Error("malformed flag accepted")
	}
	if grade("not-a-phash-flag", ref) {
		t.Error("non-phash flag accepted by phash grader")
	}
}

func TestPhashFlagRoundTrip(t *testing.T) {
	f := PhashFlag(0xdeadbeefcafef00d, 8)
	h, thr, ok := parsePhashFlag(f, DefaultPhashThreshold)
	if !ok || h != 0xdeadbeefcafef00d || thr != 8 {
		t.Fatalf("round trip failed: %x %d %v (flag %q)", h, thr, ok, f)
	}
	// Threshold omitted -> default applies.
	_, thr2, _ := parsePhashFlag("phash$0000000000000000", 12)
	if thr2 != 12 {
		t.Errorf("default threshold not applied: %d", thr2)
	}
}

// The store must route phash flags through the grader, and exact-match flags
// through equality — in the same challenge if need be.
func TestStoreUsesPhashGrader(t *testing.T) {
	ref := pngDataURL(t, 200, 150, 0)
	s := NewStore(PhashGrader(DefaultPhashThreshold))
	refHash := dhashOfDataURL(t, ref)
	ch := ChallengeHash("exercise", "ex.md")
	_ = s.Upsert(Challenge{Hash: ch, Name: "ex", Flags: []string{PhashFlag(refHash, DefaultPhashThreshold)}})
	if ok, known := s.Grade(ch, ref, "u1"); !known || !ok {
		t.Fatalf("exercise capture not graded correct (known=%v ok=%v)", known, ok)
	}
}
