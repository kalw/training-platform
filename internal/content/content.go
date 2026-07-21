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
	"strconv"
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
	// Terms is the number of terminal windows on the page (0–6), one session
	// instance each, mirroring the legacy `terms:` front-matter key. Nil
	// defaults to 1; 0 renders a lesson with no console at all.
	Terms *int `yaml:"terms"`
	// TermImages optionally gives each terminal its own image, positionally:
	// entry i is node i+1. Shorter than `terms:` (or an empty entry) falls
	// back to `image:`, so a multi-node lesson only names the nodes that
	// differ — e.g. a server on node1 and a client on node2. Without it every
	// terminal boots the same `image:`.
	TermImages []string `yaml:"term_images"`
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
	// ExerciseExpect / ExerciseExpectRegex turn on server-side content
	// verification: the platform fetches the learner's result page itself
	// (through the same in-cluster routability the port router uses) and
	// asserts the body contains this. It is exact where the screenshot phash
	// is only perceptual — a dHash cannot see a text change — so when set it
	// decides the grade. The page is fetched at the exercise's demo routing
	// (port/path), which is fixed here at build time, never client-supplied.
	ExerciseExpect      string `yaml:"exercise_expect"`
	ExerciseExpectRegex string `yaml:"exercise_expect_regex"`
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

	// exerciseHashes are the exercise challenge hashes in source order, used
	// to pair authored {:id="exerciseDemo"} links with their exercise block.
	exerciseHashes []string
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
	rendered, routings := pairDemoLinks(rendered, l.exerciseHashes)

	// Server-side content verification: the page to fetch is the exercise's
	// demo routing (adopted from the authored mark, or the defaults), and
	// what it must contain comes from the front matter. Recorded on the
	// challenge so the server never takes the target from the client.
	if fm.ExerciseExpect != "" || fm.ExerciseExpectRegex != "" {
		for i, h := range l.exerciseHashes {
			r := defaultDemoRouting()
			if i < len(routings) {
				r = routings[i]
			}
			port, err := strconv.Atoi(r.port)
			if err != nil {
				return nil, fmt.Errorf("exercise demo port %q is not a number", r.port)
			}
			for j := range l.Challenges {
				if l.Challenges[j].Hash != h {
					continue
				}
				l.Challenges[j].Verify = &scoring.VerifySpec{
					Port:        port,
					Path:        r.path,
					Expect:      fm.ExerciseExpect,
					ExpectRegex: fm.ExerciseExpectRegex,
				}
			}
			// Tell the page this exercise is graded server-side, so the button
			// asks the platform to check the session instead of opening the
			// page for a screenshot.
			rendered = strings.Replace(rendered,
				`data-hash-code="`+h+`"`, `data-hash-code="`+h+`" data-verify="1"`, 1)
		}
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
	l.exerciseHashes = append(l.exerciseHashes, chHash)
	block := fmt.Sprintf(`<div class="exercise" data-challenge="%s"><div class="q">%s</div>`+
		`%s`+
		`<div class="verdict"></div></div>`,
		chHash, renderMarkdown(text), autoDemoAnchor(chHash))
	return block, scoring.Challenge{Hash: chHash, Name: "exercise-" + shortName(text), Value: 20, Flags: flags}, nil
}

// demoRouting is where an exercise's "Test Exercise" button sends the learner
// (the result page it screenshots as proof). Defaults mirror the
// exercises-template contract; an authored demo link overrides them.
type demoRouting struct {
	port, path, term, prefix, proto string
}

func defaultDemoRouting() demoRouting {
	return demoRouting{port: "80", path: "/result.html", term: ".term1"}
}

// autoDemoAnchor is the built-in "Test Exercise" button every exercise block
// renders. It's always present (the learner needs a clear submit affordance);
// an authored {:id="exerciseDemo"} link only supplies its routing, it never
// replaces it. Uses class (not id) so multiple exercises don't collide and it
// never clashes with an authored link's id.
func autoDemoAnchor(chHash string) string { return demoButton(chHash, defaultDemoRouting()) }

func demoButton(chHash string, r demoRouting) string {
	attrs := fmt.Sprintf(`data-port="%s" data-path="%s" data-term="%s"`, r.port, r.path, r.term)
	if r.prefix != "" {
		attrs += fmt.Sprintf(` data-host-prefix="%s"`, r.prefix)
	}
	if r.proto != "" {
		attrs += fmt.Sprintf(` data-protocol="%s"`, r.proto)
	}
	return fmt.Sprintf(`<a class="exercise-demo" data-hash-code="%s" %s href="#">Test Exercise</a>`, chHash, attrs)
}

var (
	anchorTagRe = regexp.MustCompile(`<a\b[^>]*>`)
	attrValRe   = regexp.MustCompile(`\b([a-zA-Z-]+)="([^"]*)"`)
)

// pairDemoLinks implements the legacy writing-tutorials.md contract for
// authored exercise demo links: the Nth anchor marked {:id="exerciseDemo"}
// (or {:class="exerciseDemo"} when a lesson has several) is paired with the
// Nth exercise block. Rather than replace the block's "Test Exercise" button,
// the marked link *supplies that button's routing* (port, path, term, prefix,
// protocol) — so the learner always gets a clear button AND it opens the
// right result page (often a non-80 port the exercise image serves). The
// marked link itself stays a plain inline port link (a live preview).
// It also returns the routing each exercise ended up with (defaults where the
// author wrote no mark), so the build can record the same target on the
// challenge for server-side verification.
func pairDemoLinks(html string, hashes []string) (string, []demoRouting) {
	routings := make([]demoRouting, len(hashes))
	for i := range routings {
		routings[i] = defaultDemoRouting()
	}
	if len(hashes) == 0 {
		return html, routings
	}
	out := html
	i := 0
	for _, tag := range anchorTagRe.FindAllString(html, -1) {
		if i >= len(hashes) || strings.Contains(tag, "data-hash-code=") {
			continue // ran out of exercises, or it's a built-in demo button
		}
		if !strings.Contains(tag, `id="exerciseDemo"`) && !strings.Contains(tag, `class="exerciseDemo"`) {
			continue
		}
		attrs := tagAttrs(tag)
		r := defaultDemoRouting()
		if v := attrs["data-port"]; v != "" {
			r.port = v
		}
		if v := attrs["href"]; v != "" && strings.HasPrefix(v, "/") {
			r.path = v
		}
		if v := attrs["data-term"]; v != "" {
			r.term = v
		}
		r.prefix, r.proto = attrs["data-host-prefix"], attrs["data-protocol"]
		out = strings.Replace(out, autoDemoAnchor(hashes[i]), demoButton(hashes[i], r), 1)
		routings[i] = r
		i++
	}
	return out, routings
}

// tagAttrs pulls the double-quoted attributes out of an opening tag.
func tagAttrs(tag string) map[string]string {
	m := map[string]string{}
	for _, kv := range attrValRe.FindAllStringSubmatch(tag, -1) {
		m[kv[1]] = kv[2]
	}
	return m
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
