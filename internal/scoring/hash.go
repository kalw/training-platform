// Package scoring implements the platform's answer-verification contract:
// quizzes and exercises are graded server-side by comparing SHA-256 hashes,
// so plaintext solutions never reach the browser.
//
// This is the same recipe the legacy platform duplicated across Ruby
// (jekyll-quizz.rb) and bash (exportChallenges.sh) and a patched CTFd; here
// it lives in exactly one place. The recipe is byte-sensitive — producers of
// the page DOM and of the challenge store must agree exactly:
//
//	challenge hash = sha256(question_text + lesson_filename)
//	flag hash      = sha256(answer + salt)
//
// Only question text + filename go into the challenge hash (front matter is
// deliberately excluded, so editing image:/terms: never invalidates a
// challenge).
package scoring

import (
	"crypto/sha256"
	"encoding/hex"
)

// ChallengeHash identifies a quiz/exercise question independent of its
// answer: sha256(question + lessonFilename).
func ChallengeHash(question, lessonFilename string) string {
	sum := sha256.Sum256([]byte(question + lessonFilename))
	return hex.EncodeToString(sum[:])
}

// FlagHash is the salted hash of a single answer: sha256(answer + salt).
// The salt is a deploy-wide secret; without it, flag hashes can't be
// precomputed from a wordlist of likely answers.
func FlagHash(answer, salt string) string {
	sum := sha256.Sum256([]byte(answer + salt))
	return hex.EncodeToString(sum[:])
}
