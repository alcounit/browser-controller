package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	browserv1 "github.com/alcounit/browser-controller/apis/browser/v1"
	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	"github.com/alcounit/browser-controller/store"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logger "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	browserPodFinalizer = "browserpod.selenosis.io/finalizer"

	maxRetries         = 5
	mediumRetry        = time.Second * 10
	periodicReconcile  = time.Second * 30
	quickCheck         = time.Second * 3
	podDeletionTimeout = time.Minute * 5
	podCreationTimeout = time.Minute * 5

	browserContainerName = "browser"
	sidecarContainerName = "seleniferous"

	selenosisOptionsAnnotationKey = "selenosis.io/options"
)

type SelenosisOptions struct {
	Labels     map[string]string          `json:"labels,omitempty"`
	Containers map[string]ContainerOption `json:"containers,omitempty"`
}

type ContainerOption struct {
	Env map[string]string `json:"env,omitempty"`
}

// +kubebuilder:rbac:groups=selenosis.io,resources=browsers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=selenosis.io,resources=browsers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=selenosis.io,resources=browsers/finalizers,verbs=update

// BrowserReconciler reconciles Browser resources
type BrowserReconciler struct {
	client client.Client
	config *store.BrowserConfigStore
	scheme *runtime.Scheme
}

func NewBrowserReconciler(client client.Client, config *store.BrowserConfigStore, scheme *runtime.Scheme) *BrowserReconciler {
	return &BrowserReconciler{
		client: client,
		config: config,
		scheme: scheme,
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *BrowserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&browserv1.Browser{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

// Reconcile synchronizes the state of Browser and its Pod
func (r *BrowserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logger.FromContext(ctx)

	log.Info("start reconcile Browser")

	// get the Browser resource
	browser := &browserv1.Browser{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, browser); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Browser not found. Ignoring since must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get Browser")
		return ctrl.Result{}, err
	}

	// check if Browser deletion timestamp is set, if set handle deletion
	if !browser.DeletionTimestamp.IsZero() {
		log.Info("deleting Browser")
		return r.handleDeletion(ctx, browser)
	}

	// check if Browser is in Failed state, remove finalizer gc will take care Browser
	if browser.Status.Phase == corev1.PodFailed {
		// Remove finalizer
		if controllerutil.ContainsFinalizer(browser, browserPodFinalizer) {
			if err := r.retryUpdate(ctx, browser, func(b *browserv1.Browser) {
				controllerutil.RemoveFinalizer(b, browserPodFinalizer)
			}); err != nil {
				log.Error(err, "error removing Browser pod finalizer")
				return ctrl.Result{RequeueAfter: mediumRetry}, err
			}
		}
		log.Info("Browser is in Failed state, nothing to do")
		return ctrl.Result{}, nil
	}

	// ensure finalizer is set
	if !controllerutil.ContainsFinalizer(browser, browserPodFinalizer) {
		if err := r.retryUpdate(ctx, browser, func(b *browserv1.Browser) {
			controllerutil.AddFinalizer(b, browserPodFinalizer)
		}); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
		log.Info("adding finalizer to Browser")
	}

	// ensure label selenosis.io/browser.name exists
	if browser.Labels["selenosis.io/browser"] != browser.Name {
		if err := r.retryUpdate(ctx, browser, func(b *browserv1.Browser) {
			if b.Labels == nil {
				b.Labels = map[string]string{}
			}
			b.Labels["selenosis.io/browser"] = b.Name
			b.Labels["selenosis.io/browser.name"] = b.Spec.BrowserName
			b.Labels["selenosis.io/browser.version"] = b.Spec.BrowserVersion
		}); err != nil {
			log.Error(err, "failed to update Browser with name label")
			return ctrl.Result{RequeueAfter: mediumRetry}, err
		}
		log.Info("label selenosis.io/browser.name assigned to Browser")
	}

	// Set Pending status if not set
	if browser.Status.Phase == "" {
		if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
			b.Status.Phase = corev1.PodPending

		}); err != nil {
			log.Error(err, "failed to set initial Browser status")
			return ctrl.Result{}, err
		}
		log.Info("Browser status set to Pending")
	}

	log = log.WithValues("browserName", browser.Spec.BrowserName, "browserVersion", browser.Spec.BrowserVersion)

	//get the associated Pod
	pod := &corev1.Pod{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: browser.GetName(), Namespace: browser.GetNamespace()}, pod); err != nil {
		if errors.IsNotFound(err) {
			if browser.Status.Phase == corev1.PodFailed {
				log.Info("Browser is Failed state, browser pod not found. Ignoring since must be deleted")
				return ctrl.Result{}, nil
			}

			log.Info("Browser pod not found, creating new Browser pod")
			return r.handleMissingPod(ctx, browser)
		}

		log.Error(err, "error getting Browser pod")
		return ctrl.Result{}, err
	}

	// Handle pod being deleted
	if !pod.DeletionTimestamp.IsZero() && browser.DeletionTimestamp.IsZero() {
		log.Info("Browser Pod is being deleted, deleting Browser resource")
		return r.deleteBrowser(ctx, browser)
	}

	// Handle failed pod
	if pod.Status.Phase == corev1.PodFailed {

		if err := r.deletePod(ctx, pod); err != nil {
			log.Info("deleting Browser Pod")
			return ctrl.Result{RequeueAfter: mediumRetry}, err
		}

		if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
			b.Status.Phase = corev1.PodFailed
			b.Status.Message = fmt.Sprintf("pod has failed with reason: %s - %s", pod.Status.Reason, pod.Status.Message)
			log.Info("Browser Pod has failed", "reason", pod.Status.Reason, "message", pod.Status.Message)
		}); err != nil {
			log.Error(err, "failed to update Browser status to Failed")
			return ctrl.Result{RequeueAfter: mediumRetry}, err
		}
	}

	if pod.Status.Phase == corev1.PodPending {

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil {
				if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
					b.Status.Phase = corev1.PodFailed
					b.Status.Message = fmt.Sprintf("pod container %s terminated", cs.Name)

					log.Info("Browser Pod container terminated",
						"container",
						cs.Name,
						"reason",
						cs.State.Terminated.Reason,
						"message",
						pod.Status.Message,
						"exitCode",
						cs.State.Terminated.ExitCode)
				}); err != nil {
					return ctrl.Result{RequeueAfter: mediumRetry}, err
				}
				return ctrl.Result{}, nil
			}

			if cs.State.Waiting != nil {
				if !pod.CreationTimestamp.IsZero() {
					podAge := time.Since(pod.CreationTimestamp.Time)
					if podAge > podCreationTimeout {
						log.Info("Browser Pod creation timeout exceeded", "age", podAge.String(), "podStatus", pod.Status.Phase, "container", cs.Name)

						if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
							b.Status.Phase = corev1.PodFailed
							b.Status.Message = fmt.Sprintf(
								"pod creation timeout exceeded after %s",
								podCreationTimeout.String())
						}); err != nil {
							return ctrl.Result{RequeueAfter: mediumRetry}, err
						}
						return ctrl.Result{}, nil
					}
				}

				reason := cs.State.Waiting.Reason
				if reason != "ContainerCreating" && reason != "PodInitializing" {
					log.Info("Browser Pod container not ready", "container", cs.Name, "reason", reason, "message", cs.State.Waiting.Message, "podStatus", pod.Status.Phase)

					if err := r.deletePod(ctx, pod); err != nil {
						return ctrl.Result{RequeueAfter: mediumRetry}, err
					}

					if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
						b.Status.Phase = corev1.PodFailed
						b.Status.Message = fmt.Sprintf(
							"pod container %s failed: %s - %s",
							cs.Name, reason, cs.State.Waiting.Message)
					}); err != nil {
						return ctrl.Result{RequeueAfter: mediumRetry}, err
					}
					return ctrl.Result{}, nil
				}
			}
		}
	}

	return r.updateBrowserStatus(ctx, browser, pod)
}

// handleDeletion processes Browser resource deletion
func (r *BrowserReconciler) handleDeletion(ctx context.Context, browser *browserv1.Browser) (ctrl.Result, error) {
	log := logger.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(browser, browserPodFinalizer) {
		log.Info("Browser finalizer is not set, resource will be deleted during next reconcile")
		return ctrl.Result{}, nil
	}

	// Get the pod
	pod := &corev1.Pod{}
	err := r.client.Get(ctx, types.NamespacedName{Name: browser.GetName(), Namespace: browser.GetNamespace()}, pod)

	// Delete pod if exists
	if err == nil {
		if pod.DeletionTimestamp.IsZero() {
			log.Info("deleting associated pod")

			var deleteOptions []client.DeleteOption
			if pod.Status.Phase == corev1.PodFailed {
				deleteOptions = append(deleteOptions, client.GracePeriodSeconds(0))
				log.Info("using force delete for failed pod")
			}

			if err := r.client.Delete(ctx, pod, deleteOptions...); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "failed to delete Browser pod")
				return ctrl.Result{RequeueAfter: mediumRetry}, err
			}
		}

		// Check if pod deletion is taking too long
		if pod.DeletionTimestamp != nil {
			deletionTime := pod.DeletionTimestamp.Time
			if time.Since(deletionTime) > podDeletionTimeout {
				log.Info("Pod deletion is taking too long, attempting force delete")
				if err := r.deletePod(ctx, pod); err != nil {
					log.Error(err, "Failed to force delete pod after timeout")
					// Continue anyway to remove finalizer
				}
			} else {
				// Wait for pod to be deleted
				log.Info("waiting for pod to be deleted")
				return ctrl.Result{RequeueAfter: quickCheck}, nil
			}
		} else {
			// Wait for pod to be deleted
			log.Info("waiting for pod to be deleted")
			return ctrl.Result{RequeueAfter: quickCheck}, nil
		}
	} else if !errors.IsNotFound(err) {
		log.Error(err, "error checking Browser pod for deletion")
		// Don't block Browser deletion if we can't get the Pod
		log.Info("proceeding with finalizer removal despite pod check error")
	}

	// Remove finalizer
	if controllerutil.ContainsFinalizer(browser, browserPodFinalizer) {
		if err := r.retryUpdate(ctx, browser, func(b *browserv1.Browser) {
			controllerutil.RemoveFinalizer(b, browserPodFinalizer)
		}); err != nil {
			log.Error(err, "error removing Browser pod finalizer")
			return ctrl.Result{RequeueAfter: mediumRetry}, err
		}
	}

	log.Info("Browser cleanup completed")
	return ctrl.Result{}, nil
}

func (r *BrowserReconciler) deletePod(ctx context.Context, pod *corev1.Pod) error {
	log := logger.FromContext(ctx)

	if err := r.client.Delete(ctx, pod, client.GracePeriodSeconds(0)); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to force delete Browser Pod")
			return err
		}
	}

	log.Info("Browser Pod forcibly deleted")
	return nil
}

// handleMissingPod creates a new Pod for Browser
func (r *BrowserReconciler) handleMissingPod(ctx context.Context, browser *browserv1.Browser) (ctrl.Result, error) {
	log := logger.FromContext(ctx)

	key := fmt.Sprintf("%s/%s:%s",
		browser.Namespace,
		browser.Spec.BrowserName,
		browser.Spec.BrowserVersion,
	)

	log.Info("looking up browser config", "key", key)

	browserSpec, exists := r.config.Get(browser.GetNamespace(), browser.Spec.BrowserName, browser.Spec.BrowserVersion)
	if !exists || browserSpec == nil {
		if browser.Status.Phase != corev1.PodFailed {
			if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
				b.Status.Phase = corev1.PodFailed
				b.Status.Reason = "BrowserPodSpec"
				b.Status.Message = "Browser configuration not found"
			}); err != nil {
				log.Error(err, "Failed to update Browser status")
				return ctrl.Result{}, err
			}
			log.Info("Browser status set to Failed")
		}

		log.Info("Browser config not found", "key", key, "browserName", browser.Spec.BrowserName, "BrowserVersion", browser.Spec.BrowserVersion)
		return ctrl.Result{}, nil
	}

	opts, err := parseSelenosisOptions(browser.Annotations)
	if err != nil {
		log.Error(err, "invalid selenosis options JSON")
		if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
			b.Status.Phase = corev1.PodFailed
			b.Status.Reason = "InvalidSelenosisOptions"
			b.Status.Message = err.Error()
		}); err != nil {
			log.Error(err, "Failed to update Browser status")
			return ctrl.Result{}, err
		}

		log.Info("Invalid selenosis options")
		return ctrl.Result{}, nil
	}

	log.Info("parsed selenosis options", "hasOptions", opts != nil)

	// Create pod from template
	if err := r.createPod(ctx, browser, browserSpec, opts); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("Browser Pod already exists, will reconcile on next iteration")
			return ctrl.Result{RequeueAfter: quickCheck}, nil
		}
		log.Error(err, "failed to create Browser Pod")
		return ctrl.Result{}, err
	}

	log.Info("Browser Pod created")
	return ctrl.Result{RequeueAfter: quickCheck}, nil
}

// createPod creates a Pod for Browser with optimized memory usage
func (r *BrowserReconciler) createPod(ctx context.Context, browser *browserv1.Browser, browserSpec *configv1.BrowserVersionConfigSpec, opts *SelenosisOptions) error {
	log := logger.FromContext(ctx)

	pod := buildBrowserPod(browser, browserSpec, opts)

	log.Info("BrowserPodSpec configuration applied")
	return r.client.Create(ctx, pod)
}

// updateBrowserStatus efficiently updates Browser status based on Pod state
func (r *BrowserReconciler) updateBrowserStatus(ctx context.Context, browser *browserv1.Browser, pod *corev1.Pod) (ctrl.Result, error) {
	log := logger.FromContext(ctx)

	// Check for critical container termination
	for _, containerStatus := range pod.Status.ContainerStatuses {
		// Check if it's a critical container and if it has terminated state
		if (containerStatus.Name == browserContainerName || containerStatus.Name == sidecarContainerName) &&
			containerStatus.State.Terminated != nil {

			if browser.Status.Phase != corev1.PodFailed {
				if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
					b.Status.Phase = corev1.PodFailed
					b.Status.Reason = pod.Status.Reason
					b.Status.Message = pod.Status.Message
				}); err != nil {
					log.Error(err, "Failed to update Browser status")
					return ctrl.Result{}, err
				}
				log.Info("Browser status set to Failed")
			}

			log.Info("Browser Pod container statuses",
				"containerName",
				containerStatus.Name,
				"containerReady",
				strconv.FormatBool(containerStatus.Ready),
				"restartCount",
				containerStatus.RestartCount)

			log.Info("current finalizers on Browser", "finalizers", browser.Finalizers)

			// Schedule deletion of the Browser resource
			return r.deleteBrowser(ctx, browser)
		}
	}

	browserStatusChanged := browser.Status.Phase != pod.Status.Phase || browser.Status.PodIP != pod.Status.PodIP ||
		(pod.Status.StartTime != nil && (browser.Status.StartTime == nil || !browser.Status.StartTime.Equal(pod.Status.StartTime)))

	containersStatusChanged := false

	newContainerStatuses := make([]browserv1.ContainerStatus, 0, len(pod.Status.ContainerStatuses))

	// Efficiently collect container statuses
	if len(pod.Status.ContainerStatuses) > 0 {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			status := browserv1.ContainerStatus{
				Name:         containerStatus.Name,
				State:        containerStatus.State,
				Image:        containerStatus.Image,
				RestartCount: containerStatus.RestartCount,
				Ports:        getContainerPorts(containerStatus.Name, pod),
			}
			newContainerStatuses = append(newContainerStatuses, status)
		}

		// Check if container statuses changed (simplified check, could be improved with DeepEqual)
		if len(newContainerStatuses) != len(browser.Status.ContainerStatuses) {
			containersStatusChanged = true
		} else {
			// Simple check for changes (could be improved with full comparison)
			for i := range newContainerStatuses {
				if i >= len(browser.Status.ContainerStatuses) ||
					newContainerStatuses[i].RestartCount != browser.Status.ContainerStatuses[i].RestartCount ||
					!containerStateEqual(newContainerStatuses[i].State, browser.Status.ContainerStatuses[i].State) {
					containersStatusChanged = true
					break
				}
			}
		}
	}

	// Update status if changed
	if browserStatusChanged || containersStatusChanged {
		if err := r.retryStatusUpdate(ctx, browser, func(b *browserv1.Browser) {
			if browserStatusChanged {
				b.Status.PodIP = pod.Status.PodIP
				b.Status.StartTime = pod.Status.StartTime
			}

			b.Status.Phase = pod.Status.Phase

			if containersStatusChanged {
				b.Status.ContainerStatuses = newContainerStatuses
			}
		}); err != nil {
			log.Error(err, "Failed to update Browser status")
			return ctrl.Result{}, err
		}
	}

	log.Info("reconcilation completed")
	return ctrl.Result{RequeueAfter: periodicReconcile}, nil
}

func containerStateEqual(a, b corev1.ContainerState) bool {
	if (a.Running != nil) != (b.Running != nil) {
		return false
	}
	if (a.Terminated != nil) != (b.Terminated != nil) {
		return false
	}
	if (a.Waiting != nil) != (b.Waiting != nil) {
		return false
	}

	if a.Running != nil && b.Running != nil {
		return a.Running.StartedAt.Equal(&b.Running.StartedAt)
	}
	if a.Terminated != nil && b.Terminated != nil {
		return a.Terminated.ExitCode == b.Terminated.ExitCode &&
			a.Terminated.Reason == b.Terminated.Reason &&
			a.Terminated.Message == b.Terminated.Message &&
			a.Terminated.StartedAt.Equal(&b.Terminated.StartedAt) &&
			a.Terminated.FinishedAt.Equal(&b.Terminated.FinishedAt)
	}
	if a.Waiting != nil && b.Waiting != nil {
		return a.Waiting.Reason == b.Waiting.Reason &&
			a.Waiting.Message == b.Waiting.Message
	}

	return true
}

// getContainerPorts returns ports for a container with optimized memory usage
func getContainerPorts(containerName string, pod *corev1.Pod) []browserv1.ContainerPort {
	if pod.Spec.Containers == nil {
		return []browserv1.ContainerPort{}
	}

	for _, container := range pod.Spec.Containers {
		if container.Name == containerName && len(container.Ports) > 0 {
			ports := make([]browserv1.ContainerPort, 0, len(container.Ports))
			for _, port := range container.Ports {
				ports = append(ports, browserv1.ContainerPort{
					Name:          port.Name,
					ContainerPort: port.ContainerPort,
					Protocol:      port.Protocol,
					HostPort:      port.HostPort,
				})
			}
			return ports
		}
	}
	return []browserv1.ContainerPort{}
}

func (r *BrowserReconciler) retryUpdate(ctx context.Context, browser *browserv1.Browser, updateFunc func(*browserv1.Browser)) error {
	namespacedName := types.NamespacedName{
		Name:      browser.Name,
		Namespace: browser.Namespace,
	}

	for i := 0; i < maxRetries; i++ {
		current := &browserv1.Browser{}
		if err := r.client.Get(ctx, namespacedName, current); err != nil {
			return err
		}

		before := current.DeepCopy()
		updateFunc(current)

		patch := client.MergeFrom(before)
		err := r.client.Patch(ctx, current, patch)
		if err == nil {
			return nil
		}

		if !errors.IsConflict(err) {
			return err
		}

		time.Sleep(time.Millisecond * time.Duration(100*(1<<i)))
	}

	return fmt.Errorf("failed to patch Browser after %d attempts: version conflict", maxRetries)
}

func (r *BrowserReconciler) retryStatusUpdate(ctx context.Context, browser *browserv1.Browser, updateFunc func(*browserv1.Browser)) error {
	namespacedName := types.NamespacedName{
		Name:      browser.Name,
		Namespace: browser.Namespace,
	}

	for i := 0; i < maxRetries; i++ {
		current := &browserv1.Browser{}
		if err := r.client.Get(ctx, namespacedName, current); err != nil {
			return err
		}

		before := current.DeepCopy()
		updateFunc(current)

		patch := client.MergeFrom(before)
		err := r.client.Status().Patch(ctx, current, patch)
		if err == nil {
			return nil
		}

		if !errors.IsConflict(err) {
			return err
		}

		time.Sleep(time.Millisecond * time.Duration(100*(1<<i)))
	}

	return fmt.Errorf("failed to patch Browser status after %d attempts: version conflict", maxRetries)
}

func (r *BrowserReconciler) deleteBrowser(ctx context.Context, browser *browserv1.Browser) (ctrl.Result, error) {
	log := logger.FromContext(ctx)

	// Remove finalizer
	if controllerutil.ContainsFinalizer(browser, browserPodFinalizer) {
		if err := r.retryUpdate(ctx, browser, func(b *browserv1.Browser) {
			controllerutil.RemoveFinalizer(b, browserPodFinalizer)
		}); err != nil {
			log.Error(err, "error removing Browser pod finalizer")
			return ctrl.Result{RequeueAfter: mediumRetry}, err
		}
	}

	// Schedule deletion of the Browser resource
	if err := r.client.Delete(ctx, browser); err != nil && !errors.IsNotFound(err) {
		log.Error(err, "failed to delete Browser with terminated container")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func buildBrowserPod(browser *browserv1.Browser, cfg *configv1.BrowserVersionConfigSpec, opts *SelenosisOptions) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      browser.GetName(),
			Namespace: browser.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(browser, browserv1.SchemeGroupVersion.WithKind("Browser")),
			},
		},
	}

	if cfg.InitContainers != nil {

		initContainers := make([]corev1.Container, 0, len(*cfg.InitContainers))
		for _, ic := range *cfg.InitContainers {
			initContainer := corev1.Container{
				Name:  ic.Name,
				Image: ic.Image,
			}

			initContainer.ImagePullPolicy = ic.ImagePullPolicy

			if ic.VolumeMounts != nil {
				initContainer.VolumeMounts = append(initContainer.VolumeMounts, *ic.VolumeMounts...)
			}

			if ic.Env != nil {
				initContainer.Env = append(initContainer.Env, *ic.Env...)
			}

			if ic.Resources != nil {
				initContainer.Resources = *ic.Resources
			}

			if ic.Command != nil {
				initContainer.Command = *ic.Command
			}

			if ic.Ports != nil {
				initContainer.Ports = *ic.Ports
			}

			if cfg.WorkingDir != nil {
				initContainer.WorkingDir = *cfg.WorkingDir
			} else if ic.WorkingDir != nil {
				initContainer.WorkingDir = *ic.WorkingDir
			}

			initContainers = append(initContainers, initContainer)
		}
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, initContainers...)
	}

	// Base container
	browserContainer := corev1.Container{
		Name:  browserContainerName,
		Image: cfg.Image,
	}

	if cfg.Env != nil {
		browserContainer.Env = *cfg.Env
	}
	if cfg.Resources != nil {
		browserContainer.Resources = *cfg.Resources
	}

	if cfg.VolumeMounts != nil {
		browserContainer.VolumeMounts = append(browserContainer.VolumeMounts, *cfg.VolumeMounts...)
	}

	if cfg.Volumes != nil {
		volumes := make([]corev1.Volume, 0, len(*cfg.Volumes))

		for _, v := range *cfg.Volumes {
			volume := v.DeepCopy()
			volumes = append(volumes, *volume)
		}

		pod.Spec.Volumes = volumes
	}

	if cfg.WorkingDir != nil {
		browserContainer.WorkingDir = *cfg.WorkingDir
	}

	if cfg.SecurityContext != nil {
		pod.Spec.SecurityContext = cfg.SecurityContext
	}

	// Apply sidecars if present
	sidecarContainers := make([]corev1.Container, 0, 1+lenSidecars(cfg))
	sidecarContainers = append(sidecarContainers, browserContainer)

	if cfg.Sidecars != nil {
		for _, s := range *cfg.Sidecars {
			sidecar := corev1.Container{
				Name:  s.Name,
				Image: s.Image,
			}

			sidecar.ImagePullPolicy = s.ImagePullPolicy

			if s.Env != nil {
				sidecar.Env = append(sidecar.Env, *s.Env...)
			}

			if s.Resources != nil {
				sidecar.Resources = *s.Resources
			}

			if s.Command != nil {
				sidecar.Command = *s.Command
			}

			if s.Ports != nil {
				sidecar.Ports = *s.Ports
			}

			if s.VolumeMounts != nil {
				sidecar.VolumeMounts = append(sidecar.VolumeMounts, *s.VolumeMounts...)
			}

			if s.WorkingDir != nil {
				sidecar.WorkingDir = *s.WorkingDir
			}
			sidecarContainers = append(sidecarContainers, sidecar)
		}
	}

	if cfg.Privileged != nil && *cfg.Privileged {
		priv := true
		if sidecarContainers[0].SecurityContext == nil {
			sidecarContainers[0].SecurityContext = &corev1.SecurityContext{}
		}
		sidecarContainers[0].SecurityContext.Privileged = &priv
	}

	pod.Spec.Containers = sidecarContainers

	if browser.Labels != nil {
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		for k, v := range browser.Labels {
			pod.Labels[k] = v
		}
	}

	// Pod-level fields
	if cfg.Labels != nil {
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		for k, v := range *cfg.Labels {
			pod.Labels[k] = v
		}
	}

	if browser.Annotations != nil {
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		for k, v := range browser.Annotations {
			if k == selenosisOptionsAnnotationKey {
				continue
			}
			pod.Annotations[k] = v
		}
	}

	if cfg.Annotations != nil {
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		for k, v := range *cfg.Annotations {
			pod.Annotations[k] = v
		}
	}

	if cfg.NodeSelector != nil {
		pod.Spec.NodeSelector = *cfg.NodeSelector
	}

	if cfg.Affinity != nil {
		pod.Spec.Affinity = cfg.Affinity
	}

	if cfg.Tolerations != nil {
		pod.Spec.Tolerations = *cfg.Tolerations
	}

	if cfg.HostAliases != nil {
		pod.Spec.HostAliases = *cfg.HostAliases
	}

	if cfg.ImagePullSecrets != nil {
		pod.Spec.ImagePullSecrets = *cfg.ImagePullSecrets
	}

	if cfg.DNSConfig != nil {
		pod.Spec.DNSConfig = cfg.DNSConfig
	}

	pod.Spec.Hostname = browser.GetName()
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever

	applySelenosisOptions(pod, opts)

	return pod
}

func lenSidecars(cfg *configv1.BrowserVersionConfigSpec) int {
	if cfg.Sidecars != nil {
		return len(*cfg.Sidecars)
	}
	return 0
}

func parseSelenosisOptions(ann map[string]string) (*SelenosisOptions, error) {
	if ann == nil {
		return nil, nil
	}
	raw := ann[selenosisOptionsAnnotationKey]
	if raw == "" {
		return nil, nil
	}

	var opts SelenosisOptions
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", selenosisOptionsAnnotationKey, err)
	}
	return &opts, nil
}

func applySelenosisOptions(pod *corev1.Pod, opts *SelenosisOptions) {
	if pod == nil || opts == nil {
		return
	}

	if len(opts.Containers) > 0 {
		for i := range pod.Spec.Containers {
			name := pod.Spec.Containers[i].Name
			option, ok := opts.Containers[name]
			if !ok || len(option.Env) == 0 {
				continue
			}
			pod.Spec.Containers[i].Env = mergeEnvVars(pod.Spec.Containers[i].Env, option.Env)
		}
	}

	if opts.Labels != nil {
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		for k, v := range opts.Labels {
			pod.Labels[k] = v
		}
	}
}

func mergeEnvVars(base []corev1.EnvVar, override map[string]string) []corev1.EnvVar {
	if len(override) == 0 {
		return base
	}

	idx := make(map[string]int, len(base))
	out := append([]corev1.EnvVar(nil), base...)
	for i := range out {
		idx[out[i].Name] = i
	}

	for k, v := range override {
		ev := corev1.EnvVar{Name: k, Value: v}
		if pos, ok := idx[k]; ok {
			out[pos] = ev
		} else {
			idx[k] = len(out)
			out = append(out, ev)
		}
	}

	return out
}
