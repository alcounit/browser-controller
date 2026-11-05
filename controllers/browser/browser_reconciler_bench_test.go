package browser

import (
	"context"
	"testing"
	"time"

	browserv1 "github.com/alcounit/browser-controller/apis/browser/v1"
	"github.com/alcounit/browser-controller/store"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func benchBrowser() *browserv1.Browser {
	return &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				browserv1.BrowserLabelKey:        "b1",
				browserv1.BrowserNameLabelKey:    "chrome",
				browserv1.BrowserVersionLabelKey: "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
}

func runReconcileBench(b *testing.B, r *BrowserReconciler) {
	b.Helper()
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		b.Fatalf("warmup reconcile: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Reconcile(ctx, req); err != nil {
			b.Fatalf("reconcile: %v", err)
		}
	}
}

func BenchmarkReconcilePendingPod(b *testing.B) {
	scheme := newBrowserScheme(b)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: BrowserContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}},
				{Name: SidecarContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}},
			},
		},
	}
	cl := newBrowserClient(scheme, benchBrowser(), pod)
	runReconcileBench(b, NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg))
}

func BenchmarkReconcileRunningNoChange(b *testing.B) {
	scheme := newBrowserScheme(b)
	start := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: start,
		},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.0.0.1",
			StartTime: &start,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: BrowserContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: start}}},
				{Name: SidecarContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: start}}},
			},
		},
	}
	cl := newBrowserClient(scheme, benchBrowser(), pod)
	runReconcileBench(b, NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg))
}
