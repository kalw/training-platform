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
