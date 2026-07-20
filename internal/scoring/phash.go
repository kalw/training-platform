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
//
// What it can and cannot prove: at this resolution a dHash captures the page's
// coarse luminance layout — "the service came up and rendered the expected
// shape". It cannot see text. Measured on the example result page, rewriting
// every string on the page moved ~1% of bits even at a 32x32 grid, which is
// below the noise floor you must tolerate across renderers. So a perceptual
// match is evidence of layout, NOT of content; exercises that must assert what
// the page says declare a VerifySpec and are graded server-side instead.
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

// resizeGray box-averages the image down to wxh grayscale (no external image
// library needed for a hash this small).
//
// Averaging, not point-sampling: taking a single pixel per cell made the hash
// both jittery (a 1px shift flips bits) and blind to most of the page, since
// the few sample points rarely land on content. Averaging every pixel in the
// cell means everything drawn there contributes. (It still cannot resolve
// text — see DHash's note — but it is the correct downsample.)
func resizeGray(img image.Image, w, h int) [][]uint8 {
	b := img.Bounds()
	out := make([][]uint8, h)
	for y := 0; y < h; y++ {
		out[y] = make([]uint8, w)
		for x := 0; x < w; x++ {
			x0 := b.Min.X + x*max1(b.Dx())/w
			x1 := b.Min.X + (x+1)*max1(b.Dx())/w
			y0 := b.Min.Y + y*max1(b.Dy())/h
			y1 := b.Min.Y + (y+1)*max1(b.Dy())/h
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var sum, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					g := color.GrayModel.Convert(img.At(xx, yy)).(color.Gray)
					sum += uint64(g.Y)
					n++
				}
			}
			if n > 0 {
				out[y][x] = uint8(sum / n)
			}
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
