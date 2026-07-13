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

Clicking **Test Exercise** opens the exercise's result page carrying a
`hash_code`. The verify script screenshots that page at 1024×768 and submits
the capture to `/api/v1/challenges/attempt`. The server computes a
**perceptual hash (dHash)** of your capture and accepts it when it's within a
small **Hamming distance** of the reference — so identical-looking pages pass
even though no two browser screenshots are ever byte-identical.

The reference flag for this exercise (`phash$…:12`) is produced at **build
time** by `training build`: it renders the expected result page
(`exercise_result:` — here `03-fix-nginx-result.html`) with headless Chrome
at 1024×768, dHashes the screenshot, and imports that flag into the
challenge store alongside the quiz answers — one pipeline, one source of
truth.
