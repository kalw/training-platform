package scoring

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func seededStore(t *testing.T, path string) *Store {
	t.Helper()
	s := NewStore(nil)
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	if _, err := s.UseJournal(j); err != nil {
		t.Fatal(err)
	}
	_ = s.Upsert(Challenge{Hash: "c1", Name: "quiz-one", Value: 10, Flags: []string{"flag1"}})
	_ = s.Upsert(Challenge{Hash: "c2", Name: "exercise-two", Value: 20,
		Verify: &VerifySpec{Port: 80, Path: "/", Expect: "ok"}})
	return s
}

// The point of the journal: solves outlive the process.
func TestSolvesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solves.jsonl")

	s1 := seededStore(t, path)
	if correct, known := s1.Grade("c1", "flag1", "alice"); !correct || !known {
		t.Fatal("quiz solve did not grade")
	}
	s1.RecordSolve("c2", "bob") // the verify path

	// A brand-new store over the same log = a restart.
	s2 := seededStore(t, path)
	if !s2.Solved("c1", "alice") {
		t.Error("alice's quiz solve did not survive the restart")
	}
	if !s2.Solved("c2", "bob") {
		t.Error("bob's exercise solve did not survive the restart")
	}
	if s2.Solved("c1", "carol") {
		t.Error("recovered a solve that never happened")
	}
	if got := len(s2.Results()); got != 2 {
		t.Errorf("recovered %d solves, want 2", got)
	}
}

// Re-submitting must not grow the log without bound.
func TestJournalAppendsOnlyNewSolves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solves.jsonl")
	s := seededStore(t, path)

	for i := 0; i < 5; i++ {
		s.Grade("c1", "flag1", "alice")
		s.RecordSolve("c1", "alice")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := countLines(b); n != 1 {
		t.Errorf("log has %d lines for one repeated solve, want 1", n)
	}
}

// A crash can leave a torn final line; booting must not fail on it.
func TestReplayToleratesCorruptTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solves.jsonl")
	good := `{"user":"alice","challenge":"c1","at":"2026-01-01T00:00:00Z"}` + "\n"
	torn := `{"user":"bob","chall` // interrupted mid-write, no newline
	if err := os.WriteFile(path, []byte(good+torn), 0o644); err != nil {
		t.Fatal(err)
	}

	s := seededStore(t, path)
	if !s.Solved("c1", "alice") {
		t.Error("the intact record before the torn line was lost")
	}
	if s.Solved("c1", "bob") {
		t.Error("a torn record was accepted")
	}

	// And the store keeps working afterwards, appending to the same file.
	s.RecordSolve("c2", "carol")
	s2 := seededStore(t, path)
	if !s2.Solved("c2", "carol") || !s2.Solved("c1", "alice") {
		t.Error("writes after recovering from a torn log did not persist")
	}
}

func TestJournalConcurrentAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solves.jsonl")
	s := seededStore(t, path)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.RecordSolve("c1", string(rune('a'+i%10)))
		}(i)
	}
	wg.Wait()

	// 10 distinct users, each recorded once, and the file must still parse.
	s2 := seededStore(t, path)
	if got := len(s2.Results()); got != 10 {
		t.Errorf("recovered %d solves, want 10 distinct", got)
	}
}

// An empty path disables persistence rather than erroring — the in-memory
// mode CI and content authoring rely on.
func TestNoJournalIsValid(t *testing.T) {
	j, err := OpenJournal("")
	if err != nil || j != nil {
		t.Fatalf("OpenJournal(\"\") = %v, %v; want nil, nil", j, err)
	}
	if err := j.Append("u", "c"); err != nil {
		t.Errorf("Append on nil journal: %v", err)
	}
	n, skipped, err := j.Replay(func(string, string) { t.Error("visited on nil journal") })
	if n != 0 || skipped != 0 || err != nil {
		t.Errorf("Replay on nil journal = %d,%d,%v", n, skipped, err)
	}
	if err := j.Close(); err != nil {
		t.Errorf("Close on nil journal: %v", err)
	}
}

// The journal is created with its parent directory (a fresh volume mount is
// often an empty dir, or missing entirely).
func TestOpenJournalCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "solves.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err := j.Append("u", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("log not created at %s: %v", path, err)
	}
}

func TestStandings(t *testing.T) {
	s := seededStore(t, filepath.Join(t.TempDir(), "s.jsonl"))
	s.RecordSolve("c1", "alice") // 10
	s.RecordSolve("c2", "alice") // +20 = 30
	s.RecordSolve("c1", "bob")   // 10

	ranking, challenges, total := s.Standings()
	if total != 30 {
		t.Errorf("total points = %d, want 30", total)
	}
	if len(ranking) != 2 || ranking[0].User != "alice" || ranking[0].Points != 30 || ranking[0].Solved != 2 {
		t.Fatalf("ranking = %+v, want alice first with 30/2", ranking)
	}
	if ranking[1].User != "bob" || ranking[1].Points != 10 {
		t.Errorf("second place = %+v, want bob with 10", ranking[1])
	}
	byName := map[string]int{}
	for _, c := range challenges {
		byName[c.Name] = c.Solved
	}
	if byName["quiz-one"] != 2 || byName["exercise-two"] != 1 {
		t.Errorf("completion counts = %v, want quiz-one:2 exercise-two:1", byName)
	}
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
