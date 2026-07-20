# The lesson page at runtime

What the rendered lesson page does in the learner's browser, and how the
platform backs it. Authoring is covered in
[WRITING-LESSONS.md](../WRITING-LESSONS.md).

## Terminals & session lifecycle

Lesson pages embed [xterm.js](https://xtermjs.org) terminals (vendored, see
below). **Start session** boots one instance Pod per terminal and waits for
Running — pods that can never start (e.g. `ImagePullBackOff`) fail fast with
the reason shown in the panel. The page then:

- bridges each terminal to `/terminals/{pod}` (binary frames = TTY bytes,
  text frames = JSON control, currently `{"type":"resize","cols","rows"}` —
  the server drives the exec TTY size through a `TerminalSizeQueue`);
- stores Pod names in `sessionStorage`, so a reload **reattaches** to the
  running Pods instead of leaking new ones (state inside the Pod survives;
  the shell is new);
- pings `POST /api/v1/sessions/{pod}/keepalive` every minute **while the tab
  is visible**, sliding the Pod's *idle window* (`--session-idle-ttl`,
  default 10m) forward. Close the tab — or leave it hidden — and the pings
  stop, so the server GC reaps the Pods after the idle TTL. The *hard* cap
  (`--session-ttl`, default 4h) is never extended: it bounds total session
  length. Coming back to a hidden tab pings immediately and either resumes
  the session or resets the UI if the Pods were reaped;
- **Stop** deletes the Pods and clears the stored session;
- reconnects the WebSocket (with backoff) while the Pod is still alive.

Sessions API: `POST /api/v1/sessions` (create, returns
`pod`/`ip`/`expires_at`), `GET /api/v1/sessions/{pod}` (phase/ready/reason),
`DELETE /api/v1/sessions/{pod}`, `POST /api/v1/sessions/{pod}/keepalive`.
`GET /api/v1/config` serves runtime page config (`router_host` from
`ROUTER_HOST` / `--router-host`, plus the caller's `user`), keeping pages
build-once/deploy-anywhere.

## Exposed-port routing

When `--router-host` is set, `serve` **also answers for exposed-port hosts
itself**: a request whose Host is `ip<A-B-C-D>-<id>[-port].<router host>` is
proxied straight to that Pod IP (the composed server runs in-cluster, where
Pod IPs are routable). Point a wildcard ingress (`*.<router host>`) at the
same Service and `{:data-port=}` links work with no extra deployment; the
standalone `training router` remains for scaled setups.

## Legacy formatting options (writing-tutorials.md parity)

The renderer supports the authoring contract of the legacy lessons repo
(`training-lessons-ps/writing-tutorials.md`):

| Feature | Status |
|---|---|
| `terms:` front matter (0–6 terminal windows, one instance Pod each, PWD "node" semantics) | ✅ default 1; `0` = no console |
| ` ```.termN ` code blocks (click-to-run in terminal N) | ✅ |
| `[text](/){:data-term=".termN"}{:data-port="XXXX"}` exposed-port links, plus optional `{:data-host-prefix="p"}` and `{:data-protocol="https:"}` | ✅ rewritten live to `[p-]ip<A-B-C-D>-<id>-<port>.<ROUTER_HOST>` once a session is up (same byte layout as the legacy SDK; protocol defaults to the page's, not the SDK's hardcoded `http:`) |
| `SESSION_ID` / `PWD_HOST_FQDN` env inside instances (lesson snippets that echo service URLs) | ✅ injected at Pod creation; note the host is now `ip…-….${PWD_HOST_FQDN}` — `ROUTER_HOST` already includes any `direct.` subdomain |
| `{% quiz %}` / `{% exercise %}` blocks, `exercise_result:` / `exercise_threshold:` | ✅ (same hash contract) |
| Authored exercise demo links — `{:id="exerciseDemo"}` (or `{:class="exerciseDemo"}` with several): Nth marked link ↔ Nth exercise block | ✅ every exercise always renders a **"Test Exercise"** submit button; the marked link *supplies its routing* (adopts `data-port`, href→result-page path, `data-term`, `data-host-prefix`, `data-protocol`) — so the button opens the right result page (often a non-80 port) — while the marked link stays inline as a plain preview. No mark → the button uses the defaults (port 80, `/result.html`) |
| `openConsoleTool('nodeN','editor'/'session')` buttons (PWD file editor / session popup) | ❌ legacy console UI, not ported |
| ssh into an instance (`ssh -p 2223 ip…@direct.…`) | ❌ the k8s engine exposes no sshd |

## Vendored front-end assets

xterm.js, its fit addon and html2canvas are pinned as ordinary npm
dependencies in
[`internal/content/assets/package.json`](../internal/content/assets/package.json)
(+ lockfile) — the standard manager surface, so **Renovate upgrades them like
any npm project** (grouped by [`renovate.json`](../renovate.json)).
`make assets` runs `npm ci` (lockfile-integrity-verified) and copies the dist
files next to the manifest for `go:embed`; the committed copies are enforced
against the pins by a CI check, and the
[`assets-sync`](../.github/workflows/assets-sync.yml) workflow regenerates
them automatically on Renovate's PRs. The files are embedded in the binary
(served at `/assets/`, no CDN at runtime) **and** copied into every
`training build` output so a built site is self-contained. To upgrade
manually: bump the pin, `make assets`, commit both.
