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
//
// Two legacy authoring features from writing-tutorials.md are supported:
//
//   - ```.termN fenced blocks render as click-to-run code wired to terminal
//     N ("auto populating code in the terminal");
//   - kramdown-style inline attribute lists on links —
//     [text](url){:data-term=".term2"}{:data-port="8080"} — carry through as
//     data-*/id/class attributes, which the page script rewrites into live
//     exposed-port URLs once a session is up.
func renderMarkdown(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "```"):
			info := strings.TrimSpace(strings.TrimPrefix(l, "```"))
			var buf []string
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
				buf = append(buf, html.EscapeString(lines[i]))
				i++
			}
			i++ // consume closing fence
			code := strings.Join(buf, "\n")
			if m := termInfoRe.FindStringSubmatch(info); m != nil {
				out = append(out, fmt.Sprintf(
					`<pre class="term-code" data-term="%s" title="click to run in terminal %s"><code>%s</code></pre>`,
					m[1], m[1], code))
			} else {
				out = append(out, "<pre><code>"+code+"</code></pre>")
			}
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
	headingRe  = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	listRe     = regexp.MustCompile(`^[-*]\s+(.*)$`)
	codeRe     = regexp.MustCompile("`([^`]+)`")
	boldRe     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	termInfoRe = regexp.MustCompile(`^\.term([1-6])$`)
	// linkRe optionally captures a run of kramdown inline attribute lists
	// after the link. It runs on HTML-escaped text, hence &#34; for quotes.
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)((?:\{:[^{}]*\})*)`)
	ialAttrRe = regexp.MustCompile(`\{:\s*([a-zA-Z][a-zA-Z0-9-]*)=&#34;([^&{}]*)&#34;\s*\}`)
)

func inline(s string) string {
	s = codeRe.ReplaceAllString(s, "<code>$1</code>")
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := linkRe.FindStringSubmatch(m)
		return fmt.Sprintf(`<a href="%s"%s>%s</a>`, g[2], ialAttrs(g[3]), g[1])
	})
	return s
}

// ialAttrs converts kramdown inline attribute lists to HTML attributes,
// allowing only inert names (id, class, data-*) — never event handlers.
func ialAttrs(ials string) string {
	var b strings.Builder
	for _, m := range ialAttrRe.FindAllStringSubmatch(ials, -1) {
		name, val := m[1], m[2]
		if name != "id" && name != "class" && !strings.HasPrefix(name, "data-") {
			continue
		}
		fmt.Fprintf(&b, ` %s="%s"`, name, val)
	}
	return b.String()
}
