package browserconfig

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logger "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	browserConfigFinalizer = "browserconfig.selenosis.io/finalizer"
	maxRetries             = 3
	shortRetry             = time.Second * 5
	mediumRetry            = time.Second * 10
)

// +kubebuilder:rbac:groups=browserconfig.selenosis.io,resources=browserconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=browserconfig.selenosis.io,resources=browserconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=browserconfig.selenosis.io,resources=browserconfigs/finalizers,verbs=update

type BrowserConfigReconciler struct {
	client client.Client
	scheme *runtime.Scheme
}

func NewBrowserConfigReconciler(client client.Client, scheme *runtime.Scheme) *BrowserConfigReconciler {
	return &BrowserConfigReconciler{
		client: client,
		scheme: scheme,
	}
}

func (r *BrowserConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1.BrowserConfig{}).
		Complete(r)
}

func (r *BrowserConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logger.FromContext(ctx)

	browserConfig := &configv1.BrowserConfig{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, browserConfig); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get BrowserConfig object")
		return ctrl.Result{RequeueAfter: mediumRetry}, err
	}

	if !browserConfig.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(browserConfig, browserConfigFinalizer) {
			if err := r.retryPatch(ctx, browserConfig, func(bc *configv1.BrowserConfig) {
				controllerutil.RemoveFinalizer(bc, browserConfigFinalizer)
			}); err != nil {
				log.Error(err, "failed to remove finalizer")
				return ctrl.Result{RequeueAfter: shortRetry}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(browserConfig, browserConfigFinalizer) {
		if err := r.retryPatch(ctx, browserConfig, func(bc *configv1.BrowserConfig) {
			controllerutil.AddFinalizer(bc, browserConfigFinalizer)
		}); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: shortRetry}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *BrowserConfigReconciler) retryPatch(ctx context.Context, bc *configv1.BrowserConfig, mutate func(*configv1.BrowserConfig)) error {
	nn := types.NamespacedName{Name: bc.Name, Namespace: bc.Namespace}

	for i := range maxRetries {
		current := &configv1.BrowserConfig{}
		if err := r.client.Get(ctx, nn, current); err != nil {
			return err
		}

		before := current.DeepCopy()
		mutate(current)

		err := r.client.Patch(ctx, current, client.MergeFrom(before))
		if err == nil {
			return nil
		}

		if !errors.IsConflict(err) {
			return err
		}

		base := time.Millisecond * time.Duration(100*(1<<min(i, 10)))
		j := time.Duration(rand.Int64N(int64(50 * time.Millisecond)))
		t := time.NewTimer(base + j)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}

	return fmt.Errorf("failed to patch BrowserConfig after %d attempts: version conflict", maxRetries)
}
