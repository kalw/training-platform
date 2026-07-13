// Package terminal bridges a browser WebSocket to a session instance's shell
// via the Kubernetes pods/exec subresource (SPDY). It's the in-browser
// terminal: keystrokes in from the socket, program output back out.
//
// This is the same SPDY-exec path proven in the shim work, minus the Docker
// wire-protocol translation — a native WebSocket client needs no stdcopy
// framing, so bytes flow raw in both directions.
package terminal

import (
	"net/http"

	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Bridge connects browser terminals to instance Pods in one namespace.
type Bridge struct {
	cfg       *rest.Config
	cs        kubernetes.Interface
	namespace string
	container string
	upgrader  websocket.Upgrader
}

// New returns a Bridge that execs into the named container of Pods in ns.
// allowOrigin decides which Origins may open a socket (nil = allow all,
// suitable behind an authenticating proxy / for local dev).
func New(cfg *rest.Config, ns, container string, allowOrigin func(*http.Request) bool) (*Bridge, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	if container == "" {
		container = "instance"
	}
	if allowOrigin == nil {
		allowOrigin = func(*http.Request) bool { return true }
	}
	return &Bridge{
		cfg:       cfg,
		cs:        cs,
		namespace: ns,
		container: container,
		upgrader:  websocket.Upgrader{CheckOrigin: allowOrigin},
	}, nil
}

// Attach upgrades the request to a WebSocket and runs an interactive shell
// inside pod, streaming it both ways until either side closes.
func (b *Bridge) Attach(w http.ResponseWriter, r *http.Request, pod string) error {
	ws, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	req := b.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(b.namespace).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: b.container,
			Command:   []string{"/bin/sh", "-c", "exec $(command -v bash || command -v sh)"},
			Stdin:     true,
			Stdout:    true,
			Stderr:    false, // combined stream under a TTY
			TTY:       true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(b.cfg, "POST", req.URL())
	if err != nil {
		return err
	}
	stream := newWSStream(ws)
	return exec.StreamWithContext(r.Context(), remotecommand.StreamOptions{
		Stdin:             stream,
		Stdout:            stream,
		Stderr:            stream,
		Tty:               true,
		TerminalSizeQueue: stream, // browser resize control frames -> TTY size
	})
}
