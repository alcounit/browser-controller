package store

import (
	"context"
	"fmt"
	"strings"
	"sync"

	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	"github.com/go-logr/logr"
	kcache "k8s.io/client-go/tools/cache"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// BrowserConfigStore keeps an in-memory cache of BrowserVersionConfig objects.
type BrowserConfigStore struct {
	mu     sync.RWMutex
	config map[string]*configv1.BrowserVersionConfigSpec // key = namespace/browser:version
	cache  crcache.Cache
	log    logr.Logger
}

func NewBrowserConfigStore() *BrowserConfigStore {
	return &BrowserConfigStore{
		config: make(map[string]*configv1.BrowserVersionConfigSpec),
	}
}

// WithCache injects the controller-runtime cache and logger into the store.
func (s *BrowserConfigStore) WithCache(c crcache.Cache, log logr.Logger) manager.Runnable {
	s.cache = c
	s.log = log.WithName("browserconfig-store")
	return s
}

// keyFor builds the unique cache key for a browser config.
func keyFor(namespace, browser, version string) string {
	return fmt.Sprintf("%s/%s:%s", namespace, strings.ToLower(browser), strings.ToLower(version))
}

// Start wires up informer from mgr.GetCache and listens for add/update/delete events.
func (s *BrowserConfigStore) Start(ctx context.Context) error {
	if s.cache == nil {
		return fmt.Errorf("cache not initialized in BrowserConfigStore")
	}

	informer, err := s.cache.GetInformer(ctx, &configv1.BrowserConfig{})
	if err != nil {
		return fmt.Errorf("failed to get informer for BrowserConfig: %w", err)
	}

	informer.AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { s.onAddOrUpdate(obj, s.log) },
		UpdateFunc: func(_, newObj any) { s.onAddOrUpdate(newObj, s.log) },
		DeleteFunc: func(obj any) { s.onDelete(obj, s.log) },
	})

	// Wait until cache is synced
	if !kcache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("failed to sync BrowserConfig informer cache")
	}

	s.log.Info("BrowserConfigStore successfully started and synced")
	<-ctx.Done()
	return nil
}

func (s *BrowserConfigStore) onAddOrUpdate(obj any, log logr.Logger) {
	var bc *configv1.BrowserConfig
	switch t := obj.(type) {
	case *configv1.BrowserConfig:
		bc = t
	case kcache.DeletedFinalStateUnknown:
		if v, ok := t.Obj.(*configv1.BrowserConfig); ok {
			bc = v
		}
	default:
		return
	}

	if bc == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bcCopy := bc.DeepCopy()
	bcCopy.Spec.MergeWithTemplate()

	for browserName, versions := range bcCopy.Spec.Browsers {
		for version, cfg := range versions {
			key := keyFor(bcCopy.Namespace, browserName, version)
			s.config[key] = cfg
			log.Info("BrowserConfig added/updated", "key", key)
		}
	}
}

func (s *BrowserConfigStore) onDelete(obj any, log logr.Logger) {
	var bc *configv1.BrowserConfig
	switch t := obj.(type) {
	case *configv1.BrowserConfig:
		bc = t
	case kcache.DeletedFinalStateUnknown:
		if v, ok := t.Obj.(*configv1.BrowserConfig); ok {
			bc = v
		}
	default:
		return
	}

	if bc == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for browserName, versions := range bc.Spec.Browsers {
		for version := range versions {
			key := keyFor(bc.Namespace, browserName, version)
			delete(s.config, key)
			log.Info("BrowserConfig deleted", "key", key)
		}
	}
}

// Get retrieves BrowserVersionConfig from the in-memory store.
func (s *BrowserConfigStore) Get(namespace, browserName, version string) (*configv1.BrowserVersionConfigSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, exists := s.config[keyFor(namespace, browserName, version)]
	return cfg, exists
}
