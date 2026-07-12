// Package dockershim serves a subset of the Docker Engine API backed by a
// Kubernetes cluster: containers become Pods, exec/attach become
// pods/exec and pods/attach. It lets Docker-based course content ("play
// with docker") keep working while the platform itself is deployed only on
// Kubernetes — the learner (or an unmodified Play-With-Docker console)
// points DOCKER_HOST at this server and their `docker` calls land as Pods.
//
// The wire-protocol translation (Docker's hijacked raw stream vs.
// Kubernetes SPDY, the 8-byte stdcopy framing, the 101-UPGRADE handshake)
// was validated end to end against a kind cluster and against a real,
// unmodified PWD console; see training-deployment/K8S-SANDBOX-DESIGN.md.
package dockershim

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/google/uuid"
)

// Handler builds the Docker-Engine-API HTTP handler backed by Kubernetes in
// ns. Mount it on its own listener (see Serve) or compose it into a larger
// server. A non-empty ns overrides the default target namespace.
func Handler(ns string) (http.Handler, error) {
	if ns != "" {
		namespace = ns
	}
	s, err := newShim()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", s.handlePing)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/containers/create", s.handleContainerCreate)
	mux.HandleFunc("/containers/", s.handleContainersPrefix)
	mux.HandleFunc("/exec/", s.handleExecPrefix)
	mux.HandleFunc("/networks/create", s.handleNetworkCreate)
	mux.HandleFunc("/networks/", s.handleNetworksPrefix)

	// Docker CLI negotiates an API version and re-prefixes every request
	// with /vX.YZ/...; strip that prefix so the routes above still match.
	return logRequests(stripVersionPrefix(mux)), nil
}

// Serve runs the shim on its own listener at addr, targeting namespace ns.
// Blocks until the server exits.
func Serve(addr, ns string) error {
	h, err := Handler(ns)
	if err != nil {
		return err
	}
	log.Printf("dockershim listening on %s, targeting namespace %q", addr, namespace)
	return http.ListenAndServe(addr, h)
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

var versionPrefixRe = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

func stripVersionPrefix(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = versionPrefixRe.ReplaceAllString(r.URL.Path, "")
		h.ServeHTTP(w, r)
	})
}

func (s *shim) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Api-Version", "1.44")
	w.Header().Set("OSType", "linux")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *shim) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{
		Version:       "poc",
		ApiVersion:    "1.44",
		MinAPIVersion: "1.24",
		GitCommit:     "poc",
		Os:            "linux",
		Arch:          "amd64",
		BuildTime:     nowRFC3339(),
	})
}

func (s *shim) handleContainerCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req createContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	// PWD always names its instance containers itself (?name=<sessionid8>_<xid>,
	// see provisioner/dind.go) and reuses that exact string for every later
	// call instead of the Id this response returns — so honor ?name= when
	// present, matching real dockerd behavior, rather than always minting our
	// own id.
	id := r.URL.Query().Get("name")
	if id == "" {
		id = "pwd-poc-" + uuid.NewString()[:8]
	}
	s.mu.Lock()
	s.pending[id] = &req
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, createContainerResponse{Id: id})
}

// containerPathRe matches /containers/{id}/{action}; containerBareIDRe
// matches the DELETE /containers/{id} form `docker rm` actually uses (no
// action suffix — this tripped up the first pass, which only handled
// {id}/{action} shapes and 404'd on plain removal).
var containerPathRe = regexp.MustCompile(`^/containers/([^/]+)/([a-z]+)$`)
var containerJSONRe = regexp.MustCompile(`^/containers/([^/]+)/json$`)
var containerBareIDRe = regexp.MustCompile(`^/containers/([^/]+)$`)

func (s *shim) handleContainersPrefix(w http.ResponseWriter, r *http.Request) {
	if m := containerJSONRe.FindStringSubmatch(r.URL.Path); m != nil {
		s.handleContainerInspect(w, r, m[1])
		return
	}
	if m := containerPathRe.FindStringSubmatch(r.URL.Path); m != nil {
		id, action := m[1], m[2]
		switch action {
		case "start":
			s.handleContainerStart(w, r, id)
		case "stop", "kill":
			s.handleContainerStop(w, r, id)
		case "logs":
			s.handleContainerLogs(w, r, id)
		case "exec":
			s.handleExecCreate(w, r, id)
		case "attach":
			s.handleContainerAttach(w, r, id)
		case "resize":
			w.WriteHeader(http.StatusOK) // not implemented; best-effort no-op
		default:
			http.NotFound(w, r)
		}
		return
	}
	if m := containerBareIDRe.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodDelete {
		s.handleContainerRemove(w, r, m[1])
		return
	}
	http.NotFound(w, r)
}

func (s *shim) handleContainerStart(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.startContainer(id); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *shim) handleContainerInspect(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	members := map[string]bool{}
	for k, v := range s.netMembers[id] {
		members[k] = v
	}
	s.mu.Unlock()

	if !s.isManaged(id) {
		// Unmanaged name (e.g. the L2 router container PWD's
		// overlaySessionProvisioner.SessionNew tries to NetworkConnect,
		// without ever creating it through us): answer from netMembers
		// bookkeeping alone so the caller's inspect-after-connect succeeds,
		// rather than 404ing and aborting session creation. IPs here are
		// fabricated and non-routable — see K8S-SANDBOX-DESIGN.md.
		nets := map[string]networkEndpoint{}
		for net := range members {
			nets[net] = networkEndpoint{IPAddress: fakeIPFor(id, net)}
		}
		out := containerJSON{
			Id:              id,
			Name:            "/" + id,
			State:           containerState{Status: "running", Running: true},
			NetworkSettings: networkSettings{Networks: nets},
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	pod, err := s.waitForIP(ctx, id)
	if err != nil {
		if apierrors.IsNotFound(err) {
			httpError(w, http.StatusNotFound, err)
			return
		}
		httpError(w, http.StatusInternalServerError, err)
		return
	}

	nets := map[string]networkEndpoint{}
	for net := range members {
		nets[net] = networkEndpoint{IPAddress: pod.Status.PodIP}
	}

	running := pod.Status.Phase == "Running"
	out := containerJSON{
		Id:      id,
		Created: pod.CreationTimestamp.UTC().Format(time.RFC3339Nano),
		Name:    "/" + id,
		Image:   pod.Spec.Containers[0].Image,
		State: containerState{
			Status:  string(pod.Status.Phase),
			Running: running,
		},
		NetworkSettings: networkSettings{IPAddress: pod.Status.PodIP, Networks: nets},
	}
	out.Config.Image = pod.Spec.Containers[0].Image
	out.Config.Tty = pod.Spec.Containers[0].TTY
	writeJSON(w, http.StatusOK, out)
}

func (s *shim) handleContainerStop(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.deletePod(ctx, id); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *shim) handleContainerRemove(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.deletePod(ctx, id); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	delete(s.pending, id)
	delete(s.created, id)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *shim) handleContainerLogs(w http.ResponseWriter, r *http.Request, id string) {
	follow := r.URL.Query().Get("follow") == "1" || r.URL.Query().Get("follow") == "true"

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	pod, err := s.getPod(ctx, id)
	cancel()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	tty := len(pod.Spec.Containers) > 0 && pod.Spec.Containers[0].TTY

	req := s.clientset.CoreV1().Pods(namespace).GetLogs(s.k8sPodName(id), &corev1.PodLogOptions{Follow: follow})
	rc, err := req.Stream(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	// Same non-TTY multiplexing requirement as exec (see dockerFrameWriter
	// in exec.go): the docker CLI's stdcopy demuxer errors on raw bytes
	// ("unrecognized stream: <byte>") unless the container was created
	// without a TTY, in which case it expects framed output. Kubernetes
	// doesn't split container stdout/stderr, so everything is framed as
	// stdout (stream type 1).
	var dst io.Writer = w
	if !tty {
		dst = &dockerFrameWriter{stream: 1, w: w}
	}

	buf := make([]byte, 4096)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *shim) handleExecCreate(w http.ResponseWriter, r *http.Request, containerID string) {
	var req execCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	execID := uuid.NewString()
	s.mu.Lock()
	s.execs[execID] = &execEntry{containerID: containerID, cmd: req.Cmd, tty: req.Tty}
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, execCreateResponse{Id: execID})
}

func (s *shim) handleExecPrefix(w http.ResponseWriter, r *http.Request) {
	m := regexp.MustCompile(`^/exec/([^/]+)/([a-z]+)$`).FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	execID, action := m[1], m[2]
	switch action {
	case "json":
		s.handleExecInspect(w, r, execID)
	case "start":
		s.handleExecStart(w, r, execID)
	case "resize":
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (s *shim) handleExecInspect(w http.ResponseWriter, r *http.Request, execID string) {
	s.mu.Lock()
	_, ok := s.execs[execID]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("no such exec"))
		return
	}
	writeJSON(w, http.StatusOK, execInspectResponse{ID: execID, Running: false, ExitCode: 0})
}

// handleExecStart hijacks the HTTP connection, matching Docker's
// exec-start-with-TTY semantics (a raw duplex byte stream, no framing),
// then bridges it into the Kubernetes exec session opened in exec.go.
func (s *shim) handleExecStart(w http.ResponseWriter, r *http.Request, execID string) {
	var req execStartRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	entry, ok := s.execs[execID]
	s.mu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("no such exec"))
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("connection does not support hijacking"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	defer conn.Close()

	// Modern docker clients send Connection: Upgrade / Upgrade: tcp on exec
	// start and expect a 101 back, not a plain 200 — matching dockerd's own
	// hijack response here, not just "some 2xx".
	_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	_ = buf.Flush()

	stream := &hijackedStream{conn: conn, rw: buf}
	tty := req.Tty || entry.tty
	// Use a fresh context, not r.Context(): after Hijack the original
	// request's context is no longer a reliable signal for the stream's
	// lifetime and can already be canceled by the time we get here.
	podName := s.k8sPodName(entry.containerID)
	if err := s.runExec(context.Background(), podName, entry.cmd, tty, stream); err != nil {
		log.Printf("exec %s: %v", execID, err)
	}
}

// hijackedStream adapts the hijacked net.Conn + bufio.ReadWriter pair to
// io.ReadWriteCloser for remotecommand. Writes must be flushed individually:
// bufio.Writer buffers silently, and the conn is typically closed (by our
// own defer, once the remote command exits) before a byte count large
// enough to trigger an automatic flush ever accumulates — seen as "exec
// exits 0 but the client receives no output" while testing this PoC.
type hijackedStream struct {
	conn net.Conn
	rw   *bufio.ReadWriter
}

func (h *hijackedStream) Read(p []byte) (int, error) { return h.rw.Read(p) }
func (h *hijackedStream) Write(p []byte) (int, error) {
	n, err := h.rw.Write(p)
	if err != nil {
		return n, err
	}
	return n, h.rw.Flush()
}
func (h *hijackedStream) Close() error { return h.conn.Close() }

// handleNetworkCreate answers PWD's per-session dtypes.NetworkCreate call
// (overlaySessionProvisioner.SessionNew). No real Kubernetes object is
// created — see the netMembers doc comment in k8s.go for why a no-op here
// is enough to unblock session creation.
func (s *shim) handleNetworkCreate(w http.ResponseWriter, r *http.Request) {
	var req networkCreateRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	writeJSON(w, http.StatusCreated, networkCreateResponse{Id: req.Name})
}

var networkConnectRe = regexp.MustCompile(`^/networks/([^/]+)/(connect|disconnect)$`)
var networkBareIDRe = regexp.MustCompile(`^/networks/([^/]+)$`)

func (s *shim) handleNetworksPrefix(w http.ResponseWriter, r *http.Request) {
	if m := networkConnectRe.FindStringSubmatch(r.URL.Path); m != nil {
		networkName, action := m[1], m[2]
		if action == "connect" {
			var req networkConnectRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			s.networkConnect(req.Container, networkName)
		}
		// disconnect: no-op success: nothing to undo, see NetworkCreate.
		w.WriteHeader(http.StatusOK)
		return
	}
	if m := networkBareIDRe.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodDelete {
		_ = m
		w.WriteHeader(http.StatusNoContent) // no-op: no real network object exists
		return
	}
	http.NotFound(w, r)
}

// handleContainerAttach backs PWD's InstanceGetTerminal → CreateAttachConnection
// path (docker/docker.go) — the actual browser terminal, called directly by
// the pwd process over DOCKER_HOST, not routed through the L2 router. PWD's
// instance containers are always created with Tty:true (see
// provisioner/dind.go ContainerCreate), so this is always the raw-passthrough
// case, same wire format as exec's TTY branch.
func (s *shim) handleContainerAttach(w http.ResponseWriter, r *http.Request, id string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("connection does not support hijacking"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	defer conn.Close()

	_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	_ = buf.Flush()

	stream := &hijackedStream{conn: conn, rw: buf}
	podName := s.k8sPodName(id)
	if err := s.runAttach(context.Background(), podName, stream); err != nil {
		log.Printf("attach %s: %v", id, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"message": err.Error()})
}
