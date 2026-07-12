package dockershim

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// namespace is the single Kubernetes namespace the shim materializes
// containers into. Overridable via Handler/Serve. One container == one Pod;
// no networks/volumes/build (see K8S-SANDBOX-DESIGN.md for what's left out).
var namespace = "training-sessions"

type shim struct {
	clientset *kubernetes.Clientset
	restCfg   *rest.Config

	mu      sync.Mutex
	pending map[string]*createContainerRequest // docker name -> spec, before /start
	created map[string]bool                    // docker name -> pod created in k8s
	podName map[string]string                  // docker name -> sanitized k8s Pod name
	execs   map[string]*execEntry              // exec id -> target container docker name

	// netMembers tracks which "networks" a docker name has been
	// NetworkConnect'd to — including names PWD never asked us to create a
	// Pod for (the L2 router container: overlaySessionProvisioner.SessionNew
	// connects it to every new session network). Kept so /containers/{id}/json
	// answers ContainerIPs() with the network-name-keyed map PWD expects,
	// for managed pods and unmanaged names alike. See K8S-SANDBOX-DESIGN.md's
	// updated Gaps section for what this does and doesn't achieve for the
	// unmanaged (L2) case: it makes the API call succeed, not the routing.
	netMembers map[string]map[string]bool
}

type execEntry struct {
	containerID string
	cmd         []string
	tty         bool
}

func newShim() (*shim, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename())
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building clientset: %w", err)
	}
	s := &shim{
		clientset:  cs,
		restCfg:    cfg,
		pending:    map[string]*createContainerRequest{},
		created:    map[string]bool{},
		podName:    map[string]string{},
		execs:      map[string]*execEntry{},
		netMembers: map[string]map[string]bool{},
	}
	if err := s.ensureNamespace(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *shim) ensureNamespace() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = s.clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}, metav1.CreateOptions{})
		return err
	}
	return err
}

// k8sPodName maps a Docker-chosen container name to a valid Kubernetes Pod
// name and caches the mapping. PWD names instance containers
// "<sessionID[:8]>_<xid>" (see provisioner/dind.go) and reuses that exact
// string, underscore included, as the identifier for every later Docker API
// call — invalid as a k8s object name (DNS-1123: lowercase alphanumeric and
// '-' only). Translating here, once, keeps every other handler working with
// the original Docker name from the URL, matching what PWD actually sends.
func (s *shim) k8sPodName(dockerName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.podName[dockerName]; ok {
		return n
	}
	n := sanitizeK8sName(dockerName)
	s.podName[dockerName] = n
	return n
}

func sanitizeK8sName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "c"
	}
	if len(out) > 63 {
		sum := sha1.Sum([]byte(name))
		suffix := "-" + hex.EncodeToString(sum[:])[:8]
		out = out[:63-len(suffix)] + suffix
	}
	return out
}

// startContainer materializes the pending spec as a Pod — this is where
// Docker's create/start split maps onto Kubernetes' single-step Pod create.
func (s *shim) startContainer(dockerName string) error {
	s.mu.Lock()
	spec, ok := s.pending[dockerName]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("no such container: %s", dockerName)
	}
	podName := s.k8sPodName(dockerName)

	cmd := spec.Entrypoint
	cmd = append(append([]string{}, cmd...), spec.Cmd...)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "docker-k8s-shim-poc",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: spec.Image,
					Command: func() []string {
						if len(cmd) == 0 {
							return nil
						}
						return cmd
					}(),
					TTY:   spec.Tty,
					Stdin: spec.OpenStdin,
					Env:   envFromDockerFormat(spec.Env),
					SecurityContext: &corev1.SecurityContext{
						Privileged: &spec.HostConfig.Privileged,
					},
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating pod: %w", err)
	}

	s.mu.Lock()
	s.created[dockerName] = true
	if len(spec.NetworkingConfig.EndpointsConfig) > 0 {
		if s.netMembers[dockerName] == nil {
			s.netMembers[dockerName] = map[string]bool{}
		}
		for net := range spec.NetworkingConfig.EndpointsConfig {
			s.netMembers[dockerName][net] = true
		}
	}
	s.mu.Unlock()
	return nil
}

func envFromDockerFormat(env []string) []corev1.EnvVar {
	var out []corev1.EnvVar
	for _, kv := range env {
		for i := range kv {
			if kv[i] == '=' {
				out = append(out, corev1.EnvVar{Name: kv[:i], Value: kv[i+1:]})
				break
			}
		}
	}
	return out
}

// waitForIP polls pod status for an assigned IP — the PoC equivalent of the
// design doc's "inspect must return NetworkSettings.IPAddress = pod IP".
func (s *shim) waitForIP(ctx context.Context, dockerName string) (*corev1.Pod, error) {
	podName := s.k8sPodName(dockerName)
	for {
		pod, err := s.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if pod.Status.PodIP != "" {
			return pod, nil
		}
		select {
		case <-ctx.Done():
			return pod, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// isManaged reports whether dockerName is a container this shim created (vs.
// an external name like the L2 router container, which PWD's
// overlaySessionProvisioner tries to NetworkConnect without ever creating it
// through us).
func (s *shim) isManaged(dockerName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.created[dockerName]
}

func (s *shim) getPod(ctx context.Context, dockerName string) (*corev1.Pod, error) {
	return s.clientset.CoreV1().Pods(namespace).Get(ctx, s.k8sPodName(dockerName), metav1.GetOptions{})
}

func (s *shim) deletePod(ctx context.Context, dockerName string) error {
	grace := int64(0)
	err := s.clientset.CoreV1().Pods(namespace).Delete(ctx, s.k8sPodName(dockerName), metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// networkConnect records dockerName as a member of networkName. It never
// fails and never touches Kubernetes: see the netMembers doc comment above
// for why (this is what lets SessionNew's NetworkConnect(L2ContainerName,
// ...) call succeed instead of 404ing and aborting session creation).
func (s *shim) networkConnect(dockerName, networkName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.netMembers[dockerName] == nil {
		s.netMembers[dockerName] = map[string]bool{}
	}
	s.netMembers[dockerName][networkName] = true
}

// fakeIPFor returns a stable, non-functional IP for docker names this shim
// doesn't back with a real Pod (the L2 case) — enough for callers that just
// need *an* IP string back, not connectivity. See netMembers doc comment.
func fakeIPFor(dockerName, networkName string) string {
	sum := sha1.Sum([]byte(dockerName + "/" + networkName))
	return fmt.Sprintf("169.254.%d.%d", sum[0], sum[1])
}
