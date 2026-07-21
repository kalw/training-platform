package content

import (
	"strings"
	"testing"
)

// The features under test here are the legacy writing-tutorials.md authoring
// contract: terms front matter, .termN click-to-run blocks, and kramdown
// inline attribute lists on links.

func TestTermCodeBlocks(t *testing.T) {
	out := renderMarkdown("```.term1\ndocker ps\n```")
	if !strings.Contains(out, `class="term-code"`) || !strings.Contains(out, `data-term="1"`) {
		t.Errorf(".term1 fence not rendered as click-to-run block: %s", out)
	}
	if !strings.Contains(out, "docker ps") {
		t.Errorf("code content lost: %s", out)
	}
	// A plain fence (or a language tag) stays a normal code block.
	for _, fence := range []string{"```\nls\n```", "```sh\nls\n```"} {
		if out := renderMarkdown(fence); strings.Contains(out, "term-code") {
			t.Errorf("non-.termN fence %q rendered as click-to-run: %s", fence, out)
		}
	}
}

func TestLinkInlineAttributeLists(t *testing.T) {
	out := renderMarkdown(`[webserver](/){:data-term=".term2"}{:data-port="8080"}{:data-host-prefix="pfx"}`)
	for _, want := range []string{`href="/"`, `data-term=".term2"`, `data-port="8080"`, `data-host-prefix="pfx"`, `>webserver</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in: %s", want, out)
		}
	}
	// Unsafe attribute names are dropped; the link itself survives.
	out = renderMarkdown(`[x](/){:onclick="alert(1)"}{:id="ok"}`)
	if strings.Contains(out, "onclick") {
		t.Errorf("unsafe attribute leaked: %s", out)
	}
	if !strings.Contains(out, `id="ok"`) {
		t.Errorf("safe id attribute dropped: %s", out)
	}
	// Plain links are unchanged.
	if out := renderMarkdown("[a](b)"); !strings.Contains(out, `<a href="b">a</a>`) {
		t.Errorf("plain link broken: %s", out)
	}
}

func TestTermsFrontMatterControlsConsole(t *testing.T) {
	render := func(fm string) string {
		l, err := Render("x", []byte("---\n"+fm+"\n---\nbody\n"), "s", nil)
		if err != nil {
			t.Fatal(err)
		}
		return l.HTML
	}
	// Default: one terminal.
	if html := render("title: t"); !strings.Contains(html, "const TERMS = 1;") || !strings.Contains(html, `id="terms"`) {
		t.Error("default should render a 1-terminal console")
	}
	// terms: 0 removes the console column entirely.
	if html := render("terms: 0"); strings.Contains(html, `id="terms"`) || !strings.Contains(html, "const TERMS = 0;") {
		t.Error("terms: 0 should render no console")
	}
	// terms: 3, and values are clamped to 0..6.
	if html := render("terms: 3"); !strings.Contains(html, "const TERMS = 3;") {
		t.Error("terms: 3 not honoured")
	}
	// Out-of-range no longer clamps silently — it fails the build. See
	// TestTerminalLimitsAreLoud.
}

// term_images gives each terminal its own image, positionally, falling back
// to the lesson's image: — so a multi-node lesson only names what differs.
func TestPerTerminalImages(t *testing.T) {
	render := func(fm string) string {
		l, err := Render("x", []byte("---\n"+fm+"\n---\nbody\n"), "s", nil)
		if err != nil {
			t.Fatal(err)
		}
		return l.HTML
	}

	// Explicit per-node images.
	html := render("image: busybox:1.36\nterms: 2\nterm_images:\n  - nginx:alpine\n  - curlimages/curl:8.9.1")
	if !strings.Contains(html, `const TERM_IMAGES = ["nginx:alpine","curlimages/curl:8.9.1"]`) {
		t.Errorf("per-terminal images not rendered:\n%s", firstLineWith(html, "TERM_IMAGES"))
	}

	// Shorter list, and a blank entry, both fall back to image:.
	html = render("image: busybox:1.36\nterms: 3\nterm_images:\n  - nginx:alpine\n  - \"\"")
	if !strings.Contains(html, `const TERM_IMAGES = ["nginx:alpine","busybox:1.36","busybox:1.36"]`) {
		t.Errorf("fallback to image: wrong:\n%s", firstLineWith(html, "TERM_IMAGES"))
	}

	// No term_images at all: every terminal gets the lesson image (the old
	// behaviour, unchanged).
	html = render("image: busybox:1.36\nterms: 2")
	if !strings.Contains(html, `const TERM_IMAGES = ["busybox:1.36","busybox:1.36"]`) {
		t.Errorf("default should repeat image::\n%s", firstLineWith(html, "TERM_IMAGES"))
	}

	// terms: 0 -> no terminals, so no images.
	if html := render("image: busybox:1.36\nterms: 0"); !strings.Contains(html, `const TERM_IMAGES = []`) {
		t.Errorf("terms: 0 should yield an empty image list:\n%s", firstLineWith(html, "TERM_IMAGES"))
	}

	// Exactly as many images as terminals is the other boundary that must work.
	html = render("image: busybox:1.36\nterms: 1\nterm_images:\n  - nginx:alpine")
	if !strings.Contains(html, `const TERM_IMAGES = ["nginx:alpine"]`) {
		t.Errorf("one image for one terminal:\n%s", firstLineWith(html, "TERM_IMAGES"))
	}
	// (More images than terminals is a build error — see
	// TestTerminalLimitsAreLoud.)
}

func firstLineWith(s, needle string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return "(not found)"
}

// The platform caps terminals at MaxTerminals. Asking for more used to be
// trimmed in silence, which produced three separate confusions downstream —
// fewer panels than written, dead .termN fences, and port links that claim
// "start a session first" forever. Every one of those must fail the build.
func TestTerminalLimitsAreLoud(t *testing.T) {
	build := func(fm, body string) error {
		_, err := Render("x", []byte("---\n"+fm+"\n---\n"+body), "s", nil)
		return err
	}

	cases := []struct{ name, fm, body, want string }{
		{"terms above the cap", "terms: 8", "hi", "out of range"},
		{"negative terms", "terms: -1", "hi", "out of range"},
		{"more images than terminals", "terms: 2\nterm_images:\n  - a:1\n  - b:2\n  - c:3", "hi", "would be ignored"},
		{"click-to-run block for a missing node", "terms: 2", "```.term5\necho x\n```", "only has 2 terminal"},
		{"port link to a missing node", "terms: 2", `[s](/){:data-term=".term4"}{:data-port="80"}`, "only has 2 terminal"},
		{"any terminal reference with terms: 0", "terms: 0", "```.term1\necho x\n```", "no terminals"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := build(c.fm, c.body)
			if err == nil {
				t.Fatalf("built silently; expected an error mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}

	// The valid shapes must keep building: at the cap, references in range,
	// and the default (no terms:, one terminal, .term1).
	for _, ok := range []struct{ fm, body string }{
		{"terms: 6", "```.term6\necho x\n```"},
		{"terms: 2\nterm_images:\n  - a:1", `[s](/){:data-term=".term2"}{:data-port="80"}`},
		{"image: alpine:3.20", "```.term1\necho x\n```"},
		{"terms: 0", "no console here"},
	} {
		if err := build(ok.fm, ok.body); err != nil {
			t.Errorf("valid lesson rejected (%s): %v", ok.fm, err)
		}
	}
}
