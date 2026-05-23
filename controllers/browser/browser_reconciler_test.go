package browser

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var defaultCfg = ReconcilerConfig{
	PodCreationTimeout:   5 * time.Minute,
	PodDeletionTimeout:   5 * time.Minute,
	MaxRetries:           3,
	MaxWorkers:           4,
	RateLimiterBaseDelay: 100 * time.Millisecond,
	RateLimiterMaxDelay:  30 * time.Second,
}

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

func assertJitteredRequeue(t *testing.T, got time.Duration, base time.Duration) {
	t.Helper()
	half := base / 2
	if got < half || got >= base {
		t.Fatalf("expected RequeueAfter in [%v, %v), got %v", half, base, got)
	}
}

func envValue(env []corev1.EnvVar, key string) (string, bool) {
	for _, item := range env {
		if item.Name == key {
			return item.Value, true
		}
	}
	return "", false
}

func TestContainerStatusesEqual(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	later := metav1.NewTime(now.Add(1 * time.Second))

	wrap := func(state corev1.ContainerState) []browserv1.ContainerStatus {
		return []browserv1.ContainerStatus{{Name: "c", State: state, Image: "img", RestartCount: 0}}
	}

	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}}
	if !containerStatusesEqual(wrap(running), wrap(running)) {
		t.Fatal("expected identical running states to be equal")
	}

	waiting := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "Init"}}
	if containerStatusesEqual(wrap(running), wrap(waiting)) {
		t.Fatal("expected running vs waiting to be unequal")
	}

	runningLater := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: later}}
	if containerStatusesEqual(wrap(running), wrap(runningLater)) {
		t.Fatal("expected running states with different timestamps to be unequal")
	}

	terminated := corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "r"}}
	if !containerStatusesEqual(wrap(terminated), wrap(terminated)) {
		t.Fatal("expected identical terminated states to be equal")
	}

	// image change must be detected
	a := []browserv1.ContainerStatus{{Name: "c", State: running, Image: "img:v1", RestartCount: 0}}
	b := []browserv1.ContainerStatus{{Name: "c", State: running, Image: "img:v2", RestartCount: 0}}
	if containerStatusesEqual(a, b) {
		t.Fatal("expected image change to be detected")
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

	pod := buildBrowserPod(brw, cfg, nil)
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

func TestBuildBrowserPodMainContainerImagePullPolicy(t *testing.T) {
	cfg := &configv1.BrowserVersionConfigSpec{
		Image:           "browser",
		ImagePullPolicy: corev1.PullAlways,
	}
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
	}

	pod := buildBrowserPod(brw, cfg, nil)
	if len(pod.Spec.Containers) == 0 {
		t.Fatalf("expected at least one container")
	}
	if pod.Spec.Containers[0].ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("expected main container imagePullPolicy=%q, got %q", corev1.PullAlways, pod.Spec.Containers[0].ImagePullPolicy)
	}
}

func TestParseSelenosisOptionsInvalidJSON(t *testing.T) {
	ann := map[string]string{
		browserv1.SelenosisOptionsAnnotationKey: "{nope",
	}
	_, err := parseSelenosisOptions(ann)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), browserv1.SelenosisOptionsAnnotationKey) {
		t.Fatalf("expected error to mention annotation key, got %v", err)
	}
}

func TestParseSelenosisOptionsEmpty(t *testing.T) {
	opts, err := parseSelenosisOptions(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if opts != nil {
		t.Fatalf("expected nil options for nil annotations")
	}

	opts, err = parseSelenosisOptions(map[string]string{browserv1.SelenosisOptionsAnnotationKey: ""})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if opts != nil {
		t.Fatalf("expected nil options for empty annotation")
	}
}

func TestParseSelenosisOptionsValidJSON(t *testing.T) {
	ann := map[string]string{
		browserv1.SelenosisOptionsAnnotationKey: `{"labels":{"a":"b"},"containers":{"browser":{"env":{"X":"1"}}}}`,
	}
	opts, err := parseSelenosisOptions(ann)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if opts == nil || opts.Labels["a"] != "b" {
		t.Fatalf("expected labels to be parsed")
	}
	if opts.Containers["browser"].Env["X"] != "1" {
		t.Fatalf("expected container env to be parsed")
	}
}

func TestApplySelenosisOptionsMergesEnvAndLabels(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"existing": "1"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "browser",
					Env: []corev1.EnvVar{
						{Name: "A", Value: "1"},
						{Name: "B", Value: "2"},
					},
				},
				{Name: "sidecar"},
			},
		},
	}
	opts := &SelenosisOptions{
		Labels: map[string]string{"from": "options"},
		Containers: map[string]ContainerOption{
			"browser": {Env: map[string]string{"B": "override", "C": "new"}},
		},
	}

	applySelenosisOptions(pod, opts)

	if pod.Labels["existing"] != "1" || pod.Labels["from"] != "options" {
		t.Fatalf("expected labels to be merged, got %+v", pod.Labels)
	}

	env := pod.Spec.Containers[0].Env
	if val, ok := envValue(env, "A"); !ok || val != "1" {
		t.Fatalf("expected env A=1")
	}
	if val, ok := envValue(env, "B"); !ok || val != "override" {
		t.Fatalf("expected env B override")
	}
	if val, ok := envValue(env, "C"); !ok || val != "new" {
		t.Fatalf("expected env C new")
	}
}

func TestHandleMissingPodConfigNotFound(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

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

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after config not found, got err=%v", err)
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
				browserv1.BrowserLabelKey:        "b1",
				browserv1.BrowserNameLabelKey:    "chrome",
				browserv1.BrowserVersionLabelKey: "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, cfgStore, scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

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
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)

	pod := &corev1.Pod{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, pod); err != nil {
		t.Fatalf("expected pod to be created: %v", err)
	}
}

func TestHandleMissingPodPendingTimeoutExceededFailsBrowser(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	cl := newBrowserClient(scheme, brw)
	cfg := defaultCfg
	cfg.PendingTimeout = time.Minute
	r := NewBrowserReconciler(cl, cfgStore, scheme, cfg)

	_, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after pending timeout, got err=%v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected no pod to be created, got err=%v", err)
	}
}

func TestHandleMissingPodPendingTimeoutNotYetExceeded(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	cl := newBrowserClient(scheme, brw)
	cfg := defaultCfg
	cfg.PendingTimeout = time.Hour
	r := NewBrowserReconciler(cl, cfgStore, scheme, cfg)

	res, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)

	pod := &corev1.Pod{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, pod); err != nil {
		t.Fatalf("expected pod to be created: %v", err)
	}
}

func TestHandleMissingPodPendingTimeoutDisabled(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-24 * time.Hour)),
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

	res, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)

	pod := &corev1.Pod{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, pod); err != nil {
		t.Fatalf("expected pod to be created: %v", err)
	}
}

func TestHandleMissingPodInvalidSelenosisOptions(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
			Annotations: map[string]string{
				browserv1.SelenosisOptionsAnnotationKey: "{bad-json",
			},
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

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after invalid options, got err=%v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected no pod to be created, got err=%v", err)
	}
}

func TestUpdateBrowserStatusCriticalContainer(t *testing.T) {
	scheme := newBrowserScheme(t)
	cl := newBrowserClient(scheme)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue, got %v", res.RequeueAfter)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	if got.Labels[browserv1.BrowserLabelKey] != "b1" {
		t.Fatalf("expected browser label to be set")
	}
}

func TestReconcileFailedBrowserDeletesBrowser(t *testing.T) {
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted")
	}
}

func TestReconcileFailedBrowserWithPodDeletesPodAndBrowser(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Status: browserv1.BrowserStatus{Phase: corev1.PodFailed},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted")
	}
}

func TestReconcileFailedBrowserPodDeleteError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "b1",
			Namespace:  "ns",
			Finalizers: []string{browserPodFinalizer},
		},
		Status: browserv1.BrowserStatus{Phase: corev1.PodFailed},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
	}
	base := newBrowserClient(scheme, brw, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)
}

func TestHandleDeletionPodTimeout(t *testing.T) {
	scheme := newBrowserScheme(t)
	old := metav1.NewTime(time.Now().Add(-defaultCfg.PodDeletionTimeout - time.Second).UTC())
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

	res, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
							Reason:   "OOMKilled",
							ExitCode: 137,
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "OOMKilled") {
		t.Fatalf("expected message to contain reason, got %q", got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "137") {
		t.Fatalf("expected message to contain exit code, got %q", got.Status.Message)
	}
}

func TestReconcilePodPendingContainerTerminatedPodDeleteError(t *testing.T) {
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
						Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1},
					},
				},
			},
		},
	}
	base := newBrowserClient(scheme, brw, pod)
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted after CrashLoopBackOff")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "CrashLoopBackOff") {
		t.Fatalf("expected message to contain reason, got %q", got.Status.Message)
	}

	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted after second reconcile")
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
			CreationTimestamp: metav1.NewTime(time.Now().Add(-defaultCfg.PodCreationTimeout - time.Second).UTC()),
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "browser") {
		t.Fatalf("expected message to contain container name, got %q", got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "ContainerCreating") {
		t.Fatalf("expected message to contain waiting reason, got %q", got.Status.Message)
	}
}

func TestReconcilePodPendingCreationTimeoutPodDeleteError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-defaultCfg.PodCreationTimeout - time.Second).UTC()),
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
	cl := errorClient{Client: base, deleteErr: apierrors.NewInternalError(errors.New("delete"))}
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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

	pod := buildBrowserPod(brw, cfg, nil)
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

	pod := buildBrowserPod(brw, cfg, nil)
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

	pod := buildBrowserPod(brw, cfg, nil)
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

	pod := buildBrowserPod(brw, cfg, nil)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
				browserv1.BrowserLabelKey:        "b1",
				browserv1.BrowserNameLabelKey:    "chrome",
				browserv1.BrowserVersionLabelKey: "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	base := newBrowserClient(scheme, brw)
	cl := errorClient{Client: base, createErr: apierrors.NewInternalError(errors.New("boom"))}
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcilePodNotFoundBrowserFailedDeletesBrowser(t *testing.T) {
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted")
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
				browserv1.BrowserLabelKey:        "b1",
				browserv1.BrowserNameLabelKey:    "chrome",
				browserv1.BrowserVersionLabelKey: "120",
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(patchErrorClient{Client: base, getErr: apierrors.NewBadRequest("bad")}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryUpdate(context.Background(), &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}, func(*browserv1.Browser) {})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRetryUpdatePatchError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryUpdate(context.Background(), brw, func(b *browserv1.Browser) { b.Labels = map[string]string{"k": "v"} })
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRetryStatusUpdatePatchError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryStatusUpdate(context.Background(), brw, func(b *browserv1.Browser) { b.Status.Phase = corev1.PodRunning })
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestDeleteBrowserNoFinalizer(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	cl := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.handleDeletion(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.handleDeletion(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertJitteredRequeue(t, res.RequeueAfter, quickCheck)
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
	r := NewBrowserReconciler(patchErrorClient{Client: base, getPodErr: apierrors.NewInternalError(errors.New("pod"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.handleDeletion(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error for transient pod get failure")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.handleDeletion(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

type conflictPatchClient struct {
	client.Client
	patchCalls int
	failCount  int
}

func (c *conflictPatchClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patchCalls++
	if c.patchCalls <= c.failCount {
		return apierrors.NewConflict(schema.GroupResource{Resource: "browsers"}, obj.GetName(), errors.New("conflict"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

type conflictStatusPatchClient struct {
	client.Client
	statusPatchCalls int
	statusFailCount  int
}

func (c *conflictStatusPatchClient) Status() client.StatusWriter {
	return &conflictStatusPatchWriter{
		StatusWriter: c.Client.Status(),
		calls:        &c.statusPatchCalls,
		failCount:    c.statusFailCount,
	}
}

type conflictStatusPatchWriter struct {
	client.StatusWriter
	calls     *int
	failCount int
}

func (w *conflictStatusPatchWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	*w.calls++
	if *w.calls <= w.failCount {
		return apierrors.NewConflict(schema.GroupResource{Resource: "browsers"}, obj.GetName(), errors.New("conflict"))
	}
	return w.StatusWriter.Patch(ctx, obj, patch, opts...)
}

// ───────── retryBackoff ─────────

func TestRetryBackoffContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	retryBackoff(ctx, 0)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("retryBackoff took %v on cancelled context, expected < 50ms", elapsed)
	}
}

func TestRetryBackoffLargeAttemptNoPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retryBackoff(ctx, 100) // overflowed int without min(attempt, 10) guard
}

// ───────── retryUpdate / retryPatch conflict ─────────

func TestRetryUpdateConflictRetry(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	cc := &conflictPatchClient{Client: base, failCount: 1}
	r := NewBrowserReconciler(cc, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		if b.Labels == nil {
			b.Labels = map[string]string{}
		}
		b.Labels["k"] = "v"
	})
	if err != nil {
		t.Fatalf("expected success after conflict retry, got %v", err)
	}
	if cc.patchCalls < 2 {
		t.Fatalf("expected at least 2 patch calls, got %d", cc.patchCalls)
	}
}

func TestRetryUpdateConflictExhausted(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	cc := &conflictPatchClient{Client: base, failCount: defaultCfg.MaxRetries + 1}
	r := NewBrowserReconciler(cc, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryUpdate(context.Background(), brw, func(*browserv1.Browser) {})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	if cc.patchCalls != defaultCfg.MaxRetries {
		t.Fatalf("expected %d patch calls, got %d", defaultCfg.MaxRetries, cc.patchCalls)
	}
}

func TestRetryStatusUpdateConflictRetry(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	cc := &conflictStatusPatchClient{Client: base, statusFailCount: 1}
	r := NewBrowserReconciler(cc, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryStatusUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		b.Status.Phase = corev1.PodRunning
	})
	if err != nil {
		t.Fatalf("expected success after conflict retry, got %v", err)
	}
	if cc.statusPatchCalls < 2 {
		t.Fatalf("expected at least 2 status patch calls, got %d", cc.statusPatchCalls)
	}
}

func TestRetryPatchContextCancelledDuringBackoff(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}}
	base := newBrowserClient(scheme, brw)
	cc := &conflictPatchClient{Client: base, failCount: defaultCfg.MaxRetries + 1}
	r := NewBrowserReconciler(cc, store.NewBrowserConfigStore(), scheme, defaultCfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := r.retryUpdate(ctx, brw, func(*browserv1.Browser) {})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("retryUpdate took %v on cancelled context, expected < 200ms", elapsed)
	}
}

func TestReconcileFinalizerAddError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
			Labels:     map[string]string{browserv1.BrowserLabelKey: "wrong"},
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
}

func TestReconcilePendingStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
				browserv1.BrowserLabelKey:        "b1",
				browserv1.BrowserNameLabelKey:    "chrome",
				browserv1.BrowserVersionLabelKey: "120",
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
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
}

func TestReconcilePendingCreationTimeoutStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
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
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-defaultCfg.PodCreationTimeout - time.Second).UTC()),
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
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
}

func TestReconcilePendingWaitingStatusUpdateError(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
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
	r := NewBrowserReconciler(patchErrorClient{Client: base, statusPatchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted after sidecar terminated")
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(patchErrorClient{Client: base, patchErr: apierrors.NewInternalError(errors.New("patch"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(patchErrorClient{Client: base, getErr: apierrors.NewBadRequest("bad")}, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(patchErrorClient{Client: base, getErr: apierrors.NewBadRequest("bad")}, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(patchErrorClient{Client: base, getPodErr: apierrors.NewInternalError(errors.New("pod"))}, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme, defaultCfg)

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
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme, defaultCfg)

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

func TestHandleMissingPodQuotaExceededFailsBrowser(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	quotaErr := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "b1", errors.New("exceeded quota"))
	cl := errorClient{Client: base, createErr: quotaErr}
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

	res, err := r.handleMissingPod(context.Background(), brw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue, got %v", res.RequeueAfter)
	}

	if err := cl.Client.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after quota exceeded, got err=%v", err)
	}

	if err := cl.Client.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected no pod to be created, got err=%v", err)
	}
}

func TestHandleMissingPodQuotaExceededStatusUpdateError(t *testing.T) {
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
				browserv1.BrowserLabelKey:        "b1",
				browserv1.BrowserNameLabelKey:    "chrome",
				browserv1.BrowserVersionLabelKey: "120",
			},
		},
		Spec:   browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
		Status: browserv1.BrowserStatus{Phase: corev1.PodPending},
	}
	quotaErr := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "b1", errors.New("exceeded quota"))
	base := newBrowserClient(scheme, brw)
	cl := quotaCreatePatchErrorClient{
		Client:         base,
		quotaCreateErr: quotaErr,
		statusPatchErr: apierrors.NewInternalError(errors.New("patch")),
	}
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

	res, err := r.handleMissingPod(context.Background(), brw)
	if err == nil {
		t.Fatalf("expected error")
	}
	assertJitteredRequeue(t, res.RequeueAfter, mediumRetry)
}

func TestReconcileQuotaExceededDeletesBrowserOnNextReconcile(t *testing.T) {
	scheme := newBrowserScheme(t)
	cfgStore := store.NewBrowserConfigStore()
	spec := &configv1.BrowserVersionConfigSpec{Image: "img"}
	setStoreConfig(t, cfgStore, "ns/chrome:120", spec)

	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b1",
			Namespace: "ns",
		},
		Spec: browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	base := newBrowserClient(scheme, brw)
	quotaErr := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "b1", errors.New("exceeded quota"))
	cl := errorClient{Client: base, createErr: quotaErr}
	r := NewBrowserReconciler(cl, cfgStore, scheme, defaultCfg)

	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"}}

	_, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	got := &browserv1.Browser{}
	if err := cl.Client.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser after first reconcile: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected Failed after quota error, got %s", got.Status.Phase)
	}

	r2 := NewBrowserReconciler(cl.Client, cfgStore, scheme, defaultCfg)
	_, err = r2.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	if err := cl.Client.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); err == nil {
		t.Fatalf("expected browser to be deleted after second reconcile")
	}
}

type quotaCreatePatchErrorClient struct {
	client.Client
	quotaCreateErr error
	statusPatchErr error
}

func (q quotaCreatePatchErrorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if q.quotaCreateErr != nil {
		if _, ok := obj.(*corev1.Pod); ok {
			return q.quotaCreateErr
		}
	}
	return q.Client.Create(ctx, obj, opts...)
}

func (q quotaCreatePatchErrorClient) Status() client.StatusWriter {
	return &statusPatchErrorWriter{StatusWriter: q.Client.Status(), err: q.statusPatchErr}
}

func TestReconcilePodPendingCreationTimeoutFullCycle(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
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
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-defaultCfg.PodCreationTimeout - time.Second).UTC()),
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected pod to be deleted after reconcile, got err=%v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after reconcile, got err=%v", err)
	}
}

func TestReconcilePodPendingContainerTerminatedFullCycle(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
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
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "browser",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:   "OOMKilled",
							ExitCode: 137,
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected pod to be deleted after reconcile, got err=%v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after reconcile, got err=%v", err)
	}
}

func TestReconcilePodFailedFullCycle(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
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
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "OOMKilled",
			Message: "container exceeded memory limit",
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected pod to be deleted after reconcile, got err=%v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &browserv1.Browser{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected browser to be deleted after reconcile, got err=%v", err)
	}
}

func TestReconcilePodPendingInitContainerTerminated(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-setup",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "init container") {
		t.Fatalf("expected message to mention init container, got %q", got.Status.Message)
	}
}

func TestReconcilePodPendingInitContainerTerminatedExitZero(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-setup",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 0,
							Reason:   "Completed",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err != nil {
		t.Fatalf("expected pod to still exist, got %v", err)
	}
}

func TestReconcilePodPendingInitContainerCreationTimeout(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-defaultCfg.PodCreationTimeout - time.Second).UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-setup",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "PodInitializing",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "init container") {
		t.Fatalf("expected message to mention init container, got %q", got.Status.Message)
	}
}

func TestReconcilePodPendingInitContainerImagePullBackOff(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-setup",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ImagePullBackOff",
							Message: "back-off pulling image",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "init container") {
		t.Fatalf("expected message to mention init container, got %q", got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "ImagePullBackOff") {
		t.Fatalf("expected message to contain reason, got %q", got.Status.Message)
	}
}

func TestReconcilePodPendingInitContainerNoTimeoutYet(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-setup",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "PodInitializing",
						},
					},
				},
			},
		},
	}
	cl := newBrowserClient(scheme, brw, pod)
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err != nil {
		t.Fatalf("expected pod to still exist, got %v", err)
	}
}

func TestReconcilePodPendingEmptyStatusesWithInitRunning(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec:       browserv1.BrowserSpec{BrowserName: "chrome", BrowserVersion: "120"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "b1",
			Namespace:         "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-defaultCfg.PodCreationTimeout - time.Second).UTC()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-setup",
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
	r := NewBrowserReconciler(cl, store.NewBrowserConfigStore(), scheme, defaultCfg)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "ns", Name: "b1"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, &corev1.Pod{}); err == nil {
		t.Fatalf("expected pod to be deleted after init container timeout")
	}

	got := &browserv1.Browser{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get browser: %v", err)
	}
	if got.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected failed status, got %s", got.Status.Phase)
	}
}

func TestRetryStatusUpdateMaxConflict(t *testing.T) {
	scheme := newBrowserScheme(t)
	brw := &browserv1.Browser{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
	}
	base := newBrowserClient(scheme, brw)
	c := &alwaysConflictClient{Client: base}
	r := NewBrowserReconciler(c, store.NewBrowserConfigStore(), scheme, defaultCfg)

	err := r.retryStatusUpdate(context.Background(), brw, func(b *browserv1.Browser) {
		b.Status.Phase = corev1.PodRunning
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestContainerStatusesEqualPorts(t *testing.T) {
	withPorts := func(ports []browserv1.ContainerPort) []browserv1.ContainerStatus {
		return []browserv1.ContainerStatus{{Name: "c", Ports: ports}}
	}

	p1 := []browserv1.ContainerPort{{ContainerPort: 4444}}
	p2 := []browserv1.ContainerPort{{ContainerPort: 4445}}
	p3 := []browserv1.ContainerPort{{ContainerPort: 4444}, {ContainerPort: 5555}}

	if !containerStatusesEqual(withPorts(p1), withPorts(p1)) {
		t.Fatal("expected equal ports to be equal")
	}
	if containerStatusesEqual(withPorts(p1), withPorts(p3)) {
		t.Fatal("expected different port count to be unequal")
	}
	if containerStatusesEqual(withPorts(p1), withPorts(p2)) {
		t.Fatal("expected different port values to be unequal")
	}
}

func TestContainerStateEqualWaiting(t *testing.T) {
	a := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "Init", Message: "msg"}}
	b := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "Init", Message: "msg"}}
	c := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "Other"}}

	if !containerStateEqual(a, b) {
		t.Fatal("expected identical waiting states to be equal")
	}
	if containerStateEqual(a, c) {
		t.Fatal("expected different waiting reasons to be unequal")
	}
}

func TestApplySelenosisOptionsNilLabels(t *testing.T) {
	pod := &corev1.Pod{}
	opts := &SelenosisOptions{
		Labels: map[string]string{"env": "test"},
	}
	applySelenosisOptions(pod, opts)
	if pod.Labels["env"] != "test" {
		t.Fatalf("expected label env=test, got %v", pod.Labels)
	}
}

func TestMergeEnvVarsEmptyOverride(t *testing.T) {
	base := []corev1.EnvVar{{Name: "A", Value: "1"}}
	result := mergeEnvVars(base, nil)
	if len(result) != 1 || result[0].Name != "A" {
		t.Fatalf("expected base unchanged, got %v", result)
	}
}

func TestPodChangedPredicate(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Second))

	basePod := func() *corev1.Pod {
		return &corev1.Pod{
			Status: corev1.PodStatus{
				Phase:     corev1.PodRunning,
				PodIP:     "10.0.0.1",
				StartTime: &now,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "browser", Ready: true},
				},
				InitContainerStatuses: []corev1.ContainerStatus{
					{Name: "init", Ready: true},
				},
			},
		}
	}

	p := podChangedPredicate{}

	cases := []struct {
		name string
		old  client.Object
		new  client.Object
		want bool
	}{
		{
			name: "no changes",
			old:  basePod(),
			new:  basePod(),
			want: false,
		},
		{
			name: "phase changed",
			old:  basePod(),
			new: func() *corev1.Pod {
				pod := basePod()
				pod.Status.Phase = corev1.PodFailed
				return pod
			}(),
			want: true,
		},
		{
			name: "pod ip changed",
			old:  basePod(),
			new: func() *corev1.Pod {
				pod := basePod()
				pod.Status.PodIP = "10.0.0.2"
				return pod
			}(),
			want: true,
		},
		{
			name: "start time changed",
			old:  basePod(),
			new: func() *corev1.Pod {
				pod := basePod()
				pod.Status.StartTime = &later
				return pod
			}(),
			want: true,
		},
		{
			name: "start time nil to non-nil",
			old: func() *corev1.Pod {
				pod := basePod()
				pod.Status.StartTime = nil
				return pod
			}(),
			new:  basePod(),
			want: true,
		},
		{
			name: "container statuses changed",
			old:  basePod(),
			new: func() *corev1.Pod {
				pod := basePod()
				pod.Status.ContainerStatuses[0].Ready = false
				return pod
			}(),
			want: true,
		},
		{
			name: "init container statuses changed",
			old:  basePod(),
			new: func() *corev1.Pod {
				pod := basePod()
				pod.Status.InitContainerStatuses[0].Ready = false
				return pod
			}(),
			want: true,
		},
		{
			name: "deletion timestamp set",
			old:  basePod(),
			new: func() *corev1.Pod {
				pod := basePod()
				pod.DeletionTimestamp = &now
				return pod
			}(),
			want: true,
		},
		{
			name: "non-pod old object",
			old:  &corev1.ConfigMap{},
			new:  basePod(),
			want: true,
		},
		{
			name: "non-pod new object",
			old:  basePod(),
			new:  &corev1.ConfigMap{},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.Update(event.UpdateEvent{
				ObjectOld: tc.old,
				ObjectNew: tc.new,
			})
			if got != tc.want {
				t.Fatalf("Update() = %v, want %v", got, tc.want)
			}
		})
	}
}
