package scoring

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Journal makes solves survive a restart without a database.
//
// A solve is a tiny, append-only, idempotent fact — "user U solved challenge
// C" — so the storage that fits is an append-only JSON-lines file, not a SQL
// engine: appends are atomic enough at this size, replay is a sequential
// read, and the file stays inspectable with `cat`. Challenges themselves are
// NOT journalled; they are re-seeded from the build's challenges.json at
// every boot (stateless content), so the journal only holds what can't be
// recomputed.
//
// Durability: each record is appended and fsync'd before the write returns,
// so an acknowledged solve is on disk. A crash mid-append can leave a torn
// final line; replay skips unparseable lines rather than refusing to start —
// losing at most the record that was never acknowledged.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// solveRecord is one journal line. Kept minimal and additive: unknown fields
// are ignored on replay, so the format can grow without breaking old files.
type solveRecord struct {
	User      string `json:"user"`
	Challenge string `json:"challenge"`
	At        string `json:"at"` // RFC3339, informational
}

// OpenJournal opens (creating it and any parent directories) the solve log at
// path. An empty path returns a nil *Journal, which is valid and disables
// persistence — the store then behaves exactly as before.
func OpenJournal(path string) (*Journal, error) {
	if path == "" {
		return nil, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating solve-log directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening solve log %q: %w", path, err)
	}
	// Heal a torn tail. If a crash left the last line unterminated, appending
	// straight away would glue the next record onto it and lose BOTH; a
	// newline closes the damaged line so replay skips only that one.
	if err := terminateLastLine(path, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Journal{path: path, f: f}, nil
}

// terminateLastLine appends a newline when the file is non-empty and doesn't
// already end with one.
func terminateLastLine(path string, f *os.File) error {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return err
	}
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	last := make([]byte, 1)
	if _, err := r.ReadAt(last, fi.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = f.Write([]byte("\n"))
	return err
}

// Append durably records one solve. Safe on a nil Journal (no-op).
func (j *Journal) Append(user, challenge string) error {
	if j == nil {
		return nil
	}
	line, err := json.Marshal(solveRecord{User: user, Challenge: challenge, At: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.f.Write(append(line, '\n')); err != nil {
		return err
	}
	// fsync: a solve the learner was told was recorded must survive a crash.
	return j.f.Sync()
}

// Replay calls visit for every solve in the log, oldest first. Malformed
// lines (e.g. a torn write from a crash) are skipped and counted rather than
// failing the boot. Safe on a nil Journal.
func (j *Journal) Replay(visit func(user, challenge string)) (recovered, skipped int, err error) {
	if j == nil {
		return 0, 0, nil
	}
	f, err := os.Open(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Solve lines are tiny; allow a generous line so a corrupted file with a
	// huge "line" still errors cleanly instead of silently truncating.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		var r solveRecord
		if json.Unmarshal(b, &r) != nil || r.User == "" || r.Challenge == "" {
			skipped++
			continue
		}
		visit(r.User, r.Challenge)
		recovered++
	}
	if err := sc.Err(); err != nil {
		// A read error mid-file: keep what we recovered, report the rest.
		return recovered, skipped, err
	}
	return recovered, skipped, nil
}

// Close releases the log file. Safe on a nil Journal.
func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Close()
}
