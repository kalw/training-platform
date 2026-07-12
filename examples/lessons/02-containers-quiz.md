---
title: Containers — quiz
image: busybox:1.36
---

# Listing containers

A quick check. The quiz below is graded **server-side by hash**: the page
source contains only salted hashes of each choice, never the plaintext
answer, and the verdict comes from the server.

{% quiz %}
Which command lists the running containers?
- [x] docker ps
- [ ] docker ls
- [ ] docker containers
{% endquiz %}

Pick an answer and hit **Submit** — the selected choice's salted hash is
posted to `/api/v1/challenges/attempt` and compared against the challenge's
flag on the server.
