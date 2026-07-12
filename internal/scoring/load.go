package scoring

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadFile seeds the store from a JSON challenges file produced by the
// lessons build (the same source of truth as the page DOM — flags are the
// pre-salted answer hashes, so plaintext never appears here either).
//
// Format: a JSON array of Challenge objects, e.g.
//
//	[{"hash":"<challenge hash>","name":"docker-ps","value":10,
//	  "flags":["<sha256(answer+salt)>"]}]
//
// Returns the number of challenges loaded. Missing file is a hard error;
// callers that treat seeding as optional should stat it first.
func (s *Store) LoadFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var challenges []Challenge
	if err := json.Unmarshal(b, &challenges); err != nil {
		return 0, fmt.Errorf("parsing challenges file: %w", err)
	}
	for _, c := range challenges {
		if err := s.Upsert(c); err != nil {
			return 0, err
		}
	}
	return len(challenges), nil
}
