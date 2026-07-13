package content

import (
	"strings"
	"testing"

	"github.com/kalw/training-platform/internal/scoring"
)

func TestRenderQuizLesson(t *testing.T) {
	src := []byte(`---
title: Q
image: busybox:1.36
---
# Heading

{% quiz %}
Which command lists the running containers?
- [x] docker ps
- [ ] docker ls
{% endquiz %}
`)
	salt := "s3cr3t"
	l, err := Render("02-containers-quiz", src, salt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.FrontMatter.Image != "busybox:1.36" {
		t.Errorf("front matter image = %q", l.FrontMatter.Image)
	}
	if len(l.Challenges) != 1 {
		t.Fatalf("expected 1 challenge, got %d", len(l.Challenges))
	}
	ch := l.Challenges[0]

	// Challenge hash must match the exact recipe against the slug.
	want := scoring.ChallengeHash("Which command lists the running containers?", "02-containers-quiz")
	if ch.Hash != want {
		t.Errorf("challenge hash mismatch:\n got %s\nwant %s", ch.Hash, want)
	}
	// Only the correct answer's salted hash is a flag.
	correct := scoring.FlagHash("docker ps", salt)
	if len(ch.Flags) != 1 || ch.Flags[0] != correct {
		t.Errorf("flags = %v, want [%s]", ch.Flags, correct)
	}
	// The page must carry the salted hashes, never the plaintext answer text
	// as a flag value, and must render the heading.
	if !strings.Contains(l.HTML, "<h1>Heading</h1>") {
		t.Error("markdown heading not rendered")
	}
	if !strings.Contains(l.HTML, correct) {
		t.Error("correct choice's salted hash not baked into the DOM")
	}
	if strings.Contains(l.HTML, `data-flag="docker ps"`) {
		t.Error("plaintext answer leaked as a flag attribute")
	}
}

func TestRenderExerciseComputesFlagViaResolver(t *testing.T) {
	src := []byte(`---
title: Ex
image: ghcr.io/kalw/broken:latest
exercise_result: result.html
exercise_threshold: 9
---
{% exercise %}
Fix the web server so the status page renders.
{% endexercise %}
`)
	// The resolver stands in for the build-time headless render + dHash.
	wantFlag := "phash$00000000deadbeef:9"
	var seenRef string
	var seenThr int
	res := funcResolver(func(ref string, thr int) (string, error) {
		seenRef, seenThr = ref, thr
		return wantFlag, nil
	})
	l, err := Render("03-fix", src, "salt", res)
	if err != nil {
		t.Fatal(err)
	}
	if seenRef != "result.html" || seenThr != 9 {
		t.Errorf("resolver got ref=%q thr=%d, want result.html/9", seenRef, seenThr)
	}
	ch := l.Challenges[0]
	if len(ch.Flags) != 1 || ch.Flags[0] != wantFlag {
		t.Errorf("exercise flag = %v, want computed %q", ch.Flags, wantFlag)
	}
	if !strings.Contains(l.HTML, `id="exerciseDemo"`) || !strings.Contains(l.HTML, `data-hash-code="`+ch.Hash+`"`) {
		t.Error("exercise demo link / hash_code not rendered")
	}
}

func TestExercisePhashOverrideWins(t *testing.T) {
	src := []byte("---\nexercise_phash: phash$0000000000000001:5\n---\n{% exercise %}\nx\n{% endexercise %}\n")
	// Resolver would return something else; the explicit override must win.
	res := funcResolver(func(string, int) (string, error) { return "phash$ffffffffffffffff:12", nil })
	l, err := Render("ex", src, "salt", res)
	if err != nil {
		t.Fatal(err)
	}
	if l.Challenges[0].Flags[0] != "phash$0000000000000001:5" {
		t.Errorf("override ignored: %v", l.Challenges[0].Flags)
	}
}

type funcResolver func(string, int) (string, error)

func (f funcResolver) Resolve(ref string, thr int) (string, error) { return f(ref, thr) }

func TestPlainLessonHasNoChallenges(t *testing.T) {
	l, err := Render("01-plain", []byte("---\ntitle: Plain\n---\n# Hi\n\nJust text.\n"), "salt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Challenges) != 0 {
		t.Errorf("plain lesson produced %d challenges", len(l.Challenges))
	}
	if !strings.Contains(l.HTML, "Just text.") {
		t.Error("body not rendered")
	}
}

func TestAuthoredExerciseDemoLinkPairing(t *testing.T) {
	res := funcResolver(func(string, int) (string, error) { return "phash$0000000000000001:5", nil })

	// An authored {:id="exerciseDemo"} link takes over from the built-in
	// button: it gets the exercise's hash_code, keeps its own port/path, and
	// the auto "Test Exercise" anchor disappears (the reported bug: the auto
	// button hardcoded port 80 + /result.html regardless of the lesson).
	src := []byte("---\ntitle: E\n---\n" +
		"{% exercise %}\nFix it so " +
		"[webserver](/status){:id=\"exerciseDemo\"}{:data-term=\".term1\"}{:data-port=\"8888\"} is green.\n" +
		"{% endexercise %}\n")
	l, err := Render("ex", src, "s", res)
	if err != nil {
		t.Fatal(err)
	}
	h := l.Challenges[0].Hash
	if !strings.Contains(l.HTML, `data-port="8888" data-hash-code="`+h+`"`) {
		t.Error("authored demo link did not receive the exercise hash_code")
	}
	if strings.Contains(l.HTML, "Test Exercise") || strings.Contains(l.HTML, `data-port="80"`) {
		t.Error("built-in Test Exercise button should be dropped when a demo link is authored")
	}
	if !strings.Contains(l.HTML, `href="/status"`) {
		t.Error("authored demo link href (result page path) lost")
	}

	// Without an authored link the built-in button stays.
	l2, err := Render("ex2", []byte("{% exercise %}\nFix it.\n{% endexercise %}\n"), "s", res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l2.HTML, "Test Exercise") {
		t.Error("built-in Test Exercise button missing when no demo link is authored")
	}

	// Two exercises: Nth marked link pairs with Nth block ({:class=...} form).
	src3 := []byte("[a](/a){:class=\"exerciseDemo\"}{:data-port=\"1111\"}\n\n" +
		"{% exercise %}\none\n{% endexercise %}\n\n" +
		"[b](/b){:class=\"exerciseDemo\"}{:data-port=\"2222\"}\n\n" +
		"{% exercise %}\ntwo\n{% endexercise %}\n")
	l3, err := Render("ex3", src3, "s", res)
	if err != nil {
		t.Fatal(err)
	}
	h1, h2 := l3.Challenges[0].Hash, l3.Challenges[1].Hash
	if !strings.Contains(l3.HTML, `data-port="1111" data-hash-code="`+h1+`"`) ||
		!strings.Contains(l3.HTML, `data-port="2222" data-hash-code="`+h2+`"`) {
		t.Error("Nth authored link not paired with Nth exercise block")
	}
	if strings.Contains(l3.HTML, "Test Exercise") {
		t.Error("no built-in buttons expected when every exercise has an authored link")
	}
}
