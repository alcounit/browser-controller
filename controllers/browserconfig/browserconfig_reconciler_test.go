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

func TestReconcileNotFound(t *testing.T) {
	scheme := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := NewBrowserConfigReconciler(cl, scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "missing"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestReconcileAddsFinalizer(t *testing.T) {
	scheme := newTestScheme(t)
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "default",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	r := NewBrowserConfigReconciler(cl, scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &configv1.BrowserConfig{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "cfg"}, got); err != nil {
		t.Fatalf("failed to get config: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, browserConfigFinalizer) {
		t.Fatalf("expected finalizer to be set")
	}
}

func TestReconcileRemovesFinalizerOnDelete(t *testing.T) {
	scheme := newTestScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "cfg",
			Namespace:         "default",
			Finalizers:        []string{browserConfigFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	r := NewBrowserConfigReconciler(cl, scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := &configv1.BrowserConfig{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "cfg"}, got); err == nil {
		if controllerutil.ContainsFinalizer(got, browserConfigFinalizer) {
			t.Fatalf("expected finalizer to be removed")
		}
	}
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
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "default",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	cc := &conflictClient{Client: base, failCount: 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cc.patchCalls < 2 {
		t.Fatalf("expected at least 2 patch calls, got %d", cc.patchCalls)
	}

	got := &configv1.BrowserConfig{}
	if err := base.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "cfg"}, got); err != nil {
		t.Fatalf("failed to get config: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, browserConfigFinalizer) {
		t.Fatalf("expected finalizer to be set after conflict retry")
	}
}

func TestRetryPatchConflictExhausted(t *testing.T) {
	scheme := newTestScheme(t)
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "default",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	cc := &conflictClient{Client: base, failCount: maxRetries + 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	if cc.patchCalls != maxRetries {
		t.Fatalf("expected %d patch calls, got %d", maxRetries, cc.patchCalls)
	}
}

func TestRetryPatchContextCancelled(t *testing.T) {
	scheme := newTestScheme(t)
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "default",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	cc := &conflictClient{Client: base, failCount: maxRetries + 1}
	r := NewBrowserConfigReconciler(cc, scheme)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
}

func TestReconcileGetError(t *testing.T) {
	scheme := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := NewBrowserConfigReconciler(errorClient{Client: cl, getErr: errors.New("boom")}, scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != mediumRetry {
		t.Fatalf("expected medium retry, got %v", res.RequeueAfter)
	}
}

func TestReconcileAddFinalizerPatchError(t *testing.T) {
	scheme := newTestScheme(t)
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "default",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	r := NewBrowserConfigReconciler(errorClient{Client: base, patchErr: errors.New("patch")}, scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != shortRetry {
		t.Fatalf("expected short retry, got %v", res.RequeueAfter)
	}
}

func TestReconcileRemoveFinalizerPatchError(t *testing.T) {
	scheme := newTestScheme(t)
	now := metav1.NewTime(time.Now().UTC())
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "cfg",
			Namespace:         "default",
			Finalizers:        []string{browserConfigFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	r := NewBrowserConfigReconciler(errorClient{Client: base, patchErr: errors.New("patch")}, scheme)

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "cfg"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.RequeueAfter != shortRetry {
		t.Fatalf("expected short retry, got %v", res.RequeueAfter)
	}
}

func TestRetryPatchContextCancelledDuringBackoff(t *testing.T) {
	scheme := newTestScheme(t)
	cfg := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"chrome": {"120": {Image: "img"}},
			},
		},
	}
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
