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
	past := fmt.Sprintf("%d", time.Now().Add(-time.Minute).Unix())
	pod := podWithStatus("ns", "i-ok", corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.9"},
		map[string]string{managedByLabel: managedByValue, expiresAtLabel: past})
	e := &Engine{cs: fake.NewSimpleClientset(pod), ttl: time.Hour}

	st, err := e.Status(context.Background(), "ns", "i-ok")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Ready || st.Phase != "Running" || st.IP != "10.0.0.9" {
		t.Errorf("status = %+v, want ready Running with IP", st)
	}

	exp, err := e.Extend(context.Background(), "ns", "i-ok")
	if err != nil {
		t.Fatal(err)
	}
	if min := time.Now().Add(50 * time.Minute).Unix(); exp < min {
		t.Errorf("Extend expiry %d not pushed out (~1h), min %d", exp, min)
	}
	st, _ = e.Status(context.Background(), "ns", "i-ok")
	if st.ExpiresAt != exp {
		t.Errorf("Status expiry %d != extended %d", st.ExpiresAt, exp)
	}

	if _, err := e.Status(context.Background(), "ns", "i-missing"); err == nil {
		t.Error("Status of a missing pod should error")
	}
}
