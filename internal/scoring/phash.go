package scoring

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"math/bits"
	"strconv"
	"strings"

	// register JPEG/PNG decoders for image.Decode (exercise captures are
	// submitted as JPEG or PNG data URLs).
	_ "image/jpeg"
	_ "image/png"
)

// DefaultPhashThreshold is the Hamming distance under which two 64-bit dHashes
// are considered the same page. 12 matches the legacy platform default.
const DefaultPhashThreshold = 12

// DHash computes a 64-bit difference hash: convert to grayscale, downscale to
// 9x8, and set one bit per adjacent-column comparison (8 rows x 8 comparisons
// = 64 bits). dHash is robust to the small rendering differences between
// browsers/platforms that make exact screenshot matching impossible.
func DHash(img image.Image) uint64 {
	const w, h = 9, 8
	small := resizeGray(img, w, h)
	var hash uint64
	bit := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w-1; x++ {
			if small[y][x] < small[y][x+1] {
				hash |= 1 << uint(bit)
			}
			bit++
		}
	}
	return hash
}

// resizeGray nearest-neighbour downsamples to wxh grayscale (no external
// image library needed for a hash this small).
func resizeGray(img image.Image, w, h int) [][]uint8 {
	b := img.Bounds()
	out := make([][]uint8, h)
	for y := 0; y < h; y++ {
		out[y] = make([]uint8, w)
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*max1(b.Dx())/w
			sy := b.Min.Y + y*max1(b.Dy())/h
			g := color.GrayModel.Convert(img.At(sx, sy)).(color.Gray)
			out[y][x] = g.Y
		}
	}
	return out
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// Hamming is the number of differing bits between two hashes.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// PhashFlag formats a reference dHash into the flag the store grades against.
func PhashFlag(hash uint64, threshold int) string {
	return fmt.Sprintf("phash$%016x:%d", hash, threshold)
}

// PhashGrader returns a store grader: given a "phash$<hex>[:threshold]" flag
// and a submitted capture (a data: URL, or raw base64 image bytes), it decodes
// the capture, dHashes it, and accepts when the Hamming distance to the
// reference is within threshold. defaultThreshold applies when the flag omits
// one. Any decode/parse failure grades as incorrect (never panics on hostile
// input).
func PhashGrader(defaultThreshold int) func(flag, submitted string) bool {
	return func(flag, submitted string) bool {
		ref, thr, ok := parsePhashFlag(flag, defaultThreshold)
		if !ok {
			return false
		}
		img, err := decodeCapture(submitted)
		if err != nil {
			return false
		}
		return Hamming(ref, DHash(img)) <= thr
	}
}

func parsePhashFlag(flag string, def int) (uint64, int, bool) {
	s, ok := strings.CutPrefix(flag, "phash$")
	if !ok {
		return 0, 0, false
	}
	thr := def
	if hex, t, found := strings.Cut(s, ":"); found {
		s = hex
		if n, err := strconv.Atoi(t); err == nil {
			thr = n
		}
	}
	h, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, 0, false
	}
	return h, thr, true
}

func decodeCapture(s string) (image.Image, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "data:") {
		if _, b64, ok := strings.Cut(s, ","); ok {
			s = b64
		}
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}
