package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"net/http"
	"regexp"
	"time"
)

// Anonymous identity.
//
// Without social login every learner was the literal user "anonymous", so a
// whole classroom collapsed into one row on the scoreboard and their solves
// merged. Instead each browser gets a random, memorable name — "brisk-otter-4f2"
// — kept in a long-lived cookie, so learners are distinguishable and their
// progress is stable across page loads and platform restarts.
//
// This is identification, NOT authentication: the cookie is unsigned and a
// learner can set it to anything (they could equally submit any answer hash
// directly — see the forged-attempt tests). Real accounts come from social
// login, which does sign its cookie; when a user is logged in that identity
// wins over this one.
const anonCookie = "training_learner"

// anonCookieTTL is deliberately long: it is the learner's progress identity,
// and losing it looks to them like losing their solves.
const anonCookieTTL = 365 * 24 * time.Hour

type ctxKey struct{}

// nameRe is what we accept back from a cookie: our own generated shape only.
// Anything else (tampered, truncated, injected markup) is replaced with a
// fresh name, which keeps the scoreboard free of arbitrary client strings.
var nameRe = regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{3}$`)

// AnonymousMiddleware assigns every browser without one a stable anonymous
// learner name, and puts the resolved name in the request context for
// LearnerID to read. Wrap the whole handler with it.
func AnonymousMiddleware(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := ""
			if c, err := r.Cookie(anonCookie); err == nil && nameRe.MatchString(c.Value) {
				name = c.Value
			}
			if name == "" {
				name = RandomLearnerName()
				http.SetCookie(w, &http.Cookie{
					Name:     anonCookie,
					Value:    name,
					Path:     "/",
					HttpOnly: true,
					Secure:   secure,
					SameSite: http.SameSiteLaxMode,
					Expires:  time.Now().Add(anonCookieTTL),
				})
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, name)))
		})
	}
}

// LearnerID returns the anonymous learner name assigned to this request, or
// "anonymous" when the middleware isn't mounted (e.g. a direct API call from
// a script that carries no cookie).
func LearnerID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKey{}).(string); ok && v != "" {
		return v
	}
	return "anonymous"
}

// UserIDFunc resolves who a request is for scoring. A signed social-login
// session wins; otherwise the anonymous learner name is used. m may be nil
// (social login unconfigured).
func UserIDFunc(m *Manager) func(*http.Request) string {
	return func(r *http.Request) string {
		if m != nil {
			if id := m.UserID(r); id != "" && id != "anonymous" {
				return id
			}
		}
		return LearnerID(r)
	}
}

// RandomLearnerName builds a memorable "<adjective>-<animal>-<hex>" name. The
// hex suffix keeps collisions negligible across a classroom while the words
// keep it recognisable on the scoreboard.
func RandomLearnerName() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return pick(anonAdjectives) + "-" + pick(anonAnimals) + "-" + hex.EncodeToString(b)[:3]
}

func pick(list []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		return list[0]
	}
	return list[n.Int64()]
}

// Word lists: short, neutral and unambiguous — these end up on a shared
// scoreboard in a classroom.
var anonAdjectives = []string{
	"amber", "brave", "brisk", "calm", "clever", "cosmic", "crisp", "curious",
	"eager", "fluent", "gentle", "swift", "keen", "lucid", "merry", "nimble",
	"noble", "patient", "plucky", "quiet", "rapid", "serene", "sharp", "solar",
	"spry", "steady", "sunny", "tidy", "vivid", "witty", "zesty", "bright",
}

var anonAnimals = []string{
	"otter", "falcon", "badger", "heron", "lynx", "marten", "osprey", "puffin",
	"raven", "salmon", "stoat", "tapir", "vole", "wombat", "yak", "gecko",
	"ibex", "koala", "lemur", "manta", "narwhal", "ocelot", "panda", "quokka",
	"robin", "seal", "tern", "urchin", "viper", "walrus", "wren", "bison",
}
