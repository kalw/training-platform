---
title: Fix the broken web server — exercise
image: nginx
# The reference flag is computed at build time by perceptual-hashing the
# expected result. exercise_result can be a rendered image (.png/.jpg —
# hashed directly, no browser, CI-friendly) or an .html page (rendered
# headlessly with Chrome, for dev machines). Here we use the pre-rendered
# image so the build is deterministic and browser-free.
exercise_result: 03-fix-nginx-result.png
exercise_threshold: 20
# Server-side content check — authoritative. The platform fetches the result
# page from your own session Pod and asserts this text. A perceptual hash
# can't see text (rewriting every string on this page moves ~1% of the hash
# bits), so an assertion is what actually pins the content.
exercise_expect: "The service is running correctly"
terms: 2
---

# Fix the broken web server

This is a hands-on **exercise** lesson. Its `image:` front matter boots a
**custom exercise image** built `FROM ghcr.io/kalw/training-exercises-template`
— something pre-installed and deliberately broken for you to repair. You work
inside the session until the result page renders correctly, then submit that
page as proof.

{% exercise %}
The status page is broken. Fix the web server config inside your session so
[webserver](/03-fix-nginx-result.html){:id="exerciseDemo"}{:data-term=".term1"}{:data-port="8888"} renders the green **success** panel, then submit it.
{% endexercise %}

## How it's graded

Every exercise renders a **"Test Exercise"** button (the learner's submit
affordance). The `{:id="exerciseDemo"}` mark on the link above *supplies that
button's routing* (Nth marked link ↔ Nth exercise block, like the legacy
`writing-tutorials.md` contract): the button adopts the mark's `data-port`,
href (result-page path), `data-term`, `data-host-prefix` and `data-protocol`,
and carries the challenge `hash_code`. Here that points the button at the
result page on port `8888` — the port the exercise image serves — instead of
the default (port 80, `/result.html`). The marked `webserver` link itself
stays inline as a plain live preview. Without any mark, the button uses the
defaults.

This lesson sets `exercise_expect:`, so **Test Exercise** is graded
**server-side**: the platform fetches `/03-fix-nginx-result.html` on port
8888 *from your own session Pod* (Pod IPs are directly routable in-cluster,
the same property the port router uses) and asserts the body contains the
expected text. The verdict appears under the button, and the solve shows on
the [`/scoreboard`](/scoreboard).

That check is **exact**, and the browser can't fake it — the page really has
to serve the right thing. Note the target (port 8888 + the result path) is
fixed at build time from this lesson, never taken from the browser, so the
endpoint can't be pointed at anything but your own session.

Without `exercise_expect:`, exercises fall back to **screenshot proof**: the
result page loads `js/exercise-verify.js`, which screenshots it at 1024×768
with html2canvas and posts the capture to `/api/v1/challenges/attempt`, where
a **perceptual hash (dHash)** is compared within a **Hamming distance** of the
build-time reference. That tolerates cross-browser rendering differences —
but it only proves the page's coarse *layout*, not its text, which is exactly
why a content assertion exists.

The reference flag for this exercise (`phash$…:12`) is produced at **build
time** by `training build`: it renders the expected result page
(`exercise_result:` — here `03-fix-nginx-result.html`) with headless Chrome
at 1024×768, dHashes the screenshot, and imports that flag into the
challenge store alongside the quiz answers — one pipeline, one source of
truth.
