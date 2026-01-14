package browserconfig

import (
	"context"
	"errors"
	"testing"
	"time"

	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	corev1 "k8s.io/api/core/v1"
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
	getErr    error
	updateErr error
}

func (e errorClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if e.getErr != nil {
		return e.getErr
	}
	return e.Client.Get(ctx, key, obj, opts...)
}

func (e errorClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if e.updateErr != nil {
		return e.updateErr
	}
	return e.Client.Update(ctx, obj, opts...)
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

func TestReconcileAddFinalizerUpdateError(t *testing.T) {
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
	r := NewBrowserConfigReconciler(errorClient{Client: base, updateErr: errors.New("update")}, scheme)

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

func TestReconcileRemoveFinalizerUpdateError(t *testing.T) {
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
	r := NewBrowserConfigReconciler(errorClient{Client: base, updateErr: errors.New("update")}, scheme)

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
