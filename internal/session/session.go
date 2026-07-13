// Package session is the Kubernetes-native session engine: it provisions
// disposable learner sandboxes directly through the Kubernetes API (no
// Docker, no Swarm). A session is a labelled Namespace; each instance in it
// is a privileged Pod (a DinD image, so learners can run `docker` inside).
//
// This is the native counterpart to the Docker-API shim: the shim exists so
// unmodified Docker tooling keeps working, whereas this package is what a
// k8s-native console uses directly. Both back onto the same primitives
// (Pods, pods/exec, pods/attach) proven in K8S-SANDBOX-DESIGN.md.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	managedByLabel  = "app.kubernetes.io/managed-by"
	managedByValue  = "training-platform"
	sessionIDLabel  = "training.kalw/session-id"
	expiresAtLabel  = "training.kalw/expires-at" // unix seconds, for GC
	instancePodName = "instance"
)

// Engine provisions and tears down sessions against a cluster.
type Engine struct {
	cs       kubernetes.Interface
	cfg      *rest.Config
	nsPfx    string        // namespace prefix, e.g. "session-"
	ttl      time.Duration // default session lifetime
	dindImg  string        // default instance image
	hostFQDN string        // exported as PWD_HOST_FQDN inside instances
}

// Options configures an Engine.
type Options struct {
	// NamespacePrefix is prepended to session IDs to form namespace names.
	NamespacePrefix string
	// TTL is the default session lifetime (GC deletes expired namespaces).
	TTL time.Duration
	// DefaultImage is the instance image when a session doesn't specify one.
	DefaultImage string
	// HostFQDN, when set, is exported to instances as PWD_HOST_FQDN — the
	// exposed-port routing suffix legacy lesson snippets build URLs with.
	HostFQDN string
}

// New builds an Engine from the ambient kube config (in-cluster service
// account if present, else the default kubeconfig).
func New(opts Options) (*Engine, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename())
		if err != nil {
			return nil, fmt.Errorf("loading kube config: %w", err)
		}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building clientset: %w", err)
	}
	if opts.NamespacePrefix == "" {
		opts.NamespacePrefix = "session-"
	}
	if opts.TTL == 0 {
		opts.TTL = 4 * time.Hour
	}
	if opts.DefaultImage == "" {
		opts.DefaultImage = "ghcr.io/kalw/training-console-pwd:dind"
	}
	return &Engine{cs: cs, cfg: cfg, nsPfx: opts.NamespacePrefix, ttl: opts.TTL, dindImg: opts.DefaultImage, hostFQDN: opts.HostFQDN}, nil
}

// RESTConfig exposes the cluster config so callers (e.g. the terminal
// package) can open exec/attach streams against instances this Engine owns.
func (e *Engine) RESTConfig() *rest.Config { return e.cfg }

// NamespaceFor returns the namespace name backing a session id.
func (e *Engine) NamespaceFor(sessionID string) string { return e.nsPfx + sessionID }

// SessionNew creates the namespace backing a session, labelled for GC.
func (e *Engine) SessionNew(ctx context.Context, sessionID string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: e.NamespaceFor(sessionID),
			Labels: map[string]string{
				managedByLabel: managedByValue,
				sessionIDLabel: sessionID,
				expiresAtLabel: fmt.Sprintf("%d", time.Now().Add(e.ttl).Unix()),
			},
		},
	}
	_, err := e.cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating session namespace: %w", err)
	}
	return nil
}

// SessionClose deletes a session's namespace (cascading: instances go too).
func (e *Engine) SessionClose(ctx context.Context, sessionID string) error {
	err := e.cs.CoreV1().Namespaces().Delete(ctx, e.NamespaceFor(sessionID), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Instance is a running sandbox Pod.
type Instance struct {
	Name      string
	SessionID string
	IP        string
	Image     string
	// ExpiresAt is when the TTL GC becomes eligible to reap the Pod (unix
	// seconds; 0 when the instance isn't TTL-labelled).
	ExpiresAt int64
}

// InstanceNew creates a privileged instance Pod in the session namespace and
// waits (up to ctx's deadline) for it to get a Pod IP. image may be empty to
// use the engine default.
func (e *Engine) InstanceNew(ctx context.Context, sessionID, name, image string) (*Instance, error) {
	if image == "" {
		image = e.dindImg
	}
	ns := e.NamespaceFor(sessionID)
	priv := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{managedByLabel: managedByValue, sessionIDLabel: sessionID},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:            instancePodName,
				Image:           image,
				TTY:             true,
				Stdin:           true,
				Env:             e.instanceEnv(sessionID),
				SecurityContext: &corev1.SecurityContext{Privileged: &priv},
			}},
		},
	}
	if _, err := e.cs.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating instance pod: %w", err)
	}

	ip, err := e.waitForReady(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	return &Instance{Name: name, SessionID: sessionID, IP: ip, Image: image}, nil
}

// instanceEnv is the legacy writing-tutorials.md contract inside a session:
// snippets build exposed-port URLs from SESSION_ID and PWD_HOST_FQDN, e.g.
// ip$(hostname -i | sed "s/\./-/g")-${SESSION_ID}-80.${PWD_HOST_FQDN}.
// SESSION_ID must be [0-9a-z] only to match the router host encoding.
func (e *Engine) instanceEnv(sessionID string) []corev1.EnvVar {
	env := []corev1.EnvVar{{Name: "SESSION_ID", Value: hostToken(sessionID)}}
	if e.hostFQDN != "" {
		env = append(env, corev1.EnvVar{Name: "PWD_HOST_FQDN", Value: e.hostFQDN})
	}
	return env
}

// hostToken strips a name down to the [0-9a-z]+ segment the router's host
// pattern allows for the session id.
func hostToken(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') {
			b = append(b, c)
		}
	}
	if len(b) == 0 {
		return "s0"
	}
	return string(b)
}

// NewEphemeralInstance creates a privileged instance Pod directly in an
// existing namespace (rather than a per-session one) and waits for its IP.
// It's what the sessions HTTP API uses so a rendered lesson can boot a
// terminal in the shared session namespace the terminal bridge execs into.
// The Pod is labelled with a TTL for GCExpiredPods. image may be empty.
func (e *Engine) NewEphemeralInstance(ctx context.Context, ns, image string) (*Instance, error) {
	if image == "" {
		image = e.dindImg
	}
	name := "i-" + randSuffix()
	expires := time.Now().Add(e.ttl).Unix()
	priv := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				managedByLabel: managedByValue,
				expiresAtLabel: fmt.Sprintf("%d", expires),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:            instancePodName,
				Image:           image,
				TTY:             true,
				Stdin:           true,
				Env:             e.instanceEnv(name),
				SecurityContext: &corev1.SecurityContext{Privileged: &priv},
			}},
		},
	}
	if _, err := e.cs.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating instance pod: %w", err)
	}
	ip, err := e.waitForReady(ctx, ns, name)
	if err != nil {
		// Don't leave a Pod that will never come up behind (ImagePullBackOff
		// etc.); the caller only ever learns the error.
		grace := int64(0)
		_ = e.cs.CoreV1().Pods(ns).Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
		return nil, err
	}
	return &Instance{Name: name, IP: ip, Image: image, ExpiresAt: expires}, nil
}

// GCExpiredPods deletes managed instance Pods in ns whose TTL has passed.
func (e *Engine) GCExpiredPods(ctx context.Context, ns string) (int, error) {
	list, err := e.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: managedByLabel + "=" + managedByValue})
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	deleted := 0
	for i := range list.Items {
		p := &list.Items[i]
		var exp int64
		_, _ = fmt.Sscanf(p.Labels[expiresAtLabel], "%d", &exp)
		if exp != 0 && exp < now {
			grace := int64(0)
			if err := e.cs.CoreV1().Pods(ns).Delete(ctx, p.Name, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}

// DeletePod removes a Pod by name from ns (used by the sessions API DELETE).
func (e *Engine) DeletePod(ctx context.Context, ns, name string) error {
	grace := int64(0)
	err := e.cs.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// InstanceDelete removes an instance Pod.
func (e *Engine) InstanceDelete(ctx context.Context, sessionID, name string) error {
	grace := int64(0)
	err := e.cs.CoreV1().Pods(e.NamespaceFor(sessionID)).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func randSuffix() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// waitForReady blocks until the Pod is Running with an IP, or fails fast when
// the Pod can never get there (bad image, crashing container, evicted…) so
// the caller reports a reason instead of hanging until its deadline.
func (e *Engine) waitForReady(ctx context.Context, ns, name string) (string, error) {
	for {
		pod, err := e.cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		if reason := fatalPodReason(pod); reason != "" {
			return "", fmt.Errorf("instance pod cannot start: %s", reason)
		}
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
			return pod.Status.PodIP, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for instance pod (last phase %s): %w", pod.Status.Phase, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// fatalPodReason returns a human-readable reason when the Pod is in a state
// it won't recover from without intervention, or "" while it can still start.
func fatalPodReason(pod *corev1.Pod) string {
	switch pod.Status.Phase {
	case corev1.PodFailed, corev1.PodSucceeded:
		// RestartPolicyNever: a terminated instance never comes back.
		return "pod " + string(pod.Status.Phase)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "ErrImagePull", "ImagePullBackOff", "InvalidImageName",
				"CreateContainerConfigError", "CreateContainerError", "CrashLoopBackOff":
				return w.Reason + ": " + w.Message
			}
		}
	}
	return ""
}

// PodStatus is the lifecycle view of an instance Pod the sessions API serves.
type PodStatus struct {
	Name      string `json:"pod"`
	Phase     string `json:"phase"`      // Pending, Running, Failed…
	Ready     bool   `json:"ready"`      // Running with an IP
	Reason    string `json:"reason"`     // fatal reason when the Pod can't start
	IP        string `json:"ip"`         // Pod IP once assigned
	ExpiresAt int64  `json:"expires_at"` // unix seconds; 0 if not TTL-labelled
}

// Status reports the lifecycle state of a Pod in ns. A missing Pod returns a
// NotFound error from the API server (callers map it to 404).
func (e *Engine) Status(ctx context.Context, ns, name string) (*PodStatus, error) {
	pod, err := e.cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var exp int64
	_, _ = fmt.Sscanf(pod.Labels[expiresAtLabel], "%d", &exp)
	return &PodStatus{
		Name:      name,
		Phase:     string(pod.Status.Phase),
		Ready:     pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "",
		Reason:    fatalPodReason(pod),
		IP:        pod.Status.PodIP,
		ExpiresAt: exp,
	}, nil
}

// Extend pushes a Pod's TTL out by the engine TTL from now (the sessions
// keepalive: a page that is still open pings so its instance isn't reaped).
// Returns the new expiry (unix seconds).
func (e *Engine) Extend(ctx context.Context, ns, name string) (int64, error) {
	expires := time.Now().Add(e.ttl).Unix()
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, expiresAtLabel, fmt.Sprintf("%d", expires))
	_, err := e.cs.CoreV1().Pods(ns).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return 0, err
	}
	return expires, nil
}

// GCExpired deletes every managed session namespace whose expires-at label is
// in the past. Intended to be called periodically (Kubernetes has no
// built-in namespace TTL). Returns the number of namespaces deleted.
func (e *Engine) GCExpired(ctx context.Context) (int, error) {
	list, err := e.cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	deleted := 0
	for i := range list.Items {
		ns := &list.Items[i]
		exp := ns.Labels[expiresAtLabel]
		var expUnix int64
		_, _ = fmt.Sscanf(exp, "%d", &expUnix)
		if expUnix != 0 && expUnix < now {
			if err := e.cs.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{}); err == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}
