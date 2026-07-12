package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kalw/training-platform/internal/scoring"
)

// BuildDir renders every .md/.markdown lesson in srcDir into an HTML page
// under outDir, writes an index.html linking them, and emits
// outDir/challenges.json aggregating every quiz/exercise challenge — the
// single artifact the server seeds scoring from. Returns the number of
// lessons rendered and the aggregated challenges.
//
// This is the whole "build" pipeline: the Markdown→PWD-HTML renderer and the
// challenge importer are the same pass, so the page DOM and the challenge
// store are guaranteed to agree on every hash.
func BuildDir(srcDir, outDir, salt string) (int, []scoring.Challenge, error) {
	// Exercise flags are computed here, at challenge-creation time, by
	// rendering each exercise's result page (headless Chrome) and perceptual-
	// hashing it — never hand-written into the lesson.
	return BuildDirWithResolver(srcDir, outDir, salt, NewPhashResolver(srcDir, ""))
}

// BuildDirWithResolver is BuildDir with an injectable exercise resolver
// (tests use a stub to avoid needing a browser).
func BuildDirWithResolver(srcDir, outDir, salt string, resolver ExerciseResolver) (int, []scoring.Challenge, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, nil, err
	}

	var all []scoring.Challenge
	var index []indexEntry
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".markdown") {
			// Copy non-lesson assets (result pages, images, css) through so
			// they're served alongside the rendered lessons.
			if b, rerr := os.ReadFile(filepath.Join(srcDir, name)); rerr == nil {
				_ = os.WriteFile(filepath.Join(outDir, name), b, 0o644)
			}
			continue
		}
		src, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			return count, all, err
		}
		slug := strings.TrimSuffix(strings.TrimSuffix(name, ".markdown"), ".md")
		lesson, err := Render(slug, src, salt, resolver)
		if err != nil {
			return count, all, fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(outDir, slug+".html"), []byte(lesson.HTML), 0o644); err != nil {
			return count, all, err
		}
		all = append(all, lesson.Challenges...)
		title := lesson.FrontMatter.Title
		if title == "" {
			title = slug
		}
		index = append(index, indexEntry{Slug: slug, Title: title, Challenges: len(lesson.Challenges)})
		count++
	}

	if err := os.WriteFile(filepath.Join(outDir, "index.html"), []byte(renderIndex(index)), 0o644); err != nil {
		return count, all, err
	}
	blob, _ := json.MarshalIndent(all, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "challenges.json"), blob, 0o644); err != nil {
		return count, all, err
	}
	return count, all, nil
}

type indexEntry struct {
	Slug       string
	Title      string
	Challenges int
}

func renderIndex(entries []indexEntry) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>training-platform — lessons</title>` +
		`<style>body{margin:0;font:15px/1.6 -apple-system,Segoe UI,Roboto,sans-serif;background:#0f1117;color:#e6e8ee}` +
		`header{padding:16px 24px;border-bottom:1px solid #262b38}main{max-width:760px;margin:0 auto;padding:24px}` +
		`a{color:#4f8cff;text-decoration:none}li{margin:8px 0}.pill{color:#9aa3b2;font-size:12px}` +
		`header a{font-size:13px;margin-left:12px}</style></head><body>` +
		`<header><strong>training-platform</strong><a href="/scoreboard">scoreboard</a></header><main><h1>Lessons</h1><ul>`)
	for _, e := range entries {
		b.WriteString(fmt.Sprintf(`<li><a href="/%s.html">%s</a> <span class="pill">%d challenge(s)</span></li>`,
			e.Slug, htmlEscape(e.Title), e.Challenges))
	}
	b.WriteString(`</ul></main></body></html>`)
	return b.String()
}
