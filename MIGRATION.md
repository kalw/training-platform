# Migration status — consolidating the six repos

This repo is the successor to a six-repo, three-language platform. This file
records **what has actually been replaced, what has not, and what must be
true before each old repo can be retired** — plus the operational context
that used to live in those repos.

Read with [`DESIGN.md`](DESIGN.md) (why one binary),
[`WRITING-LESSONS.md`](WRITING-LESSONS.md) (authoring) and
[`K8S-SANDBOX-DESIGN.md`](K8S-SANDBOX-DESIGN.md) (the shim/router
experiments this is built on).

## Retirement matrix

| repo | replaced by | status |
|---|---|---|
| `training-console-pwd` | `internal/session`, `internal/terminal`, `internal/dockershim`, `internal/router` | ✅ **retirable** |
| `training-console-pwd-sdk` | the rendered lesson page's own JS (`internal/content/layout.go`) | ✅ **retirable** |
| `training-ctfd` | `internal/scoring` | ✅ **retirable** — solves are durable; users/teams/admin declared out of scope |
| `training-lessons-ps` | `internal/content` | ⚠️ **not yet** — renderer is a subset; site structure not ported |
| `training-exercise-template` | *nothing — still required* | ⛔ **keep** |
| `training-deployment` | `deploy/helm/training-platform` + `make dev-*` | 🔸 **scope decision** |

### ✅ Ready to retire

**`training-console-pwd` / `-sdk`.** The session engine (Namespaces + Pods,
TTL/idle GC), terminals (WebSocket ⇄ `pods/exec`, TTY resize), the
exposed-port router (host encoding kept byte-compatible) and the Docker
Engine API shim are all here and exercised on kind. The SDK's *behavioural
contract* — `terms:` node panels, `.termN` click-to-run, `{:data-port=}`
links, reconnect, close — is reimplemented against this binary's endpoints;
its code is not vendored (it is frozen on xterm 2.9.2 / webpack 2 / Node 8).

Deliberately **not** ported: `openConsoleTool(...)` popups (file editor,
session panel), the ssh gateway, Windows instances. Retiring loses those.

### ✅ `training-ctfd` — replaced

What is replaced: the challenge-hash lookup, the attempt endpoint, phash
exercise grading, server-side content verification, the scoreboard, and —
since the durable solve log landed — **persistence**.

**Solves survive restarts** without running a database. They are the only
state that cannot be recomputed (challenges are re-seeded from the build's
`challenges.json` at every boot), and a solve is a tiny, append-only,
idempotent fact — so the store is an **append-only JSON-lines file**
(`--solves-file`, `persistence.*` in the chart), fsync'd per record and
replayed at boot. A crash-torn final line is skipped rather than failing the
boot, and the tail is healed on open so the next append can't merge into it.
`cat` is a valid debugging tool.

**Deliberately not replaced — declared out of scope:** users, teams, an admin
UI and challenge management. Identity is social login (GitHub/Google) or, with
none configured, a per-browser random learner name (`clever-marten-077`) in a
long-lived cookie — enough to keep a classroom's rows distinct on the
scoreboard without running accounts. It identifies, it does not authenticate.
Challenges are immutable, seeded from the build. The global
**standings** view (`/api/v1/standings`, rendered on `/scoreboard`) covers
the reporting need that the CTFd scoreboard served. Revisit only if
challenge management is actually wanted.

### ⚠️ `training-lessons-ps` — blocked on renderer fidelity

The Markdown renderer is an intentional subset. Measured against the 88
existing posts, it cannot faithfully render:

| feature | posts using it |
|---|---|
| images `![]()` | 34 |
| ordered lists `1.` | 25 |
| tables | 7 |
| Liquid (`include`/`highlight`/`raw`) | 7 |

Also not ported: the site structure — 8 layouts, 8 includes, the *parcours*
(learning-path) pages, à-la-carte index, tags and categories, feed. This
platform renders a flat list of lessons.

**Content is explicitly out of scope for this repo** (only the living
`examples/lessons` belong here), so the question is not "migrate the 88
posts" but "where does course content live, and can it be authored within the
subset?" Ordered lists and images are basic enough that they should be added
before authoring at volume.

### ⛔ `training-exercise-template` — keep

This platform *consumes* exercise images; it does not build them. The
template is the base image exercises are built `FROM`, and it ships the
result-page loader contract. Retiring it removes the ability to author new
hands-on exercises. (This repo now serves its own `/js/exercise-verify.js`,
so the template's copy of the verify script can be dropped in favour of the
loader stub — but the image itself is still needed.)

### 🔸 `training-deployment` — depends on whether k8s-only is settled

This repo ships its own Helm chart and a `make dev-*` loop, so the Kubernetes
path is covered. `training-deployment` additionally carries the **Ansible
role, molecule tests and haproxy** host-deployment path. If "Kubernetes-only"
is final, retiring it is intentional scope reduction, not a gap.

Its two design documents are the institutional memory and have been **moved
here**: `K8S-SANDBOX-DESIGN.md` (in this repo) and the architecture narrative
folded into `DESIGN.md`. `HIGH-LEVEL-DESIGN.md` still describes the legacy
six-repo topology and should be treated as history.

## Before retiring anything

1. ~~Make the scoring store durable~~ — **done**: append-only solve log,
   enabled with `persistence.enabled=true` (the chart provisions a small PVC).
   Note it is **off by default**, so a deployment that wants durable solves
   must turn it on.
2. ~~Decide the identity/teams story~~ — **out of scope** by decision; the
   global standings view covers reporting.
3. Add ordered lists + images to the renderer (blocks authoring at volume).
4. Confirm the k8s-only decision (decides `training-deployment`).
5. Archive rather than delete — the 88 posts and the Ansible role are the
   only copies of that work.

## Operational context worth keeping

- **`CTFD_SALT`** is a build-time secret: it hashes quiz answers into both the
  page DOM and `challenges.json`. The same salt must be used by `build` and
  `serve`, or nothing grades. CI passes `insecure-default-salt` for PRs.
- **ghcr packages default to private** on first push — flip them public by
  hand in the GitHub UI (Package settings → Danger Zone). The dialog is
  fragile under browser automation; do it manually.
- **Everything is configured at container start**, not build time. Changing a
  URL/domain means restarting the container, not rebuilding the image.
- **`ROUTER_HOST`** drives the exposed-port links. Unset, `{:data-port=}`
  links render inert with an explanatory title. The dev values use
  `direct.127.0.0.1.sslip.io:8080` so links work through `make dev-forward`.
- **RBAC**: the session keepalive slides a Pod label, so the chart's Role
  needs `pods: patch`. Without it keepalives 403 and the idle GC reaps Pods
  that are actively in use.
- **Local loop**: `make dev-deploy` (build + kind load + lessons ConfigMap +
  helm upgrade), `dev-lessons` (re-render content only), `dev-forward`,
  `dev-down`. Requires the kind cluster's control-plane container to be
  running — after a Docker restart, `docker start <cluster>-control-plane`.
- **Solves are in-memory unless `persistence.enabled=true`** (or
  `--solves-file` / `SOLVES_FILE` outside the chart). The boot log says which
  mode is active — "solves are IN-MEMORY only" vs "solves persisted to
  <path> (recovered N)". The PVC carries `helm.sh/resource-policy: keep` so
  `helm uninstall` does not bin learner progress.
- **Vendored front-end assets** (xterm, html2canvas) are pinned as npm
  dependencies in `internal/content/assets/package.json` and refreshed with
  `make assets`; CI enforces the committed copies match the lockfile, and
  Renovate opens the bumps.

## Known limitations carried forward

- **A perceptual hash proves layout, not content** — see `WRITING-LESSONS.md`.
  Use `exercise_expect:` when the page's text matters.
- **The scoring channel verifies outcomes but does not prevent forgery**: a
  client can POST a correct quiz hash or a screenshot directly, bypassing the
  UI (the Playwright suite demonstrates this deliberately). The server-side
  content check is the one grading path a client cannot fake.
- **The server-side content check runs no JavaScript** — it reads raw HTML, so
  client-rendered result pages still need the phash path.
