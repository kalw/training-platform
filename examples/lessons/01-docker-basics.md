---
title: Docker basics
image: busybox:1.36
---

# Docker basics

Welcome. This lesson has **no quiz and no exercise** — it's a plain
front-matter lesson. The `image:` in the front matter boots the session
instance you get when you click **Start session** on the right.

## Try the console

The panel on the right is a live terminal into a session Pod, bridged over a
WebSocket to the Kubernetes `pods/exec` API. Once connected, try:

```
echo "hello from a pod in kind"
uname -a
ls /
```

## What just happened

- the page called `POST /api/v1/sessions`, which created a Pod in the
  session namespace and returned its name
- the terminal connected to `/terminals/<pod>` and your keystrokes were
  streamed to the container's shell

That's the whole "play with docker" loop — served by one Go binary running
in Kubernetes.
