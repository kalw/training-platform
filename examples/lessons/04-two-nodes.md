---
title: Two nodes, two images
image: alpine:3.20
terms: 2
term_images:
  - nginx:alpine
---

# Two nodes, two images

This lesson boots **two different sandboxes**. `term_images:` names them
positionally: `node1` runs `nginx:alpine`, while `node2` is not listed and so
falls back to the lesson's `image:` — `alpine:3.20`. Because the images
differ, each panel is labelled with the one it runs.

## node1 — the server

nginx is already serving on port 80. Confirm which image you are on, and
print the address the client will need:

```.term1
nginx -v
hostname -i
```

You can also open it straight from this page:
[the running server](/){:data-term=".term1"}{:data-port="80"}

## node2 — the client

A plain alpine box: no nginx, just a shell and `wget`. Click to prove it is a
different machine:

```.term2
nginx -v || echo "no nginx here - this is a different image"
```

Now fetch the server. Pod IPs are directly routable inside the cluster, so
node2 can reach node1 by the address you printed above. This block is **not**
click-to-run, because you have to substitute the IP first — copy it, replace
`NODE1_IP`, and paste:

```
wget -qO- http://NODE1_IP | head -4
```

You should get nginx's welcome page HTML, served by a container that does not
exist on the box you typed the command into.

## Why two images

A single-image lesson can only show one side of a system. Two let a lesson
pose the questions that actually come up:

- a server and a client, to teach connectivity and debugging
- a control plane and a worker
- a deliberately broken box next to a known-good one, to compare

Anything `term_images:` does not cover falls back to `image:`, so a lesson
only names the nodes that differ.
