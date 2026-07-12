package scoring

import "testing"

func TestGradeQuiz(t *testing.T) {
	s := NewStore(nil)
	salt := "s3cr3t"
	ch := ChallengeHash("What port does HTTP use?", "2019-http.md")
	correct := FlagHash("80", salt)
	if err := s.Upsert(Challenge{Hash: ch, Name: "http-port", Value: 10, Flags: []string{correct}}); err != nil {
		t.Fatal(err)
	}

	if ok, known := s.Grade(ch, FlagHash("80", salt), "u1"); !known || !ok {
		t.Fatalf("correct answer not accepted (known=%v ok=%v)", known, ok)
	}
	if !s.Solved(ch, "u1") {
		t.Error("solve not recorded")
	}
	if ok, _ := s.Grade(ch, FlagHash("8080", salt), "u2"); ok {
		t.Error("wrong answer accepted")
	}
	if _, known := s.Grade("deadbeef", "x", "u1"); known {
		t.Error("unknown challenge reported as known")
	}
}

func TestGradeExercisePhash(t *testing.T) {
	// Grader that accepts only an exact flag match, standing in for the real
	// perceptual-hash comparator.
	s := NewStore(func(flag, submitted string) bool { return flag == "phash$abcd:12"+submitted })
	ch := ChallengeHash("Fix the broken nginx", "2020-nginx.md")
	_ = s.Upsert(Challenge{Hash: ch, Name: "nginx", Flags: []string{"phash$abcd:12"}})

	if ok, known := s.Grade(ch, "", "u1"); !known || !ok {
		t.Fatalf("phash grader not invoked/accepted (known=%v ok=%v)", known, ok)
	}
	if ok, _ := s.Grade(ch, "nope", "u1"); ok {
		t.Error("phash grader accepted a non-matching capture")
	}
}

func TestExerciseRejectedWithoutGrader(t *testing.T) {
	s := NewStore(nil) // no phash grader wired
	ch := ChallengeHash("q", "f.md")
	_ = s.Upsert(Challenge{Hash: ch, Flags: []string{"phash$abcd"}})
	if ok, _ := s.Grade(ch, "anything", "u1"); ok {
		t.Error("exercise accepted with no grader configured")
	}
}
