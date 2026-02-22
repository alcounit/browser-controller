package store

import (
	"context"
	"errors"
	"testing"
	"time"

	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKeyForLowercases(t *testing.T) {
	key := keyFor("Ns", "Chrome", "RC1")
	if key != "Ns/chrome:rc1" {
		t.Fatalf("unexpected key: %q", key)
	}
}

func TestBrowserConfigStoreWithCache(t *testing.T) {
	store := NewBrowserConfigStore()
	fc := &fakeCache{}

	r := store.WithCache(fc, logr.Discard())
	if r != store {
		t.Fatalf("expected WithCache to return store runnable")
	}
	if store.cache != fc {
		t.Fatalf("expected cache to be set")
	}
}

func TestBrowserConfigStoreOnAddOrUpdateMergesAndStores(t *testing.T) {
	templateEnv := []corev1.EnvVar{{Name: "TZ", Value: "UTC"}}
	overrideEnv := []corev1.EnvVar{{Name: "TZ", Value: "PST"}, {Name: "CUSTOM", Value: "1"}}
	bc := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "ns",
		},
		Spec: configv1.BrowserConfigSpec{
			Template: &configv1.Template{
				ImagePullPolicy: corev1.PullIfNotPresent,
				Env:             &templateEnv,
			},
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"Chrome": {
					"144.0": {
						Image: "img",
						Env:   &overrideEnv,
					},
				},
			},
		},
	}

	store := NewBrowserConfigStore()
	store.onAddOrUpdate(bc, logr.Discard())

	cfg, ok := store.Get("ns", "CHROME", "144.0")
	if !ok || cfg == nil {
		t.Fatalf("expected config to be stored")
	}
	if cfg.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("expected imagePullPolicy to be merged from template, got %q", cfg.ImagePullPolicy)
	}
	if cfg.Env == nil || len(*cfg.Env) != 2 {
		t.Fatalf("expected env to be merged, got %+v", cfg.Env)
	}
}

func TestBrowserConfigStoreOnAddOrUpdateDeepCopyIsolated(t *testing.T) {
	bc := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "ns",
		},
		Spec: configv1.BrowserConfigSpec{
			Template: &configv1.Template{
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"Chrome": {
					"144.0": {Image: "img"},
				},
			},
		},
	}

	store := NewBrowserConfigStore()
	store.onAddOrUpdate(bc, logr.Discard())

	// Mutate original after add.
	bc.Spec.Browsers["Chrome"]["144.0"].ImagePullPolicy = corev1.PullNever

	cfg, ok := store.Get("ns", "chrome", "144.0")
	if !ok || cfg == nil {
		t.Fatalf("expected config to be stored")
	}
	if cfg.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("expected store to keep merged value from copy, got %q", cfg.ImagePullPolicy)
	}
}

func TestBrowserConfigStoreOnAddOrUpdateDeletedFinalStateUnknown(t *testing.T) {
	bc := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "ns",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"Firefox": {"120.0": {Image: "img"}},
			},
		},
	}

	store := NewBrowserConfigStore()
	store.onAddOrUpdate(cache.DeletedFinalStateUnknown{Obj: bc}, logr.Discard())

	if _, ok := store.Get("ns", "firefox", "120.0"); !ok {
		t.Fatalf("expected config to be stored from DeletedFinalStateUnknown")
	}
}

func TestBrowserConfigStoreOnAddOrUpdateDeletedFinalStateUnknownNilObj(t *testing.T) {
	store := NewBrowserConfigStore()
	store.onAddOrUpdate(cache.DeletedFinalStateUnknown{Obj: (*configv1.BrowserConfig)(nil)}, logr.Discard())
}

func TestBrowserConfigStoreOnAddOrUpdateIgnoresUnknownType(t *testing.T) {
	store := NewBrowserConfigStore()
	store.onAddOrUpdate("not-a-config", logr.Discard())
	if _, ok := store.Get("ns", "chrome", "144.0"); ok {
		t.Fatalf("expected no configs to be stored")
	}
}

func TestBrowserConfigStoreOnDeleteRemovesKeys(t *testing.T) {
	bc := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "ns",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"Chrome":  {"144.0": {Image: "img"}},
				"Firefox": {"120.0": {Image: "img"}},
			},
		},
	}

	store := NewBrowserConfigStore()
	store.onAddOrUpdate(bc, logr.Discard())

	store.onDelete(bc, logr.Discard())

	if _, ok := store.Get("ns", "chrome", "144.0"); ok {
		t.Fatalf("expected chrome config to be deleted")
	}
	if _, ok := store.Get("ns", "firefox", "120.0"); ok {
		t.Fatalf("expected firefox config to be deleted")
	}
}

func TestBrowserConfigStoreOnDeleteDeletedFinalStateUnknown(t *testing.T) {
	bc := &configv1.BrowserConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cfg",
			Namespace: "ns",
		},
		Spec: configv1.BrowserConfigSpec{
			Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
				"Safari": {"17.0": {Image: "img"}},
			},
		},
	}

	store := NewBrowserConfigStore()
	store.onAddOrUpdate(bc, logr.Discard())

	store.onDelete(cache.DeletedFinalStateUnknown{Obj: bc}, logr.Discard())
	if _, ok := store.Get("ns", "safari", "17.0"); ok {
		t.Fatalf("expected config to be deleted from DeletedFinalStateUnknown")
	}
}

func TestBrowserConfigStoreOnDeleteDeletedFinalStateUnknownNilObj(t *testing.T) {
	store := NewBrowserConfigStore()
	store.onDelete(cache.DeletedFinalStateUnknown{Obj: (*configv1.BrowserConfig)(nil)}, logr.Discard())
}

func TestBrowserConfigStoreOnDeleteIgnoresUnknownType(t *testing.T) {
	store := NewBrowserConfigStore()
	store.onDelete("not-a-config", logr.Discard())
}

func TestBrowserConfigStoreGetMissing(t *testing.T) {
	store := NewBrowserConfigStore()
	if _, ok := store.Get("ns", "missing", "1"); ok {
		t.Fatalf("expected missing config to return false")
	}
}

func TestBrowserConfigStoreStartNoCache(t *testing.T) {
	store := NewBrowserConfigStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := store.Start(ctx); err == nil {
		t.Fatalf("expected error when cache is nil")
	}
}

func TestBrowserConfigStoreStartGetInformerError(t *testing.T) {
	store := NewBrowserConfigStore()
	fc := &fakeCache{getInformerErr: errors.New("boom")}
	store.WithCache(fc, logr.Discard())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := store.Start(ctx); err == nil {
		t.Fatalf("expected error from GetInformer")
	}
}

func TestBrowserConfigStoreStartWaitForCacheSyncFails(t *testing.T) {
	store := NewBrowserConfigStore()
	fi := &fakeInformer{synced: false}
	fc := &fakeCache{informer: fi}
	store.WithCache(fc, logr.Discard())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Start(ctx); err == nil {
		t.Fatalf("expected error when cache sync fails")
	}
}

func TestBrowserConfigStoreStartSuccess(t *testing.T) {
	store := NewBrowserConfigStore()
	syncCh := make(chan struct{})
	handlerCh := make(chan struct{})
	fi := &fakeInformer{synced: true, syncedCalled: syncCh, handlerSet: handlerCh}
	fc := &fakeCache{informer: fi}
	store.WithCache(fc, logr.Discard())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- store.Start(ctx)
	}()

	select {
	case <-syncCh:
	case <-time.After(1 * time.Second):
		t.Fatalf("expected informer HasSynced to be called")
	}

	select {
	case <-handlerCh:
	case <-time.After(1 * time.Second):
		t.Fatalf("expected handler to be registered")
	}

	if fi.handler != nil {
		bc := &configv1.BrowserConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "ns"},
			Spec: configv1.BrowserConfigSpec{
				Browsers: map[string]map[string]*configv1.BrowserVersionConfigSpec{
					"Chrome": {"144.0": {Image: "img"}},
				},
			},
		}
		fi.handler.OnAdd(bc, false)
		fi.handler.OnUpdate(nil, bc)
		fi.handler.OnDelete(bc)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("expected Start to return nil, got %v", err)
	}
	if !fi.addHandlerCalled {
		t.Fatalf("expected event handler to be registered")
	}
}

type fakeCache struct {
	informer       crcache.Informer
	getInformerErr error
}

func (f *fakeCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return nil
}

func (f *fakeCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}

func (f *fakeCache) GetInformer(ctx context.Context, obj client.Object, opts ...crcache.InformerGetOption) (crcache.Informer, error) {
	if f.getInformerErr != nil {
		return nil, f.getInformerErr
	}
	return f.informer, nil
}

func (f *fakeCache) GetInformerForKind(ctx context.Context, gvk schema.GroupVersionKind, opts ...crcache.InformerGetOption) (crcache.Informer, error) {
	return f.informer, nil
}

func (f *fakeCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	return nil
}

func (f *fakeCache) Start(ctx context.Context) error {
	return nil
}

func (f *fakeCache) WaitForCacheSync(ctx context.Context) bool {
	return true
}

func (f *fakeCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return nil
}

type fakeInformer struct {
	synced           bool
	addHandlerCalled bool
	syncedCalled     chan struct{}
	handlerSet       chan struct{}
	handler          cache.ResourceEventHandler
}

func (f *fakeInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	f.addHandlerCalled = true
	f.handler = handler
	if f.handlerSet != nil {
		close(f.handlerSet)
	}
	return fakeHandlerReg{synced: f.synced}, nil
}

func (f *fakeInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return f.AddEventHandler(handler)
}

func (f *fakeInformer) RemoveEventHandler(cache.ResourceEventHandlerRegistration) error {
	return nil
}

func (f *fakeInformer) AddIndexers(cache.Indexers) error {
	return nil
}

func (f *fakeInformer) HasSynced() bool {
	if f.syncedCalled != nil {
		select {
		case <-f.syncedCalled:
		default:
			close(f.syncedCalled)
		}
	}
	return f.synced
}

func (f *fakeInformer) IsStopped() bool {
	return false
}

type fakeHandlerReg struct {
	synced bool
}

func (r fakeHandlerReg) HasSynced() bool {
	return r.synced
}
