package browserconfig

import (
	"context"
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
	browserConfigFinalizer string = "browserconfig.selenosis.io/finalizer"
	shortRetry                    = time.Second * 5
	mediumRetry                   = time.Second * 10
)

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

func (r BrowserConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
			controllerutil.RemoveFinalizer(browserConfig, browserConfigFinalizer)
			if err := r.client.Update(ctx, browserConfig); err != nil {
				log.Error(err, "failed to remove finalizer")
				return ctrl.Result{RequeueAfter: shortRetry}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(browserConfig, browserConfigFinalizer) {
		controllerutil.AddFinalizer(browserConfig, browserConfigFinalizer)
		if err := r.client.Update(ctx, browserConfig); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: shortRetry}, err
		}
	}

	return ctrl.Result{}, nil
}
