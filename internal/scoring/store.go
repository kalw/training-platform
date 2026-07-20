package scoring

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Challenge is one gradable question, keyed by its ChallengeHash. Flags are
// the set of accepting salted-answer hashes (multiple-correct is allowed).
// Exercise challenges use a perceptual-hash flag of the form
// "phash$<hex>[:<threshold>]" instead of an exact answer hash; the store
// treats those opaquely (see Grade).
type Challenge struct {
	Hash  string   `json:"hash"`
	Name  string   `json:"name"`
	Value int      `json:"value"`
	Flags []string `json:"flags"`
	// Verify, when set, grades this exercise by fetching the learner's result
	// page server-side and asserting its content — exact, and unlike the
	// screenshot proof it can't be produced by the browser. It takes
	// precedence over the phash flag (see the /challenges/verify endpoint).
	Verify *VerifySpec `json:"verify,omitempty"`
}

// VerifySpec says where the learner's result page lives inside their session
// and what it must contain. Port/Path are fixed at build time from the
// lesson — never taken from the client — so the endpoint can't be turned into
// an arbitrary-URL fetcher.
type VerifySpec struct {
	Port int    `json:"port"`
	Path string `json:"path"`
	// Expect is a plain substring the page body must contain.
	Expect string `json:"expect,omitempty"`
	// ExpectRegex is an alternative to Expect (RE2, matched against the body).
	ExpectRegex string `json:"expect_regex,omitempty"`
}

// Matches reports whether a fetched page body satisfies the spec.
func (v *VerifySpec) Matches(body string) bool {
	switch {
	case v.Expect != "":
		return strings.Contains(body, v.Expect)
	case v.ExpectRegex != "":
		re, err := regexp.Compile(v.ExpectRegex)
		return err == nil && re.MatchString(body)
	}
	return false
}

// Assertive reports whether the spec actually asserts something (a spec with
// neither Expect nor ExpectRegex would accept any page, so it's ignored).
func (v *VerifySpec) Assertive() bool {
	return v != nil && (v.Expect != "" || v.ExpectRegex != "")
}

// Store is an in-memory challenge registry. It replaces the patched-CTFd
// `challenge_hash` column + lookup endpoint: challenges are seeded at boot
// (from a challenges file produced by the lessons build) and resolved by
// hash at attempt time. Safe for concurrent use.
type Store struct {
	mu         sync.RWMutex
	byHash     map[string]*Challenge
	solves     map[string]map[string]bool // challengeHash -> set of userIDs
	phashGrade func(challengeFlag, submitted string) bool
}

// NewStore returns an empty Store. phashGrader, if non-nil, grades
// perceptual-hash exercise flags (flag "phash$..." vs a submitted capture);
// pass nil to reject exercise submissions until a grader is wired.
func NewStore(phashGrader func(challengeFlag, submitted string) bool) *Store {
	return &Store{
		byHash:     map[string]*Challenge{},
		solves:     map[string]map[string]bool{},
		phashGrade: phashGrader,
	}
}

// Upsert adds or replaces a challenge, keyed by c.Hash.
func (s *Store) Upsert(c Challenge) error {
	if c.Hash == "" {
		return fmt.Errorf("challenge has empty hash")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	s.byHash[c.Hash] = &cp
	return nil
}

// Get returns the challenge for a hash, or false if unknown.
func (s *Store) Get(hash string) (Challenge, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.byHash[hash]
	if !ok {
		return Challenge{}, false
	}
	return *c, true
}

// Len reports how many challenges are loaded.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byHash)
}

// Grade evaluates a submission against the challenge identified by
// challengeHash. submitted is either a pre-hashed answer (quiz: the browser
// sends FlagHash(answer,salt), never plaintext) or an exercise capture that
// the phash grader compares against a "phash$..." flag. Returns
// (correct, known): known is false if the challenge hash isn't registered.
// A correct grade records a solve for userID (idempotent).
func (s *Store) Grade(challengeHash, submitted, userID string) (correct, known bool) {
	s.mu.RLock()
	c, ok := s.byHash[challengeHash]
	grade := s.phashGrade
	s.mu.RUnlock()
	if !ok {
		return false, false
	}

	for _, flag := range c.Flags {
		if isPhashFlag(flag) {
			if grade != nil && grade(flag, submitted) {
				correct = true
				break
			}
			continue
		}
		if flag == submitted {
			correct = true
			break
		}
	}

	if correct {
		s.mu.Lock()
		if s.solves[challengeHash] == nil {
			s.solves[challengeHash] = map[string]bool{}
		}
		s.solves[challengeHash][userID] = true
		s.mu.Unlock()
	}
	return correct, true
}

// RecordSolve marks challengeHash solved by userID (idempotent). Used by the
// server-side verify path, which establishes correctness by fetching the
// learner's page rather than by matching a flag.
func (s *Store) RecordSolve(challengeHash, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.solves[challengeHash] == nil {
		s.solves[challengeHash] = map[string]bool{}
	}
	s.solves[challengeHash][userID] = true
}

// Solved reports whether userID has solved challengeHash.
func (s *Store) Solved(challengeHash, userID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.solves[challengeHash][userID]
}

// List returns every challenge, sorted by name. Copies are returned; callers
// that expose these to clients must strip Flags (see the HTTP layer).
func (s *Store) List() []Challenge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Challenge, 0, len(s.byHash))
	for _, c := range s.byHash {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Solve is one (user, challenge) completion, for the scoreboard.
type Solve struct {
	User          string `json:"user"`
	ChallengeHash string `json:"challenge_hash"`
	ChallengeName string `json:"challenge_name"`
	Value         int    `json:"value"`
}

// Results returns every recorded solve, sorted by user then challenge name.
func (s *Store) Results() []Solve {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Solve
	for hash, users := range s.solves {
		name, value := hash, 0
		if c, ok := s.byHash[hash]; ok {
			name, value = c.Name, c.Value
		}
		for u := range users {
			out = append(out, Solve{User: u, ChallengeHash: hash, ChallengeName: name, Value: value})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User != out[j].User {
			return out[i].User < out[j].User
		}
		return out[i].ChallengeName < out[j].ChallengeName
	})
	return out
}

func isPhashFlag(flag string) bool {
	return len(flag) > 6 && flag[:6] == "phash$"
}
