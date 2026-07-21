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
	if html := render("terms: 99"); !strings.Contains(html, "const TERMS = 6;") {
		t.Error("terms should clamp to 6")
	}
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

	// More images than terminals: the extras are ignored, not rendered.
	html = render("image: busybox:1.36\nterms: 1\nterm_images:\n  - nginx:alpine\n  - ignored:1")
	if !strings.Contains(html, `const TERM_IMAGES = ["nginx:alpine"]`) {
		t.Errorf("extra term_images should be dropped:\n%s", firstLineWith(html, "TERM_IMAGES"))
	}
}

func firstLineWith(s, needle string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return "(not found)"
}
