package scoring

import (
	"fmt"
	"sort"
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
