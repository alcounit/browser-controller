package browser

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	browserv1 "github.com/alcounit/browser-controller/apis/browser/v1"
	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	"github.com/alcounit/browser-controller/store"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func newBrowserScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	if err := browserv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add browserv1 scheme: %v", err)
	}
	if err := configv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add configv1 scheme: %v", err)
	}
	return scheme
}

func newBrowserClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&browserv1.Browser{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return builder.Build()
}

func setStoreConfig(t *testing.T, cfgStore *store.BrowserConfigStore, key string, spec *configv1.BrowserVersionConfigSpec) {
	t.Helper()
	v := reflect.ValueOf(cfgStore).Elem().FieldByName("config")
	if !v.IsValid() {
		t.Fatalf("config field not found")
	}
	m := *(*map[string]*configv1.BrowserVersionConfigSpec)(unsafe.Pointer(v.UnsafeAddr()))
	m[key] = spec
}

func TestContainerStateEqual(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	a := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}}
	b := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}}
	if !containerStateEqual(a, b) {
		t.Fatalf("expected running states to be equal")
	}

	b = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "Init"}}
	if containerStateEqual(a, b) {
		t.Fatalf("expected different states to be unequal")
	}

	a = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "A", Message: "m"}}
	b = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "A", Message: "m"}}
	if !containerStateEqual(a, b) {
		t.Fatalf("expected waiting states to be equal")
	}

	a = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "r"}}
	b = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "r"}}
	if !containerStateEqual(a, b) {
		t.Fatalf("expected terminated states to be equal")
	}

	a = corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}}
	b = corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(1 * time.Second))}}
	if containerStateEqual(a, b) {
		t.Fatalf("expected running states to be different")
	}

	a = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}
	b = corev1.ContainerState{}
	if containerStateEqual(a, b) {
		t.Fatalf("expected terminated presence mismatch to be different")
	}
}

func TestGetContainerPorts(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 4444},
					},
				},
			},
		},
	}
	ports := getContainerPorts("main", pod)
	if len(ports) != 1 || ports[0].ContainerPort != 4444 {
		t.Fatalf("expected one port 4444, got %+v", ports)
	}
	ports = getContainerPorts("missing", pod)
	if len(ports) != 0 {
		t.Fatalf("expected no ports, got %+v", ports)
	}
}

func TestLenSidecars(t *testing.T) {
	cfg := &configv1.BrowserVersionConfigSpec{}
	if lenSidecars(cfg) != 0 {
		t.Fatalf("expected 0 sidecars")
	}
	cfg.Sidecars = &[]configv1.Sidecar{{Name: "s1", Image: "i"}}
	if lenSidecars(cfg) != 1 {
		t.Fatalf("expected 1 sidecar")
	}
}

func TestBuildBrowserPod(t *testing.T) {
	labels := map[string]string{"l": "v"}
	annotations := map[string]string{"a": "b"}
	priv := true
	workingDir := "/work"
	sidecars := []configv1.Sidecar{{Name: "seleniferous", Image: "sidecar"}}
	cfg := &configv1.BrowserVersionConfigSpec{
		Image:       "browser",
		Labels:      &labels,
		Annotations: &annotations,
		Sidecars:    &sidecars,
		Privileged:  &priv,
		WorkingDir:  &workingDir,
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
			Labels:    map[string]string{"from": "browser"},
		},
	}

	pod := buildBrowserPod(brw, cfg)
	if pod.Name != "b1" || pod.Namespace != "ns" {
		t.Fatalf("unexpected pod identity")
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(pod.Spec.Containers))
	}
	if pod.Spec.Containers[0].SecurityContext == nil || pod.Spec.Containers[0].SecurityContext.Privileged == nil || !*pod.Spec.Containers[0].SecurityContext.Privileged {
		t.Fatalf("expected privileged security context")
	}
	if pod.Labels["from"] != "browser" || pod.Labels["l"] != "v" {
		t.Fatalf("expected merged labels, got %+v", pod.Labels)
	}
	if pod.Annotations["a"] != "b" {
		t.Fatalf("expected annotations to be set")
	}
}

func TestHandleMissingPodConfigNotFound(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, cfgStore, scheme)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	if err := cl.Create(context.Background(), brw); err != nil {
		t.Fatalf("create browser: %v", err)
	}

	_, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
}

func TestHandleMissingPodStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				"selenosis.io/browser":         "b1",
				"selenosis.io/browser.name":    "chrome",
				"selenosis.io/browser.version": "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, cfgStore, scheme)

	_, err := r.handleMissingPod(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleMissingPodCreatesPod(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, cfgStore, scheme)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	if err := cl.Create(context.Background(), brw); err != nil {
		t.Fatalf("create browser: %v", err)
	}

	res, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != quickCheck {
		t.Fatalf("expected quick requeue, got %v", res.RequeueAfter)
	}

	pod := &corev1.Pod{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, pod); err != nil {
		t.Fatalf("expected pod to be created: %v", err)
	}
}

func TestUpdateBrowserStatusCriticalContainer(t *testing.T) {
	scheme := newBrowserScheme(t)
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
	}
	if err := cl.Create(context.Background(), brw); err != nil {
		t.Fatalf("create browser: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: browserContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "error",
							Message:  "boom",
						},
					},
				},
			},
		},
	}
	pod.Status.StartTime = &now

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err == nil {
		t.Fatalf("expected browser to be deleted")
	}
}

func TestUpdateBrowserStatusUpdatesFields(t *testing.T) {
	scheme := newBrowserScheme(t)
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}
	if err := cl.Create(context.Background(), brw); err != nil {
		t.Fatalf("create browser: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "browser"},
			},
		},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.0.0.1",
			StartTime: &now,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "browser", RestartCount: 1},
			},
		},
	}

	res, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != periodicReconcile {
		t.Fatalf("expected periodic requeue, got %v", res.RequeueAfter)
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.PodIP != "10.0.0.1" || got.Status.Phase != corev1.PodRunning {
		t.Fatalf("unexpected status: %+v", got.Status)
	}
	if len(got.Status.ContainerStatuses) != 1 || got.Status.ContainerStatuses[0].RestartCount != 1 {
		t.Fatalf("unexpected container statuses: %+v", got.Status.ContainerStatuses)
	}
}

func TestReconcileNotFound(t *testing.T) {
	scheme := newBrowserScheme(t)
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "missing"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcileAddsFinalizerAndLabels(t *testing.T) {
	scheme := newBrowserScheme(t)
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	if err := cl.Create(context.Background(), brw); err != nil {
		t.Fatalf("create browser: %v", err)
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, browserPodFinalizer) {
		t.Fatalf("expected finalizer to be set")
	}
	if got.Labels["selenosis.io/browser"] != "b1" {
		t.Fatalf("expected browser label to be set")
	}
}

func TestReconcileFailedBrowserRemovesFinalizer(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Status: browserv1.BrowserStatus{
			Phase: corev1.PodFailed,
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if controllerutil.ContainsFinalizer(got, browserPodFinalizer) {
		t.Fatalf("expected finalizer to be removed")
	}
}

func TestReconcileFailedBrowserFinalizerRemoveError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Status: browserv1.BrowserStatus{
			Phase: corev1.PodFailed,
		},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestHandleDeletionNoFinalizer(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	now := metav1.NewTime(time.Now().UTC())
	brw.DeletionTimestamp = &now

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue, got %v", res.RequeueAfter)
	}
}

func TestHandleDeletionPodNotFound(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHandleDeletionPodDeletionInProgress(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{"pod.finalizer"},
		},
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != quickCheck {
		t.Fatalf("expected quick requeue, got %v", res.RequeueAfter)
	}
}

func TestHandleDeletionPodTimeout(t *testing.T) {
	scheme := newBrowserScheme(t)
	old := metav1.NewTime(time.Now().Add(-podDeletionTimeout - time.Second).UTC())
	now := metav1.NewTime(time.Now().UTC())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &old,
			Finalizers:        []string{"pod.finalizer"},
		},
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHandleMissingPodAlreadyExists(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, cfgStore, scheme)

	res, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != quickCheck {
		t.Fatalf("expected quick requeue, got %v", res.RequeueAfter)
	}
}

func TestReconcilePodFailedUpdatesStatus(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Reason",
			Message: "Message",
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
}

func TestReconcilePodPendingContainerTerminated(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason: "Error",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcilePodPendingWaitingBadReason(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "boom",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcilePodPendingCreationTimeout(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-podCreationTimeout - time.Second).UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "ContainerCreating",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestUpdateBrowserStatusNoChanges(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Status: browserv1.BrowserStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.0.0.1",
			StartTime: &now,
			ContainerStatuses: []browserv1.ContainerStatus{
				{Name: "browser", RestartCount: 1},
			},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "browser"},
			},
		},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.0.0.1",
			StartTime: &now,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "browser", RestartCount: 1},
			},
		},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestBuildBrowserPodWithInitContainersAndVolumes(t *testing.T) {
	workDir := "/work"
	init := []configv1.Sidecar{{Name: "init", Image: "img", WorkingDir: &workDir}}
	volumes := []corev1.Volume{{Name: "v"}}
	mounts := []corev1.VolumeMount{{Name: "v", MountPath: "/m"}}
	cfg := &configv1.BrowserVersionConfigSpec{
		Image:          "browser",
		InitContainers: &init,
		Volumes:        &volumes,
		VolumeMounts:   &mounts,
		WorkingDir:     &workDir,
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}

	pod := buildBrowserPod(brw, cfg)
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("expected init container")
	}
	if len(pod.Spec.Volumes) != 1 {
		t.Fatalf("expected volume")
	}
	if pod.Spec.Containers[0].WorkingDir != "/work" {
		t.Fatalf("expected working dir")
	}
}

func TestBuildBrowserPodInitContainerFields(t *testing.T) {
	workDir := "/work"
	cmd := []string{"sh"}
	ports := []corev1.ContainerPort{{ContainerPort: 8080}}
	env := []corev1.EnvVar{{Name: "A", Value: "B"}}
	mounts := []corev1.VolumeMount{{Name: "v", MountPath: "/m"}}
	resources := corev1.ResourceRequirements{}
	init := []configv1.Sidecar{{
		Name:            "init",
		Image:           "img",
		Command:         &cmd,
		WorkingDir:      &workDir,
		Ports:           &ports,
		Env:             &env,
		VolumeMounts:    &mounts,
		Resources:       &resources,
		ImagePullPolicy: corev1.PullAlways,
	}}
	cfg := &configv1.BrowserVersionConfigSpec{
		Image:          "browser",
		InitContainers: &init,
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}

	pod := buildBrowserPod(brw, cfg)
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("expected init container")
	}
	if len(pod.Spec.InitContainers[0].Env) != 1 || len(pod.Spec.InitContainers[0].Ports) != 1 {
		t.Fatalf("expected init container fields to be set")
	}
}

func TestBuildBrowserPodAllFields(t *testing.T) {
	workDir := "/work"
	labels := map[string]string{"l": "v"}
	annotations := map[string]string{"a": "b"}
	env := []corev1.EnvVar{{Name: "ENV", Value: "v"}}
	resources := corev1.ResourceRequirements{}
	volumes := []corev1.Volume{{Name: "v"}}
	mounts := []corev1.VolumeMount{{Name: "v", MountPath: "/m"}}
	nodeSelector := map[string]string{"k": "v"}
	affinity := &corev1.Affinity{}
	tolerations := []corev1.Toleration{{Key: "k"}}
	hostAliases := []corev1.HostAlias{{IP: "127.0.0.1", Hostnames: []string{"h"}}}
	sidecarEnv := []corev1.EnvVar{{Name: "S", Value: "v"}}
	sidecarPorts := []corev1.ContainerPort{{ContainerPort: 123}}
	sidecarMounts := []corev1.VolumeMount{{Name: "v", MountPath: "/m"}}
	sidecarCmd := []string{"run"}
	sidecars := []configv1.Sidecar{{
		Name:            "sidecar",
		Image:           "sidecar-img",
		Command:         &sidecarCmd,
		WorkingDir:      &workDir,
		Ports:           &sidecarPorts,
		Env:             &sidecarEnv,
		VolumeMounts:    &sidecarMounts,
		Resources:       &resources,
		ImagePullPolicy: corev1.PullIfNotPresent,
	}}
	priv := true
	pullSecrets := []corev1.LocalObjectReference{{Name: "sec"}}
	dnsConfig := &corev1.PodDNSConfig{}
	secCtx := &corev1.PodSecurityContext{}

	cfg := &configv1.BrowserVersionConfigSpec{
		Image:            "browser",
		Labels:           &labels,
		Annotations:      &annotations,
		Env:              &env,
		Resources:        &resources,
		Volumes:          &volumes,
		VolumeMounts:     &mounts,
		NodeSelector:     &nodeSelector,
		Affinity:         affinity,
		Tolerations:      &tolerations,
		HostAliases:      &hostAliases,
		Sidecars:         &sidecars,
		Privileged:       &priv,
		ImagePullSecrets: &pullSecrets,
		DNSConfig:        dnsConfig,
		SecurityContext:  secCtx,
		WorkingDir:       &workDir,
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "b1",
			Namespace:   "ns",
			Labels:      map[string]string{"from": "browser"},
			Annotations: map[string]string{"ba": "bv"},
		},
	}

	pod := buildBrowserPod(brw, cfg)
	if pod.Spec.NodeSelector["k"] != "v" {
		t.Fatalf("expected node selector")
	}
	if pod.Spec.Affinity == nil || pod.Spec.DNSConfig == nil {
		t.Fatalf("expected pod spec fields to be set")
	}
	if len(pod.Spec.Tolerations) != 1 || len(pod.Spec.HostAliases) != 1 {
		t.Fatalf("expected tolerations/hostAliases")
	}
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.SecurityContext == nil {
		t.Fatalf("expected image pull secrets/security context")
	}
	if pod.Spec.Containers[0].WorkingDir != "/work" {
		t.Fatalf("expected working dir on main container")
	}
	if pod.Spec.Containers[1].Name != "sidecar" {
		t.Fatalf("expected sidecar container")
	}
}

func TestBuildBrowserPodBrowserLabelsOnly(t *testing.T) {
	cfg := &configv1.BrowserVersionConfigSpec{
		Image: "browser",
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
			Labels:    map[string]string{"only": "browser"},
		},
	}

	pod := buildBrowserPod(brw, cfg)
	if pod.Labels["only"] != "browser" {
		t.Fatalf("expected browser labels to be applied")
	}
}

type errorClient struct {
	client.Client
	createErr error
	deleteErr error
}

func (e errorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if e.createErr != nil {
		return e.createErr
	}
	return e.Client.Create(ctx, obj, opts...)
}

func (e errorClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}
	return e.Client.Delete(ctx, obj, opts...)
}

func TestDeletePodNotFound(t *testing.T) {
	scheme := newBrowserScheme(t)
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	err := r.deletePod(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "ns"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDeletePodError(t *testing.T) {
	scheme := newBrowserScheme(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	err := r.deletePod(context.Background(), pod)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleMissingPodCreateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				"selenosis.io/browser":         "b1",
				"selenosis.io/browser.name":    "chrome",
				"selenosis.io/browser.version": "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	base := newBrowserClient(scheme, brw)
	cl := errorClient{Client: base, createErr: apierrors.NewInternalError(errors.New("boom"))}
	r := NewBrowserReconciler(cl, cfgStore, scheme)

	_, err := r.handleMissingPod(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcilePodDeletedDeletesBrowser(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{"pod.finalizer"},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcilePodNotFoundBrowserFailed(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     browserv1.BrowserStatus{Phase: corev1.PodFailed},
		Spec: browserv1.BrowserSpec{
			BrowserName:    "chrome",
			BrowserVersion: "120",
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcilePodPendingContainerCreatingNoTimeout(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				"selenosis.io/browser":         "b1",
				"selenosis.io/browser.name":    "chrome",
				"selenosis.io/browser.version": "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

type patchErrorClient struct {
	client.Client
	patchErr       error
	statusPatchErr error
	getErr         error
	getPodErr      error
}

func (p patchErrorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if p.getErr != nil {
		return p.getErr
	}
	if p.getPodErr != nil {
		if _, ok := obj.(*corev1.Pod); ok {
			return p.getPodErr
		}
	}
	return p.Client.Get(ctx, key, obj, opts...)
}

func (p patchErrorClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if p.patchErr != nil {
		return p.patchErr
	}
	return p.Client.Patch(ctx, obj, patch, opts...)
}

func (p patchErrorClient) Status() client.StatusWriter {
	return &statusPatchErrorWriter{StatusWriter: p.Client.Status(), err: p.statusPatchErr}
}

type statusPatchErrorWriter struct {
	client.StatusWriter
	err error
}

func (s *statusPatchErrorWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if s.err != nil {
		return s.err
	}
	return s.StatusWriter.Patch(ctx, obj, patch, opts...)
}

func TestRetryUpdateGetError(t *testing.T) {
	scheme := newBrowserScheme(t)
	base := newBrowserClient(scheme)
	r := NewBrowserReconciler(patchErrorClient{Client: base, getErr: apierrors.NewBadRequest("bad")}, store.NewBrowserConfigStore(), scheme)

	err := r.retryUpdate(context.Background(), &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}, func(*browserv1.Browser) {})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRetryUpdatePatchError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	err := r.retryUpdate(context.Background(), brw, func(b *browserv1.Browser) { b.Labels = map[string]string{"k": "v"} })
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRetryStatusUpdatePatchError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	err := r.retryStatusUpdate(context.Background(), brw, func(b *browserv1.Browser) { b.Status.Phase = corev1.PodRunning })
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestDeleteBrowserNoFinalizer(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.deleteBrowser(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted")
	}
}

func TestDeleteBrowserDeleteError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
	}
	base := newBrowserClient(scheme, brw)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.deleteBrowser(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleDeletionPodDeleteError(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	res, err := r.handleDeletion(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestHandleDeletionDeleteSuccess(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != quickCheck {
		t.Fatalf("expected quick requeue, got %v", res.RequeueAfter)
	}
}

func TestHandleDeletionFailedPodGraceDelete(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != quickCheck {
		t.Fatalf("expected quick requeue, got %v", res.RequeueAfter)
	}
}

func TestHandleDeletionPodGetError(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, getPodErr: apierrors.NewInternalError(errors.New("pod"))}, store.NewBrowserConfigStore(), scheme)

	_, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHandleDeletionFinalizerRemoveError(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	res, err := r.handleDeletion(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcileDeletionTimestamp(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcileFinalizerAddError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcileLabelUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels:     map[string]string{"selenosis.io/browser": "wrong"},
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcilePendingStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcilePendingTerminatedStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				"selenosis.io/browser":         "b1",
				"selenosis.io/browser.name":    "chrome",
				"selenosis.io/browser.version": "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
					},
				},
			},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcilePendingCreationTimeoutStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				"selenosis.io/browser":         "b1",
				"selenosis.io/browser.name":    "chrome",
				"selenosis.io/browser.version": "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-podCreationTimeout - time.Second).UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
					},
				},
			},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcilePendingWaitingStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
			Labels: map[string]string{
				"selenosis.io/browser":         "b1",
				"selenosis.io/browser.name":    "chrome",
				"selenosis.io/browser.version": "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcileHandleMissingPodCreateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	cl := errorClient{Client: base, createErr: apierrors.NewInternalError(errors.New("create"))}
	r := NewBrowserReconciler(cl, cfgStore, scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcilePodDeletingDeleteBrowserError(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{"pod.finalizer"},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcilePodPendingWaitingDeleteError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestUpdateBrowserStatusCriticalSidecar(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: sidecarContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
					},
				},
			},
		},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDeleteBrowserFinalizerSuccess(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.deleteBrowser(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDeleteBrowserRetryUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme)

	_, err := r.deleteBrowser(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestUpdateBrowserStatusCriticalAlreadyFailed(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Status: browserv1.BrowserStatus{Phase: corev1.PodFailed},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: browserContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
					},
				},
			},
		},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestUpdateBrowserStatusNoContainerStatuses(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcilePodFailedDeleteError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}
	base := newBrowserClient(scheme, brw, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcilePodPendingPodInitializing(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRetryStatusUpdateGetError(t *testing.T) {
	scheme := newBrowserScheme(t)
	base := newBrowserClient(scheme)
	r := NewBrowserReconciler(patchErrorClient{Client: base, getErr: apierrors.NewBadRequest("bad")}, store.NewBrowserConfigStore(), scheme)

	err := r.retryStatusUpdate(context.Background(), &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}, func(*browserv1.Browser) {})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestUpdateBrowserStatusBrowserStatusChangedOnly(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: browserv1.BrowserStatus{
			Phase:     corev1.PodPending,
			PodIP:     "",
			StartTime: nil,
			ContainerStatuses: []browserv1.ContainerStatus{
				{Name: "browser", RestartCount: 1},
			},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "browser"}},
		},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.0.0.2",
			StartTime: &now,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "browser", RestartCount: 1},
			},
		},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestUpdateBrowserStatusContainerStateChange(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: browserv1.BrowserStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []browserv1.ContainerStatus{
				{Name: "browser"},
			},
		},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "browser"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "pull"},
					},
				},
			},
		},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
func TestReconcileBrowserGetError(t *testing.T) {
	scheme := newBrowserScheme(t)
	base := newBrowserClient(scheme)
	r := NewBrowserReconciler(patchErrorClient{Client: base, getErr: apierrors.NewBadRequest("bad")}, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcilePodGetError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, getPodErr: apierrors.NewInternalError(errors.New("pod"))}, store.NewBrowserConfigStore(), scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestUpdateBrowserStatusContainerStatusLengthChange(t *testing.T) {
	scheme := newBrowserScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "browser"}},
		},
		Status: corev1.PodStatus{
			Phase:     corev1.PodRunning,
			PodIP:     "10.0.0.1",
			StartTime: &now,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "browser", RestartCount: 2},
			},
		},
	}

	_, err := r.updateBrowserStatus(context.Background(), brw, pod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

type conflictClient struct {
	client.Client
	patchCalls       int
	statusPatchCalls int
}

func (c *conflictClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchCalls == 0 {
		c.patchCalls++
		return apierrors.NewConflict(schema.GroupResource{Group: "selenosis.io", Resource: "browsers"}, obj.GetName(), nil)
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *conflictClient) Status() client.StatusWriter {
	return &conflictStatusWriter{StatusWriter: c.Client.Status(), parent: c}
}

type conflictStatusWriter struct {
	client.StatusWriter
	parent *conflictClient
}

func (w *conflictStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if w.parent.statusPatchCalls == 0 {
		w.parent.statusPatchCalls++
		return apierrors.NewConflict(schema.GroupResource{Group: "selenosis.io", Resource: "browsers"}, obj.GetName(), nil)
	}
	return w.StatusWriter.Patch(ctx, obj, patch, opts...)
}

func TestRetryUpdateConflictThenSuccess(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}
	base := newBrowserClient(scheme, brw)
	c := &conflictClient{Client: base}
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme)

	err := r.retryUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		if b.Labels == nil {
			b.Labels = map[string]string{}
		}
		b.Labels["k"] = "v"
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.patchCalls == 0 {
		t.Fatalf("expected conflict patch to be invoked")
	}
}

func TestRetryStatusUpdateConflictThenSuccess(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}
	base := newBrowserClient(scheme, brw)
	c := &conflictClient{Client: base}
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme)

	err := r.retryStatusUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		b.Status.Phase = corev1.PodRunning
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.statusPatchCalls == 0 {
		t.Fatalf("expected conflict status patch to be invoked")
	}
}

type alwaysConflictClient struct {
	client.Client
}

func (c *alwaysConflictClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return apierrors.NewConflict(schema.GroupResource{Group: "selenosis.io", Resource: "browsers"}, obj.GetName(), nil)
}

func (c *alwaysConflictClient) Status() client.StatusWriter {
	return &alwaysConflictStatusWriter{StatusWriter: c.Client.Status()}
}

type alwaysConflictStatusWriter struct {
	client.StatusWriter
}

func (w *alwaysConflictStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return apierrors.NewConflict(schema.GroupResource{Group: "selenosis.io", Resource: "browsers"}, obj.GetName(), nil)
}

func TestRetryUpdateMaxConflict(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
	}
	base := newBrowserClient(scheme, brw)
	c := &alwaysConflictClient{Client: base}
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme)

	err := r.retryUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		if b.Labels == nil {
			b.Labels = map[string]string{}
		}
		b.Labels["k"] = "v"
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRetryStatusUpdateMaxConflict(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
	}
	base := newBrowserClient(scheme, brw)
	c := &alwaysConflictClient{Client: base}
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme)

	err := r.retryStatusUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		b.Status.Phase = corev1.PodRunning
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}
