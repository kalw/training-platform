// Package content renders training lessons — Markdown with YAML front matter
// and {% quiz %} / {% exercise %} blocks — into Play-With-Docker-compatible
// HTML pages, and imports the quizzes/exercises into challenges for the
// scoring engine. It is the Go replacement for the legacy stack's
// jekyll-quizz.rb + jekyll-exercise.rb (render time) and exportChallenges.sh
// (challenge import), all in one place so the hash recipe can't drift.
//
// The hash contract is unchanged and shared with internal/scoring:
//
//	challenge hash = sha256(question_or_exercise_text + lesson_filename)
//	quiz flag      = sha256(answer + salt)
//	exercise flag  = phash$<dHash hex>[:threshold]   (screenshot proof)
package content

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kalw/training-platform/internal/scoring"
	"gopkg.in/yaml.v3"
)

// FrontMatter is the subset of a lesson's YAML header this renderer reads.
// Unknown keys are ignored (front matter is not part of the hash recipe, so
// adding fields is always safe).
type FrontMatter struct {
	Title string `yaml:"title"`
	// Image boots the session instance for this lesson (a DinD image, or a
	// custom exercise image built FROM training-exercises-template).
	Image string `yaml:"image"`
	// ExerciseResult selects the reference result page whose perceptual hash
	// is computed at build time to grade this lesson's exercise. It's a path
	// relative to the lessons directory (an .html rendered headlessly, or a
	// .png/.jpg hashed directly); empty uses the built-in default result
	// page. Mirrors the legacy `exercise_result:` front-matter key.
	ExerciseResult string `yaml:"exercise_result"`
	// ExerciseThreshold overrides the default Hamming threshold (12).
	ExerciseThreshold int `yaml:"exercise_threshold"`
	// ExercisePhash is an optional explicit override of the computed flag
	// ("phash$<hex>[:threshold]"), for when there's no renderable reference.
	ExercisePhash string `yaml:"exercise_phash"`
}

// ExerciseResolver computes an exercise's reference phash flag from its
// result reference at build time. *PhashResolver implements it; tests inject
// a stub. A nil resolver means exercises are recorded without grading.
type ExerciseResolver interface {
	Resolve(resultRef string, threshold int) (string, error)
}

// Lesson is a rendered lesson plus the challenges it registers.
type Lesson struct {
	Slug        string
	FrontMatter FrontMatter
	HTML        string
	Challenges  []scoring.Challenge
}

var (
	quizRe     = regexp.MustCompile(`(?s)\{%\s*quiz\s*%\}(.*?)\{%\s*endquiz\s*%\}`)
	exerciseRe = regexp.MustCompile(`(?s)\{%\s*exercise\s*%\}(.*?)\{%\s*endexercise\s*%\}`)
	answerRe   = regexp.MustCompile(`^\s*-\s*\[( |x|X)\]\s*(.+?)\s*$`)
)

// Render turns one lesson's bytes into a Lesson. slug is the output name
// (usually the filename without extension); salt is the scoring salt;
// resolver computes exercise phash flags from result pages (nil = record
// exercises without a grading flag).
func Render(slug string, src []byte, salt string, resolver ExerciseResolver) (*Lesson, error) {
	fm, body, err := splitFrontMatter(src)
	if err != nil {
		return nil, err
	}
	l := &Lesson{Slug: slug, FrontMatter: fm}

	// Extract quiz/exercise blocks, replacing each with an HTML placeholder
	// that survives Markdown rendering, then render the remaining Markdown.
	// A block error aborts the whole render — a lesson that can't produce its
	// challenge (e.g. the exercise phash can't be computed) must fail the
	// build loudly, never ship silently with a missing challenge.
	var placeholders []string
	var blockErr error
	body = quizRe.ReplaceAllStringFunc(body, func(m string) string {
		if blockErr != nil {
			return ""
		}
		inner := quizRe.FindStringSubmatch(m)[1]
		htmlBlock, ch, err := l.renderQuiz(slug, inner, salt)
		if err != nil {
			blockErr = fmt.Errorf("quiz: %w", err)
			return ""
		}
		l.Challenges = append(l.Challenges, ch)
		placeholders = append(placeholders, htmlBlock)
		return "\n" + placeholderToken(len(placeholders)-1) + "\n"
	})
	body = exerciseRe.ReplaceAllStringFunc(body, func(m string) string {
		if blockErr != nil {
			return ""
		}
		inner := exerciseRe.FindStringSubmatch(m)[1]
		htmlBlock, ch, err := l.renderExercise(slug, inner, resolver)
		if err != nil {
			blockErr = fmt.Errorf("exercise: %w", err)
			return ""
		}
		l.Challenges = append(l.Challenges, ch)
		placeholders = append(placeholders, htmlBlock)
		return "\n" + placeholderToken(len(placeholders)-1) + "\n"
	})
	if blockErr != nil {
		return nil, blockErr
	}

	rendered := renderMarkdown(body)
	for i, ph := range placeholders {
		rendered = strings.Replace(rendered, placeholderToken(i), ph, 1)
	}

	l.HTML = layout(fm, rendered)
	return l, nil
}

// placeholderToken is a bare HTML comment; renderMarkdown passes lines
// starting with "<" through untouched, so it survives rendering on its own
// line and we substitute the real block HTML back in afterwards.
func placeholderToken(i int) string { return fmt.Sprintf("<!--BLOCK:%d-->", i) }

// renderQuiz turns a quiz block into HTML + a Challenge. The block body is
// the question followed by "- [x]"/"- [ ]" choices; correct choices become
// flags. Each choice's salted hash is baked into the DOM so the page never
// contains the plaintext answer, and grading happens server-side.
func (l *Lesson) renderQuiz(slug, inner, salt string) (string, scoring.Challenge, error) {
	var question []string
	type choice struct {
		text    string
		hash    string
		correct bool
	}
	var choices []choice
	for _, line := range strings.Split(strings.TrimSpace(inner), "\n") {
		if m := answerRe.FindStringSubmatch(line); m != nil {
			choices = append(choices, choice{
				text:    m[2],
				hash:    scoring.FlagHash(m[2], salt),
				correct: strings.EqualFold(m[1], "x"),
			})
			continue
		}
		if strings.TrimSpace(line) != "" {
			question = append(question, strings.TrimSpace(line))
		}
	}
	q := strings.Join(question, " ")
	if q == "" || len(choices) == 0 {
		return "", scoring.Challenge{}, fmt.Errorf("quiz needs a question and at least one choice")
	}
	chHash := scoring.ChallengeHash(q, slug)
	var flags []string
	var opts strings.Builder
	for i, c := range choices {
		if c.correct {
			flags = append(flags, c.hash)
		}
		fmt.Fprintf(&opts, `<label><input type="radio" name="q-%s" data-flag="%s"> %s</label>`,
			chHash[:8], c.hash, htmlEscape(c.text))
		_ = i
	}
	block := fmt.Sprintf(`<div class="quiz" data-challenge="%s"><div class="q">%s</div>%s<button class="quiz-submit">Submit</button><div class="verdict"></div></div>`,
		chHash, htmlEscape(q), opts.String())
	return block, scoring.Challenge{Hash: chHash, Name: "quiz-" + shortName(q), Value: 10, Flags: flags}, nil
}

// renderExercise turns an exercise block into HTML + a Challenge graded by
// screenshot (phash). The exercise text hashes to the challenge id; the flag
// is the front-matter reference phash. The rendered "Test Exercise" link
// carries the hash_code the verify script submits with (mirroring the
// exercises-template contract).
func (l *Lesson) renderExercise(slug, inner string, resolver ExerciseResolver) (string, scoring.Challenge, error) {
	text := strings.TrimSpace(inner)
	if text == "" {
		return "", scoring.Challenge{}, fmt.Errorf("exercise block is empty")
	}
	chHash := scoring.ChallengeHash(text, slug)

	// Determine the reference flag. An explicit exercise_phash wins; otherwise
	// compute it from the result page (front-matter exercise_result, or the
	// built-in default) via the resolver — this is the challenge-creation-time
	// perceptual hashing, not a value hand-written in the lesson.
	threshold := l.FrontMatter.ExerciseThreshold
	var flags []string
	switch {
	case l.FrontMatter.ExercisePhash != "":
		flags = append(flags, l.FrontMatter.ExercisePhash)
	case resolver != nil:
		flag, err := resolver.Resolve(l.FrontMatter.ExerciseResult, threshold)
		if err != nil {
			return "", scoring.Challenge{}, fmt.Errorf("computing exercise phash: %w", err)
		}
		flags = append(flags, flag)
	}
	block := fmt.Sprintf(`<div class="exercise" data-challenge="%s"><div class="q">%s</div>`+
		`<a id="exerciseDemo" class="exercise-demo" data-hash-code="%s" data-term=".term1" data-port="80" href="#">Test Exercise</a>`+
		`<div class="verdict"></div></div>`,
		chHash, renderMarkdown(text), chHash)
	return block, scoring.Challenge{Hash: chHash, Name: "exercise-" + shortName(text), Value: 20, Flags: flags}, nil
}

func splitFrontMatter(src []byte) (FrontMatter, string, error) {
	s := string(src)
	var fm FrontMatter
	if !strings.HasPrefix(s, "---") {
		return fm, s, nil // no front matter is allowed
	}
	rest := strings.TrimPrefix(s, "---")
	// leading newline then the YAML until the next "---" line.
	rest = strings.TrimLeft(rest, "\r\n")
	end := regexp.MustCompile(`(?m)^---\s*$`).FindStringIndex(rest)
	if end == nil {
		return fm, s, fmt.Errorf("front matter opened with --- but never closed")
	}
	yamlPart := rest[:end[0]]
	body := rest[end[1]:]
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return fm, "", fmt.Errorf("parsing front matter: %w", err)
	}
	return fm, strings.TrimLeft(body, "\r\n"), nil
}

func shortName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" {
		s = "challenge"
	}
	return s
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
