package dockershim

import (
	"context"
	"encoding/binary"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// runExec bridges a raw duplex stream (the hijacked Docker exec connection)
// to a Kubernetes pods/exec session over SPDY. This translation — Docker's
// hijacked-TCP raw stream on one side, k8s SPDY exec on the other — is the
// risky unknown the PoC exists to de-risk (see K8S-SANDBOX-DESIGN.md,
// "Exec/attach protocol").
func (s *shim) runExec(ctx context.Context, podName string, cmd []string, tty bool, stream io.ReadWriteCloser) error {
	req := s.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "main",
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    !tty, // combined stream when tty, like Docker's raw framing
			TTY:       tty,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(s.restCfg, "POST", req.URL())
	if err != nil {
		return err
	}

	if tty {
		// Raw passthrough — matches Docker's exec-with-TTY wire format.
		return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:  stream,
			Stdout: stream,
			Stderr: stream,
			Tty:    true,
		})
	}

	// No TTY: Docker's raw-stream protocol multiplexes stdout/stderr with an
	// 8-byte frame header ([stream_type, 0,0,0, big-endian uint32 size]) so
	// the client can demux — without it the CLI silently drops the output
	// (seen while testing `docker exec` without -it: exit code 0, no text).
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stream,
		Stdout: &dockerFrameWriter{stream: 1, w: stream},
		Stderr: &dockerFrameWriter{stream: 2, w: stream},
		Tty:    false,
	})
}

// runAttach bridges a raw duplex stream to the pod's own process 1 via the
// Kubernetes attach subresource — the k8s analogue of `docker attach`,
// distinct from exec (which starts a new process). Requires the pod's
// container to have been created with Stdin/TTY true, which startContainer
// (k8s.go) sets from the same OpenStdin/Tty fields PWD always sends true for
// instance containers (provisioner/dind.go ContainerCreate).
func (s *shim) runAttach(ctx context.Context, podName string, stream io.ReadWriteCloser) error {
	req := s.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: "main",
			Stdin:     true,
			Stdout:    true,
			Stderr:    false,
			TTY:       true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(s.restCfg, "POST", req.URL())
	if err != nil {
		return err
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stream,
		Stdout: stream,
		Stderr: stream,
		Tty:    true,
	})
}

type dockerFrameWriter struct {
	stream byte // 1 = stdout, 2 = stderr
	w      io.Writer
}

func (f *dockerFrameWriter) Write(p []byte) (int, error) {
	header := [8]byte{f.stream, 0, 0, 0}
	binary.BigEndian.PutUint32(header[4:], uint32(len(p)))
	if _, err := f.w.Write(header[:]); err != nil {
		return 0, err
	}
	return f.w.Write(p)
}
