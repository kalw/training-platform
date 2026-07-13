package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func podWithStatus(ns, name string, status corev1.PodStatus, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Status:     status,
	}
}

func TestFatalPodReason(t *testing.T) {
	cases := []struct {
		status corev1.PodStatus
		want   string // "" means recoverable
	}{
		{corev1.PodStatus{Phase: corev1.PodPending}, ""},
		{corev1.PodStatus{Phase: corev1.PodFailed}, "pod Failed"},
		{corev1.PodStatus{Phase: corev1.PodSucceeded}, "pod Succeeded"},
		{corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{
			{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "nope"}}},
		}}, "ImagePullBackOff: nope"},
		{corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{
			{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}},
		}}, ""},
	}
	for i, c := range cases {
		if got := fatalPodReason(&corev1.Pod{Status: c.status}); got != c.want {
			t.Errorf("case %d: fatalPodReason = %q, want %q", i, got, c.want)
		}
	}
}

func TestWaitForReadyFailsFastOnFatalState(t *testing.T) {
	pod := podWithStatus("ns", "i-bad", corev1.PodStatus{
		Phase: corev1.PodPending,
		ContainerStatuses: []corev1.ContainerStatus{
			{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull", Message: "not found"}}},
		},
	}, nil)
	e := &Engine{cs: fake.NewSimpleClientset(pod), ttl: time.Hour}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := e.waitForReady(ctx, "ns", "i-bad")
	if err == nil || ctx.Err() != nil {
		t.Fatalf("want fast failure with reason, got err=%v ctxErr=%v", err, ctx.Err())
	}
	if want := "ErrImagePull"; err != nil && !strings.Contains(err.Error(), want) {
		t.Errorf("error %q should carry the pod reason %q", err, want)
	}
}

func TestStatusAndExtend(t *testing.T) {
	now := time.Now()
	hard := fmt.Sprintf("%d", now.Add(time.Hour).Unix())
	staleIdle := fmt.Sprintf("%d", now.Add(-time.Minute).Unix())
	pod := podWithStatus("ns", "i-ok", corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.9"},
		map[string]string{managedByLabel: managedByValue, expiresAtLabel: hard, idleExpiresAtLabel: staleIdle})
	e := &Engine{cs: fake.NewSimpleClientset(pod), ttl: time.Hour, idleTTL: 10 * time.Minute}

	st, err := e.Status(context.Background(), "ns", "i-ok")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Ready || st.Phase != "Running" || st.IP != "10.0.0.9" {
		t.Errorf("status = %+v, want ready Running with IP", st)
	}
	// Effective expiry is the sooner deadline — here the stale idle window.
	if st.ExpiresAt != now.Add(-time.Minute).Unix() {
		t.Errorf("Status expiry %d, want the (stale) idle deadline", st.ExpiresAt)
	}

	// Extend slides the idle window by IdleTTL; the hard cap stays put.
	exp, err := e.Extend(context.Background(), "ns", "i-ok")
	if err != nil {
		t.Fatal(err)
	}
	wantMin, wantMax := now.Add(9*time.Minute).Unix(), now.Add(11*time.Minute).Unix()
	if exp < wantMin || exp > wantMax {
		t.Errorf("Extend expiry %d, want idle window ~10m out [%d,%d]", exp, wantMin, wantMax)
	}
	st, _ = e.Status(context.Background(), "ns", "i-ok")
	if st.ExpiresAt != exp {
		t.Errorf("Status expiry %d != extended %d", st.ExpiresAt, exp)
	}

	if _, err := e.Status(context.Background(), "ns", "i-missing"); err == nil {
		t.Error("Status of a missing pod should error")
	}
	if _, err := e.Extend(context.Background(), "ns", "i-missing"); err == nil {
		t.Error("Extend of a missing pod should error")
	}
}

func TestGCReapsIdleExpiredPods(t *testing.T) {
	now := time.Now()
	hardFuture := fmt.Sprintf("%d", now.Add(time.Hour).Unix())
	idlePast := fmt.Sprintf("%d", now.Add(-time.Minute).Unix())
	idleFuture := fmt.Sprintf("%d", now.Add(9*time.Minute).Unix())
	hardPast := fmt.Sprintf("%d", now.Add(-time.Minute).Unix())

	mk := func(name string, labels map[string]string) *corev1.Pod {
		labels[managedByLabel] = managedByValue
		return podWithStatus("ns", name, corev1.PodStatus{Phase: corev1.PodRunning}, labels)
	}
	e := &Engine{cs: fake.NewSimpleClientset(
		// Abandoned: page stopped pinging (closed tab) — idle window passed.
		mk("i-abandoned", map[string]string{expiresAtLabel: hardFuture, idleExpiresAtLabel: idlePast}),
		// Live: pings keep the idle window ahead.
		mk("i-live", map[string]string{expiresAtLabel: hardFuture, idleExpiresAtLabel: idleFuture}),
		// Over the hard cap even though recently pinged.
		mk("i-capped", map[string]string{expiresAtLabel: hardPast, idleExpiresAtLabel: idleFuture}),
		// Pre-idle-label Pod: only the hard cap applies.
		mk("i-legacy", map[string]string{expiresAtLabel: hardFuture}),
	), ttl: time.Hour, idleTTL: 10 * time.Minute}

	n, err := e.GCExpiredPods(context.Background(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("GC reaped %d pods, want 2 (abandoned + capped)", n)
	}
	for name, wantAlive := range map[string]bool{"i-abandoned": false, "i-live": true, "i-capped": false, "i-legacy": true} {
		_, err := e.Status(context.Background(), "ns", name)
		if alive := err == nil; alive != wantAlive {
			t.Errorf("pod %s alive=%v, want %v", name, alive, wantAlive)
		}
	}
}
