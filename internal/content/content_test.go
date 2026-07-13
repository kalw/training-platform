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
	if !strings.Contains(l.HTML, `class="exercise-demo"`) || !strings.Contains(l.HTML, `data-hash-code="`+ch.Hash+`"`) {
		t.Error("exercise demo button / hash_code not rendered")
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

	// An authored {:id="exerciseDemo"} link SUPPLIES the routing of the always-
	// present "Test Exercise" button (the reported bug was that marking an
	// inline link dropped the button, and the default button hit port 80 not
	// the exercise's real result-page port). The button adopts port/path/term;
	// the marked link stays inline as a plain port link.
	src := []byte("---\ntitle: E\n---\n" +
		"{% exercise %}\nFix it so " +
		"[webserver](/status){:id=\"exerciseDemo\"}{:data-term=\".term2\"}{:data-port=\"8888\"} is green.\n" +
		"{% endexercise %}\n")
	l, err := Render("ex", src, "s", res)
	if err != nil {
		t.Fatal(err)
	}
	h := l.Challenges[0].Hash
	// The button is present, styled, carries the hash, and adopted the
	// authored port / path / term (not the port-80 default).
	if !strings.Contains(l.HTML, "Test Exercise") {
		t.Error("built-in Test Exercise button must always render")
	}
	if !strings.Contains(l.HTML, `data-hash-code="`+h+`"`) {
		t.Error("demo button missing the exercise hash_code")
	}
	for _, want := range []string{`data-port="8888"`, `data-path="/status"`, `data-term=".term2"`} {
		if !strings.Contains(l.HTML, want) {
			t.Errorf("demo button did not adopt authored routing %s", want)
		}
	}
	if strings.Contains(l.HTML, `data-port="80"`) {
		t.Error("demo button kept the default port 80 instead of the authored 8888")
	}
	// The authored inline link survives as a plain preview link.
	if !strings.Contains(l.HTML, `href="/status"`) {
		t.Error("authored inline link lost")
	}

	// Without an authored link the button uses the defaults.
	l2, err := Render("ex2", []byte("{% exercise %}\nFix it.\n{% endexercise %}\n"), "s", res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l2.HTML, "Test Exercise") || !strings.Contains(l2.HTML, `data-port="80"`) ||
		!strings.Contains(l2.HTML, `data-path="/result.html"`) {
		t.Error("default demo button (port 80, /result.html) missing")
	}

	// Two exercises: Nth marked link supplies the Nth button's routing.
	src3 := []byte("[a](/a){:class=\"exerciseDemo\"}{:data-port=\"1111\"}\n\n" +
		"{% exercise %}\none\n{% endexercise %}\n\n" +
		"[b](/b){:class=\"exerciseDemo\"}{:data-port=\"2222\"}\n\n" +
		"{% exercise %}\ntwo\n{% endexercise %}\n")
	l3, err := Render("ex3", src3, "s", res)
	if err != nil {
		t.Fatal(err)
	}
	h1, h2 := l3.Challenges[0].Hash, l3.Challenges[1].Hash
	if !strings.Contains(l3.HTML, `data-port="1111" data-path="/a" data-term=".term1" href="#">Test Exercise</a>`) &&
		!strings.Contains(l3.HTML, `data-hash-code="`+h1+`" data-port="1111"`) {
		t.Error("first button did not adopt the first authored link's port")
	}
	if !strings.Contains(l3.HTML, `data-hash-code="`+h2+`" data-port="2222"`) {
		t.Error("second button did not adopt the second authored link's port")
	}
	if n := strings.Count(l3.HTML, `>Test Exercise</a>`); n != 2 {
		t.Errorf("expected 2 Test Exercise buttons, got %d", n)
	}
}
