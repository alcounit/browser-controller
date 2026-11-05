package browserconfig

import (
	"context"
	"errors"
	"testing"
	"time"

	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	if err := configv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add configv1 scheme: %v", err)
	}
	return scheme
}

func liveConfig() *configv1.BrowserConfig {
	return &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
}

func deletingConfig() *configv1.BrowserConfig {
	cfg := liveConfig()
	now := metav1.NewTime(time.Now().UTC())
	cfg.Finalizers = []string{browserConfigFinalizer}
	cfg.DeletionTimestamp = &now
	return cfg
}

func reconcileCfg(ctx context.Context, r *BrowserConfigReconciler) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
}

func assertFinalizerGone(t *testing.T, cl client.Client) {
	t.Helper()
	got := &configv1.BrowserConfig{}
	err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "cfg"}, got)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		t.Fatalf("failed to get config: %v", err)
	}
	if controllerutil.ContainsFinalizer(got, browserConfigFinalizer) {
		t.Fatalf("expected finalizer to be removed")
	}
}

func TestReconcileNotFound(t *testing.T) {
	scheme := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := NewBrowserConfigReconciler(cl, scheme)

	res, err := reconcileCfg(context.Background(), r)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !res.IsZero() {
		t.Fatalf("expected empty result, got %+v", res)
	}
}

func TestReconcileDoesNotAddFinalizer(t *testing.T) {
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(liveConfig()).Build()
	r := NewBrowserConfigReconciler(errorClient{Client: base, patchErr: errors.New("unexpected patch")}, scheme)

	res, err := reconcileCfg(context.Background(), r)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !res.IsZero() {
		t.Fatalf("expected empty result, got %+v", res)
	}

	got := &configv1.BrowserConfig{}
	if err := base.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "cfg"}, got); err != nil {
		t.Fatalf("failed to get config: %v", err)
	}
	if controllerutil.ContainsFinalizer(got, browserConfigFinalizer) {
		t.Fatalf("expected finalizer not to be added")
	}
}

func TestReconcileRemovesLegacyFinalizerOnDelete(t *testing.T) {
	scheme := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingConfig()).Build()
	r := NewBrowserConfigReconciler(cl, scheme)

	if _, err := reconcileCfg(context.Background(), r); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertFinalizerGone(t, cl)
}

type errorClient struct {
	client.Client
	getErr   error
	patchErr error
}

func (e errorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if e.getErr != nil {
		return e.getErr
	}
	return e.Client.Get(ctx, key, obj, opts...)
}

func (e errorClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if e.patchErr != nil {
		return e.patchErr
	}
	return e.Client.Patch(ctx, obj, patch, opts...)
}

type conflictClient struct {
	client.Client
	patchCalls int
	failCount  int
}

func (c *conflictClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patchCalls++
	if c.patchCalls <= c.failCount {
		return apierrors.NewConflict(configv1.Resource("browserconfigs"), obj.GetName(), errors.New("conflict"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestRetryPatchConflictThenSuccess(t *testing.T) {
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingConfig()).Build()
	cc := &conflictClient{Client: base, failCount: 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	if _, err := reconcileCfg(context.Background(), r); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cc.patchCalls < 2 {
		t.Fatalf("expected at least 2 patch calls, got %d", cc.patchCalls)
	}
	assertFinalizerGone(t, base)
}

func TestRetryPatchConflictExhausted(t *testing.T) {
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingConfig()).Build()
	cc := &conflictClient{Client: base, failCount: maxRetries + 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	res, err := reconcileCfg(context.Background(), r)
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	if !res.IsZero() {
		t.Fatalf("expected empty result with error, got %+v", res)
	}
	if cc.patchCalls != maxRetries {
		t.Fatalf("expected %d patch calls, got %d", maxRetries, cc.patchCalls)
	}
}

func TestRetryPatchContextCancelled(t *testing.T) {
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingConfig()).Build()
	cc := &conflictClient{Client: base, failCount: maxRetries + 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := reconcileCfg(ctx, r); err == nil {
		t.Fatalf("expected error from cancelled context")
	}
}

func TestReconcileGetError(t *testing.T) {
	scheme := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := NewBrowserConfigReconciler(errorClient{Client: cl, getErr: errors.New("boom")}, scheme)

	res, err := reconcileCfg(context.Background(), r)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !res.IsZero() {
		t.Fatalf("expected empty result with error, got %+v", res)
	}
}

func TestReconcileRemoveFinalizerPatchError(t *testing.T) {
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingConfig()).Build()
	r := NewBrowserConfigReconciler(errorClient{Client: base, patchErr: errors.New("patch")}, scheme)

	res, err := reconcileCfg(context.Background(), r)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !res.IsZero() {
		t.Fatalf("expected empty result with error, got %+v", res)
	}
}

func TestRetryPatchContextCancelledDuringBackoff(t *testing.T) {
	scheme := newTestScheme(t)
	cfg := liveConfig()
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	cc := &conflictClient{Client: base, failCount: maxRetries + 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := r.retryPatch(ctx, cfg, func(*configv1.BrowserConfig) {})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("retryPatch took %v on cancelled context, expected < 200ms", elapsed)
	}
}

type secondGetErrorClient struct {
	client.Client
	calls int
}

func (c *secondGetErrorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.calls++
	if c.calls > 1 {
		return errors.New("get")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestRetryPatchGetError(t *testing.T) {
	scheme := newTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deletingConfig()).Build()
	r := NewBrowserConfigReconciler(&secondGetErrorClient{Client: base}, scheme)

	res, err := reconcileCfg(context.Background(), r)
	if err == nil {
		t.Fatalf("expected error from retryPatch get")
	}
	if !res.IsZero() {
		t.Fatalf("expected empty result with error, got %+v", res)
	}
}
