# Writing lessons

The authoring reference for this platform — the successor to
`training-lessons-ps/writing-tutorials.md`, which documented the Jekyll +
CTFd stack. Everything below is what **this binary** implements; where it
differs from the legacy contract, that is called out.

Lessons are Markdown files with YAML front matter. `training build` renders
each one to a standalone HTML page and, in the **same pass**, imports the
quizzes/exercises it declares into `challenges.json` — so a page and its
challenges can never disagree on a hash.

```sh
training build --src examples/lessons --out site --salt "$CTFD_SALT"
```

See [`examples/lessons`](examples/lessons) for a plain, a quiz and an
exercise lesson. Those examples are deliberately the only content in this
repo; course material lives elsewhere.

## Front matter

```yaml
---
title: Fix the broken web server        # page title
image: nginx                            # image the session instance boots
terms: 2                                # terminal panels, 0–6 (default 1)
exercise_expect: "running correctly"    # server-side content check (see below)
exercise_result: result.png             # phash reference (fallback grading)
exercise_threshold: 20                  # Hamming threshold for the phash
---
```

| key | meaning |
|---|---|
| `title` | page title; falls back to the slug |
| `image` | the container image each session instance runs (a DinD image, or a custom exercise image) |
| `terms` | number of terminal panels, **0–6**, one session Pod each. `0` renders a lesson with no console at all. Default 1. |
| `exercise_expect` / `exercise_expect_regex` | assert the learner's result page **content**, graded server-side. Authoritative when set. |
| `exercise_result` | reference page/image for perceptual-hash grading (`.html` rendered headlessly, `.png`/`.jpg` hashed directly) |
| `exercise_threshold` | Hamming distance allowed for the phash (default 12 of 64) |
| `exercise_phash` | explicit `phash$<hex>[:threshold]` override |

Front matter is **not** part of the hash recipe (only question text +
filename), so editing these keys never invalidates existing challenges.

## Markdown support — read this before writing

The renderer is a deliberately **small, predictable subset**, not
CommonMark/kramdown. It supports:

- ATX headings `#`–`####`
- fenced code blocks
- unordered lists (`-`, `*`)
- `**bold**`, `` `inline code` ``
- links, including kramdown inline attribute lists (below)
- paragraphs, and raw HTML lines passed through untouched

**Not supported today** (they render literally, which is usually not what you
want): ordered lists (`1.`), images (`![]()`), tables, blockquotes, nested
lists, italics, headings deeper than `####`, and Liquid tags other than the
`{% quiz %}` / `{% exercise %}` blocks below.

That is the main authoring gap versus the legacy Jekyll site — see
[`MIGRATION.md`](MIGRATION.md). Until it is closed, either keep lessons within
the subset or drop to a raw HTML block (a line starting with `<` is passed
through verbatim).

## Terminals

`terms:` gives the page N terminal panels, `node1`…`nodeN`, one session Pod
each — the legacy "PWD node" model. Panels are real
[xterm.js](https://xtermjs.org) terminals bridged to `pods/exec`, with TTY
resize wired through.

### Click-to-run code blocks

A fence tagged `.termN` becomes a clickable block that types itself into
terminal N:

````
```.term1
echo "hello from a pod"
uname -a
```
````

A plain fence (or a language tag like ```` ```sh ````) stays an ordinary
non-clickable code block — use that for content the learner should copy into
a file rather than run.

### Linking to a service running in a session

Any port a learner's session exposes can be linked with kramdown inline
attribute lists:

```
[webserver](/){:data-term=".term1"}{:data-port="8080"}
```

Once a session is up these rewrite to the exposed-port router's host
encoding, `ip<A-B-C-D>-<id>-<port>.<ROUTER_HOST>`, and open the service.
Before a session exists the click is swallowed with a hint.

| attribute | effect |
|---|---|
| `{:data-term=".termN"}` | which node's Pod to route to (default `.term1`) |
| `{:data-port="8080"}` | the port inside the session |
| `{:data-host-prefix="app"}` | prepends `app-` to the hostname |
| `{:data-protocol="https:"}` | overrides the scheme (defaults to the page's) |

Only `id`, `class` and `data-*` attributes are honoured — event handlers are
stripped.

Inside a session, `SESSION_ID` and `PWD_HOST_FQDN` are exported, so legacy
snippets that build a URL by hand still work:

````
```.term1
echo "http://ip$(hostname -i | sed 's/\./-/g')-${SESSION_ID}-8080.${PWD_HOST_FQDN}"
```
````

**Not ported from the legacy console:** `openConsoleTool(...)` (the file
editor / session popups) and the ssh gateway.

## Quizzes

```
{% quiz %}
Which command lists the running containers?
- [x] docker ps
- [ ] docker ls
{% endquiz %}
```

The block body is the question, then `- [x]` / `- [ ]` choices; correct ones
become flags. Each choice's **salted hash** is baked into the DOM, never the
plaintext answer, and grading happens server-side.

The page only ever confirms an answer was *submitted* — never whether it was
right. Every choice's hash is in the DOM, so revealing the verdict would let a
learner brute-force it. Outcomes live on `/scoreboard`.

## Exercises

```
{% exercise %}
The status page is broken. Fix the web server so
[webserver](/result.html){:id="exerciseDemo"}{:data-port="8888"} renders the
green success panel, then submit it.
{% endexercise %}
```

Every exercise renders a **"Test Exercise"** button. A link marked
`{:id="exerciseDemo"}` (or `{:class="exerciseDemo"}` when a lesson has
several — Nth marked link ↔ Nth exercise block) *supplies that button's
routing*: the button adopts its `data-port`, href (result-page path),
`data-term`, `data-host-prefix` and `data-protocol`. The marked link itself
stays inline as a plain preview. With no mark the button uses the defaults
(port 80, `/result.html`).

This matters because exercise images usually serve their result page on a
non-80 port — the mark is how the button learns it.

### Grading: pick the right mechanism

|  | content check | screenshot proof (phash) |
|---|---|---|
| **turned on by** | `exercise_expect:` / `exercise_expect_regex:` | nothing (default) |
| **proves** | exactly what the page **says** | the page's coarse **layout** |
| **produced by** | the platform, fetching the learner's Pod | the learner's browser |
| **forgeable by the client** | no | yes |

**Prefer the content check when the text matters.** A perceptual hash cannot
see text: rewriting every string on a result page moves the hash ~1 bit of 64,
and ~1% of bits even at a 32×32 grid — below the noise you must tolerate
across browsers. No threshold separates those, so a phash match proves the
service *came up and rendered the expected shape*, not that it says the right
thing.

```yaml
exercise_expect: "The service is running correctly"
# or
exercise_expect_regex: "Success|healthy"
```

The platform then fetches the result page **from the learner's own session
Pod** and asserts the body. The fetch target (port + path) comes from the
exercise's demo routing, fixed at build time — never from the browser — and
the Pod is validated as one of the platform's own instances before it is
dialled.

Keep the phash path for results that are genuinely **visual**, or whose page
is **client-rendered**: the server-side fetch reads raw HTML and runs no
JavaScript. For that path the result page must load the verify client:

```html
<script>
  (function () {
    var d = new URLSearchParams(location.search).get('lessonsDomain');
    if (!d) return;
    var s = document.createElement('script');
    s.src = d.replace(/\/+$/, '') + '/js/exercise-verify.js';
    document.head.appendChild(s);
  })();
</script>
```

It screenshots the page at 1024×768 with html2canvas and posts the capture to
`/api/v1/challenges/attempt`. Exercise images built `FROM`
`training-exercises-template` inherit this loader.

## The hash contract

```
challenge hash = sha256(question_or_exercise_text + lesson_filename)
quiz flag      = sha256(answer + salt)
exercise flag  = phash$<dHash hex>[:threshold]
```

Rendering and challenge import happen in one pass in one binary, so unlike the
legacy stack (where the recipe was duplicated across Ruby, bash and Python)
there is nothing to keep in sync. Changing a question's **text** or its
**filename** changes its hash and therefore creates a new challenge —
existing solves stay attached to the old one.
