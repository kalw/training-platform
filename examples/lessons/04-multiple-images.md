---
title: Multiple images
image: alpine:3.20
terms: 4
term_images:
  - nginx:alpine
  - redis:alpine
  - busybox:1.36
---

# Multiple images

One lesson, four sandboxes, **four different images**. `term_images:` maps to
the panels positionally — entry 1 is `node1`, entry 2 is `node2`, and so on:

- `node1` — `nginx:alpine`, a web server
- `node2` — `redis:alpine`, a datastore
- `node3` — `busybox:1.36`, a minimal toolbox
- `node4` — **not listed**, so it falls back to the lesson's `image:`, `alpine:3.20`

You only name the nodes that differ; everything the list doesn't cover falls
back to `image:`. Because this lesson mixes images, every panel is labelled
with the one it runs.

**The limit is 6 terminals per lesson.** It is a hard cap — each panel is a
privileged Pod, per learner — and asking for more fails the build rather than
silently giving you fewer. Listing more `term_images:` than you have `terms:`,
or pointing a `.termN` block at a node the lesson never boots, fails the same
way.

## Each node is a different machine

Click each block and compare what is installed:

```.term1
nginx -v
hostname -i
```

```.term2
redis-server --version
```

```.term3
busybox | head -1
```

```.term4
cat /etc/os-release | head -2
```

Only `node1` has nginx, only `node2` has redis. They are separate containers
from separate images, not four shells on one box.

## They can still talk to each other

Pod IPs are routable between the panels, so the toolbox can reach the server.
Copy the address `node1` printed above, replace `NODE1_IP`, and paste this
into `node3` — it is a plain block, not click-to-run, because you have to
substitute the IP first:

```
wget -qO- http://NODE1_IP | head -4
```

You can also open the server straight from this page:
[the running web server](/){:data-term=".term1"}{:data-port="80"}

## Why this matters

A single-image lesson can only show one side of a system. Several let a lesson
pose the questions that actually come up in practice:

- a server, a datastore and a client, to teach connectivity and debugging
- a control plane and a worker
- a deliberately broken box next to a known-good one, to compare
