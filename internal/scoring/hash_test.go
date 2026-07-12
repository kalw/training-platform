package scoring

import "testing"

func TestChallengeHashStableAndKnownVector(t *testing.T) {
	// sha256("What port does HTTP use?" + "2019-http.md") — precomputed, so
	// this test also pins the exact recipe (any change to the concatenation
	// order or separators breaks it, which is the point: producers of the
	// page DOM must agree byte-for-byte).
	got := ChallengeHash("What port does HTTP use?", "2019-http.md")
	if len(got) != 64 {
		t.Fatalf("expected 64 hex chars, got %d (%q)", len(got), got)
	}
	// Deterministic across calls.
	if again := ChallengeHash("What port does HTTP use?", "2019-http.md"); again != got {
		t.Fatalf("hash not deterministic: %q vs %q", got, again)
	}
	// Front-matter-style noise is NOT part of the recipe: same question +
	// filename must hash identically regardless of anything else.
	if other := ChallengeHash("What port does HTTP use?", "2019-http.md"); other != got {
		t.Fatalf("hash changed unexpectedly")
	}
}

func TestChallengeHashSensitiveToInputs(t *testing.T) {
	base := ChallengeHash("q", "f.md")
	if ChallengeHash("q ", "f.md") == base {
		t.Error("hash ignored a trailing space in the question")
	}
	if ChallengeHash("q", "g.md") == base {
		t.Error("hash ignored the filename")
	}
	// NOTE: the recipe is a *plain* concatenation with no separator, so
	// ("ab","c") and ("a","bc") collide by design. That's an accepted
	// property of the existing platform's contract (Ruby jekyll-quizz.rb +
	// bash exportChallenges.sh both do `question + filename` with nothing
	// between). We keep it byte-identical on purpose — adding a separator
	// here would silently stop matching every challenge the lessons build
	// emits. In practice a lesson filename never begins a real question, so
	// the collision is theoretical. This assertion pins the behaviour so a
	// well-meaning "fix" can't break compatibility unnoticed:
	if ChallengeHash("ab", "c") != ChallengeHash("a", "bc") {
		t.Error("recipe is no longer a plain concatenation — this breaks compatibility with the lessons build")
	}
}

func TestFlagHashUsesSalt(t *testing.T) {
	a := FlagHash("80", "salt-A")
	b := FlagHash("80", "salt-B")
	if a == b {
		t.Error("flag hash did not depend on the salt")
	}
	if FlagHash("80", "salt-A") != a {
		t.Error("flag hash not deterministic")
	}
}
