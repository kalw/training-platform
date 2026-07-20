# Scoring — grading, persistence, identity

How quiz and exercise solves are graded, stored, and attributed. Authoring
the challenges themselves is covered in
[WRITING-LESSONS.md](../WRITING-LESSONS.md).

## The contract

```
challenge hash = sha256(question_or_exercise_text + lesson_filename)
quiz flag      = sha256(answer + salt)
exercise flag  = phash$<dHash hex>[:threshold]
```

The quiz/exercise pages only ever confirm that an answer was *submitted* —
never whether it was correct — so the UI can't be used to brute-force the
answer (each choice's salted hash is in the DOM); outcomes live on the
`/scoreboard`. The [Playwright suite](../e2e) proves this, and demonstrates
that the scoring channel *verifies* outcomes but doesn't *prevent* forgery
(a client can POST a correct hash / screenshot directly, bypassing the UI).

## How an exercise is graded

There are two mechanisms, and **which one applies depends on the lesson**:

| | server-side content check | screenshot proof (phash) |
|---|---|---|
| **Turned on by** | `exercise_expect:` / `exercise_expect_regex:` | nothing (the default) |
| **Proves** | exactly what the page **says** | the page's coarse **layout** |
| **Produced by** | the platform, fetching the learner's Pod | the learner's browser |
| **Forgeable by the client** | no | yes |

**Prefer the content check.** A dHash cannot see text: measured on the example
result page, rewriting *every string on it* moves ~1% of the hash bits even at
a 32×32 grid — below the noise floor you must tolerate across renderers. So a
perceptual match proves the service came up and rendered the expected shape,
**not** that it says the right thing. Add an assertion when the content
matters:

```yaml
exercise_expect: "The service is running correctly"   # substring
# or
exercise_expect_regex: "Success|healthy"
```

The platform then fetches the result page **from the learner's own session
Pod** — in-cluster, the same direct Pod-IP routability the port router uses —
and asserts the body. `POST /api/v1/challenges/verify {challenge_hash, pod}`;
the **port and path come from the challenge** (built from the exercise's demo
routing), never from the request, and the pod is checked against the session
engine before dialing, so the endpoint can't be steered at anything but the
caller's own session. Responses are capped and redirects are not followed.
Keep phash for exercises whose proof is genuinely visual, or whose result page
is client-rendered (the server fetch sees raw HTML, it runs no JS).

## The screenshot proof client

The exercise's **"Test Exercise"** button opens the *learner's own* result
page — served by their session Pod on the exercise image's port, reached
through the exposed-port router — with `?hash_code=…&lessonsDomain=…`
appended. That page carries a tiny loader (baked into
`training-exercises-template`, and into the example
[`03-fix-nginx-result.html`](../examples/lessons/03-fix-nginx-result.html))
that pulls **`/js/exercise-verify.js`** from the platform.
`exercise-verify.js` loads the vendored **html2canvas**, screenshots the page
at 1024×768, and POSTs the capture (a JPEG data-URL) to
`/api/v1/challenges/attempt`, which perceptual-hashes it against the
build-time reference and records the solve. Because the result page is a
different origin than the platform, the scoring API answers the CORS
preflight and reflects the origin (with credentials, so an authenticated
solve still attributes). Both `exercise-verify.js` and `html2canvas.min.js`
are embedded in the binary (served at `/js/` and `/assets/`) and copied into
every `training build` output.

## Persistence (durable solves)

Challenges are re-seeded from the build's `challenges.json` at every boot, so
the only state worth keeping is **solves**. A solve is a tiny, append-only,
idempotent fact, so persistence is an **append-only JSON-lines file** rather
than a database:

```sh
training serve --solves-file /data/solves.jsonl     # or SOLVES_FILE=…
```

Each record is fsync'd before the request returns, and the log is replayed at
boot (`scoring: solves persisted to … (recovered N)`). A crash-torn final line
is skipped rather than failing the boot, and the tail is healed on open so the
next append can't merge into it. The file stays readable with `cat`.

**Without it, solves live in memory and are lost on restart** — fine for CI
and content authoring, lossy for a real class. The boot log always states
which mode is active.

On Kubernetes, turn it on in the chart (it provisions a small PVC, annotated
`helm.sh/resource-policy: keep` so `helm uninstall` doesn't bin learner
progress):

```sh
helm upgrade --install training deploy/helm/training-platform \
  --set persistence.enabled=true --set persistence.size=1Gi
```

`/scoreboard` shows the **global standings** (ranking by points, per-challenge
completion) on top of the per-solve list; the same data is at
`GET /api/v1/standings`.

## Who a solve belongs to

With social login configured, solves attribute to the real account. Without
it, every browser is still given its **own random, memorable learner name** —
`clever-marten-077` — kept in a long-lived cookie, so a classroom shows up as
distinct rows instead of collapsing into one `anonymous` entry, and progress
is stable across page loads and platform restarts. The page tells learners
which name they are (`you are …` on `/scoreboard`, `user` in
`GET /api/v1/config`).

This is **identification, not authentication**: the cookie is unsigned, so a
learner could set it to another name — just as they could POST a correct
answer hash directly (the e2e suite demonstrates both). Use social login when
attribution has to be trustworthy. Names coming back from a cookie are
validated against the generated shape, so nothing arbitrary reaches the
shared scoreboard.
