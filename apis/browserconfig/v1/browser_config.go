package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BrowserConfig is the root CRD type that defines browser configurations and associated pod templates.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type BrowserConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BrowserConfigSpec `json:"spec,omitempty"`
	Status ConfigStatus      `json:"status,omitempty"`
}

// BrowserConfigSpec defines the desired state of BrowserConfig.
type BrowserConfigSpec struct {
	// Template provides a base pod template for all browsers and versions.
	// +kubebuilder:validation:Optional
	Template *Template `json:"template,omitempty"`

	// Browsers maps browser names and versions to specific configurations.
	// If a field is nil, it falls back to the corresponding Template value.
	// Example: {"chrome": {"99.0": {...}, "100.0": {...}}, "firefox": {...}}
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	Browsers map[string]map[string]*BrowserVersionConfigSpec `json:"browsers"`
}

// Template defines a base pod specification that applies to all browsers/versions unless overridden.
type Template struct {

	// Labels are additional pod labels.
	Labels *map[string]string `json:"labels,omitempty"`

	// Annotations are additional pod annotations.
	Annotations *map[string]string `json:"annotations,omitempty"`

	// Env defines environment variables for the main container.
	Env *[]corev1.EnvVar `json:"env,omitempty"`

	// Resources defines CPU/memory requests and limits for the main container.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	//ImagePullPolicy defines container image pull policy
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Volumes defines pod volumes.
	Volumes *[]corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts defines mounts for pod volumes.
	VolumeMounts *[]corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// NodeSelector defines node selection constraints.
	NodeSelector *map[string]string `json:"nodeSelector,omitempty"`

	// Affinity defines pod affinity/anti-affinity rules.
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations defines tolerations for node taints.
	Tolerations *[]corev1.Toleration `json:"tolerations,omitempty"`

	// HostAliases defines custom /etc/hosts entries.
	HostAliases *[]corev1.HostAlias `json:"hostAliases,omitempty"`

	// List of initialization containers belonging to the pod.
	InitContainers *[]Sidecar `json:"initContainers,omitempty"`

	// Sidecars defines additional containers in the pod (minimum 1).
	// +kubebuilder:validation:MinItems=1
	Sidecars *[]Sidecar `json:"sidecars,omitempty"`

	// Privileged indicates if the main container should run in privileged mode.
	// +kubebuilder:default=false
	Privileged *bool `json:"privileged,omitempty"`

	// ImagePullSecrets specifies secrets for pulling private images.
	ImagePullSecrets *[]corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// DNSConfig defines pod-level DNS settings.
	DNSConfig *corev1.PodDNSConfig `json:"dnsConfig,omitempty"`

	// SecurityContext defines security context for the pod.
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// Container's working directory.
	// +optional
	WorkingDir *string `json:"workingDir,omitempty"`
}

// Sidecar defines a secondary container to be injected into the pod.
type Sidecar struct {
	// Name is the container name.
	Name string `json:"name"`

	// Image is the container image.
	Image string `json:"image"`

	// Command overrides the container entrypoint.Template.
	Command *[]string `json:"command,omitempty"`

	// Container's working directory.
	WorkingDir *string `json:"workingDir,omitempty"`

	// Ports defines container ports.
	Ports *[]corev1.ContainerPort `json:"ports,omitempty"`

	// Env defines environment variables for the sidecar.
	Env *[]corev1.EnvVar `json:"env,omitempty"`

	// VolumeMounts defines mounts for pod volumes.
	VolumeMounts *[]corev1.VolumeMount `json:"volumeMounts,omitempty"`

	//ImagePullPolicy defines container image pull policy
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Resources defines CPU/memory requests and limits for the sidecar container.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// BrowserVersionConfigSpec defines per-browser-version overrides.
// Fields set to nil will inherit values from the Template.
type BrowserVersionConfigSpec struct {
	// Image is the browser container image.
	Image string `json:"image"`

	Labels           *map[string]string             `json:"labels,omitempty"`
	Annotations      *map[string]string             `json:"annotations,omitempty"`
	Env              *[]corev1.EnvVar               `json:"env,omitempty"`
	Resources        *corev1.ResourceRequirements   `json:"resources,omitempty"`
	ImagePullPolicy  corev1.PullPolicy              `json:"imagePullPolicy,omitempty"`
	Volumes          *[]corev1.Volume               `json:"volumes,omitempty"`
	VolumeMounts     *[]corev1.VolumeMount          `json:"volumeMounts,omitempty"`
	NodeSelector     *map[string]string             `json:"nodeSelector,omitempty"`
	Affinity         *corev1.Affinity               `json:"affinity,omitempty"`
	Tolerations      *[]corev1.Toleration           `json:"tolerations,omitempty"`
	HostAliases      *[]corev1.HostAlias            `json:"hostAliases,omitempty"`
	InitContainers   *[]Sidecar                     `json:"initContainers,omitempty"`
	Sidecars         *[]Sidecar                     `json:"sidecars,omitempty"`
	Privileged       *bool                          `json:"privileged,omitempty"`
	ImagePullSecrets *[]corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	DNSConfig        *corev1.PodDNSConfig           `json:"dnsConfig,omitempty"`
	SecurityContext  *corev1.PodSecurityContext     `json:"securityContext,omitempty"`
	WorkingDir       *string                        `json:"workingDir,omitempty"`
}

// ConfigStatus defines the observed state of BrowserConfig.
type ConfigStatus struct {
	// Version is the current configuration version.
	Version string `json:"version,omitempty"`

	// LastUpdated is the timestamp of the last update.
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
}

// BrowserConfigList contains a list of BrowserConfig objects.
// +kubebuilder:object:root=true
type BrowserConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BrowserConfig `json:"items"`
}

// MergeWithTemplate merges the BrowserConfigSpec template into all browsers and versions.
func (spec *BrowserConfigSpec) MergeWithTemplate() {
	if spec.Template == nil {
		return
	}

	for browserName, versions := range spec.Browsers {
		for version, b := range versions {
			if b == nil {
				continue
			}
			b.mergeWithSpec(spec)
			versions[version] = b
		}
		spec.Browsers[browserName] = versions
	}
}

// mergeWithSpec merges Template values into a BrowserVersionConfig.
func (b *BrowserVersionConfigSpec) mergeWithSpec(t *BrowserConfigSpec) {

	b.Labels = mergeMapPtr(t.Template.Labels, b.Labels)
	b.Annotations = mergeMapPtr(t.Template.Annotations, b.Annotations)
	b.Env = mergeEnvPtr(t.Template.Env, b.Env)
	b.Resources = firstNonNilResource(t.Template.Resources, b.Resources)
	if b.ImagePullPolicy == "" {
		b.ImagePullPolicy = t.Template.ImagePullPolicy
	}
	b.Volumes = mergeVolumePtr(t.Template.Volumes, b.Volumes)

	b.NodeSelector = mergeMapPtr(t.Template.NodeSelector, b.NodeSelector)

	if b.Affinity == nil {
		b.Affinity = t.Template.Affinity
	}

	b.Tolerations = mergeTolerationPtr(t.Template.Tolerations, b.Tolerations)
	b.HostAliases = mergeHostAliasPtr(t.Template.HostAliases, b.HostAliases)
	b.VolumeMounts = mergeVolumeMountsPtr(b.VolumeMounts, t.Template.VolumeMounts)

	originalSidecars := b.Sidecars

	b.Sidecars = mergeSidecarPtr(t.Template.Sidecars, b.Sidecars)

	if originalSidecars != nil && t.Template.Sidecars != nil {
		for i := range *b.Sidecars {
			for _, origSidecar := range *originalSidecars {
				if (*b.Sidecars)[i].Name == origSidecar.Name {
					templateSidecar := findTemplateSidecar(t.Template.Sidecars, origSidecar.Name)
					if templateSidecar != nil {
						(*b.Sidecars)[i].mergeWithTemplate(templateSidecar)
					}
					break
				}
			}
		}
	}

	originalInitContainers := b.InitContainers

	b.InitContainers = mergeSidecarPtr(b.InitContainers, t.Template.InitContainers)

	if originalInitContainers != nil && t.Template.InitContainers != nil {
		for i := range *b.InitContainers {
			for _, origInit := range *originalInitContainers {
				if (*b.InitContainers)[i].Name == origInit.Name {
					templateInit := findTemplateSidecar(t.Template.InitContainers, origInit.Name)
					if templateInit != nil {
						(*b.InitContainers)[i].mergeWithTemplate(templateInit)
					}
					break
				}
			}
		}
	}

	if b.Privileged == nil {
		b.Privileged = t.Template.Privileged
	}

	b.ImagePullSecrets = mergeLocalObjectRefPtr(t.Template.ImagePullSecrets, b.ImagePullSecrets)

	if b.DNSConfig == nil {
		b.DNSConfig = t.Template.DNSConfig
	}

	if b.SecurityContext == nil {
		b.SecurityContext = t.Template.SecurityContext
	}

	if b.WorkingDir == nil {
		b.WorkingDir = t.Template.WorkingDir
	}
}

func mergeMapPtr(template, override *map[string]string) *map[string]string {
	if template == nil && override == nil {
		return nil
	}

	result := map[string]string{}
	if template != nil {
		for k, v := range *template {
			result[k] = v
		}
	}

	if override != nil {
		for k, v := range *override {
			result[k] = v
		}
	}

	return &result
}

func mergeEnvPtr(template, override *[]corev1.EnvVar) *[]corev1.EnvVar {
	if template == nil && override == nil {
		return nil
	}

	result := map[string]corev1.EnvVar{}
	if template != nil {
		for _, env := range *template {
			result[env.Name] = env
		}
	}

	if override != nil {
		for _, env := range *override {
			result[env.Name] = env
		}
	}

	merged := make([]corev1.EnvVar, 0, len(result))
	for _, env := range result {
		merged = append(merged, env)
	}

	return &merged
}

func firstNonNilResource(template, override *corev1.ResourceRequirements) *corev1.ResourceRequirements {
	if override != nil {
		return override
	}

	return template
}

func mergeVolumePtr(template, override *[]corev1.Volume) *[]corev1.Volume {
	result := []corev1.Volume{}
	if template != nil {
		result = append(result, *template...)
	}

	if override != nil {
		result = append(result, *override...)
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func mergeTolerationPtr(template, override *[]corev1.Toleration) *[]corev1.Toleration {
	result := []corev1.Toleration{}
	if template != nil {
		result = append(result, *template...)
	}

	if override != nil {
		result = append(result, *override...)
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func mergeHostAliasPtr(template, override *[]corev1.HostAlias) *[]corev1.HostAlias {
	result := []corev1.HostAlias{}
	if template != nil {
		result = append(result, *template...)
	}

	if override != nil {
		result = append(result, *override...)
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func mergeSidecarPtr(template, override *[]Sidecar) *[]Sidecar {
	if template == nil && override == nil {
		return nil
	}
	if override == nil {
		cp := append([]Sidecar{}, *template...)
		return &cp
	}
	if template == nil {
		cp := append([]Sidecar{}, *override...)
		return &cp
	}

	result := append([]Sidecar{}, *override...)
	overrideNames := map[string]struct{}{}
	for _, s := range *override {
		overrideNames[s.Name] = struct{}{}
	}
	for _, s := range *template {
		if _, exists := overrideNames[s.Name]; !exists {
			result = append(result, s)
		}
	}
	return &result
}

func mergeVolumeMountsPtr(template, override *[]corev1.VolumeMount) *[]corev1.VolumeMount {
	result := []corev1.VolumeMount{}

	if template != nil {
		for _, t := range *template {
			copy := t.DeepCopy()
			result = append(result, *copy)
		}
	}

	if override != nil {
		for _, o := range *override {
			copy := o.DeepCopy()
			result = append(result, *copy)
		}
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func mergeLocalObjectRefPtr(template, override *[]corev1.LocalObjectReference) *[]corev1.LocalObjectReference {
	result := []corev1.LocalObjectReference{}
	if template != nil {
		result = append(result, *template...)
	}

	if override != nil {
		result = append(result, *override...)
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func (s *Sidecar) mergeWithTemplate(t *Sidecar) {
	if s.Command == nil {
		s.Command = t.Command
	}

	s.WorkingDir = t.WorkingDir
	s.Env = mergeEnvPtr(t.Env, s.Env)
	s.Ports = mergeContainerPortPtr(t.Ports, s.Ports)
	s.VolumeMounts = mergeVolumeMountPtr(t.VolumeMounts, s.VolumeMounts)

	if s.Resources == nil {
		s.Resources = t.Resources
	}
}

func mergeContainerPortPtr(template, override *[]corev1.ContainerPort) *[]corev1.ContainerPort {
	result := []corev1.ContainerPort{}
	if template != nil {
		result = append(result, *template...)
	}

	if override != nil {
		result = append(result, *override...)
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func mergeVolumeMountPtr(template, override *[]corev1.VolumeMount) *[]corev1.VolumeMount {
	result := []corev1.VolumeMount{}
	if template != nil {
		result = append(result, *template...)
	}

	if override != nil {
		result = append(result, *override...)
	}

	if len(result) == 0 {
		return nil
	}

	return &result
}

func findTemplateSidecar(template *[]Sidecar, name string) *Sidecar {
	if template == nil {
		return nil
	}

	for i := range *template {
		if (*template)[i].Name == name {
			return &(*template)[i]
		}
	}

	return nil
}
