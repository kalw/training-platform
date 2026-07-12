package content

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// renderMarkdown converts a small, predictable Markdown subset to HTML:
// ATX headings, fenced code blocks, unordered lists, bold, inline code, and
// paragraphs. It is intentionally minimal — lesson content is authored in
// this repo, not arbitrary user input — and mirrors the client-side renderer
// used in the demo so pages look identical however they are produced.
func renderMarkdown(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "```"):
			var buf []string
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
				buf = append(buf, html.EscapeString(lines[i]))
				i++
			}
			i++ // consume closing fence
			out = append(out, "<pre><code>"+strings.Join(buf, "\n")+"</code></pre>")
		case headingRe.MatchString(l):
			m := headingRe.FindStringSubmatch(l)
			n := len(m[1])
			out = append(out, fmt.Sprintf("<h%d>%s</h%d>", n, inline(html.EscapeString(m[2])), n))
			i++
		case listRe.MatchString(l):
			var items []string
			for i < len(lines) && listRe.MatchString(lines[i]) {
				items = append(items, "<li>"+inline(html.EscapeString(listRe.FindStringSubmatch(lines[i])[1]))+"</li>")
				i++
			}
			out = append(out, "<ul>"+strings.Join(items, "")+"</ul>")
		case strings.TrimSpace(l) == "":
			i++
		case strings.HasPrefix(strings.TrimSpace(l), "<"):
			// Pass through raw HTML lines (e.g. injected block placeholders).
			out = append(out, l)
			i++
		default:
			out = append(out, "<p>"+inline(html.EscapeString(l))+"</p>")
			i++
		}
	}
	return strings.Join(out, "\n")
}

var (
	headingRe = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	listRe    = regexp.MustCompile(`^[-*]\s+(.*)$`)
	codeRe    = regexp.MustCompile("`([^`]+)`")
	boldRe    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func inline(s string) string {
	s = codeRe.ReplaceAllString(s, "<code>$1</code>")
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = linkRe.ReplaceAllString(s, `<a href="$2">$1</a>`)
	return s
}
