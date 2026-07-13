---
title: Fix the broken web server — exercise
image: ghcr.io/kalw/my-broken-nginx:latest
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
[webserver](/){:data-term=".term1"}{:data-port="8080"} renders the green **success** panel, then submit it.
{% endexercise %}

## How it's graded

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
