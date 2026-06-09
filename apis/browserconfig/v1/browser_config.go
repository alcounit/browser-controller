package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=browserconfigs,scope=Namespaced
// BrowserConfig is the root CRD type that defines browser configurations and associated pod templates.
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

	// ImagePullPolicy defines container image pull policy.
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

	// InitContainers defines initialization containers for the pod.
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

	// Command overrides the entrypoint of the main browser container.
	Command *[]string `json:"command,omitempty"`

	// Args overrides the arguments passed to the main browser container entrypoint.
	Args *[]string `json:"args,omitempty"`

	// WorkingDir sets the working directory for the main browser container.
	WorkingDir *string `json:"workingDir,omitempty"`
}

// Sidecar defines a secondary container to be injected into the pod.
type Sidecar struct {
	// Name is the container name.
	Name string `json:"name"`

	// Image is the container image.
	Image string `json:"image"`

	// Command overrides the container entrypoint.
	Command *[]string `json:"command,omitempty"`

	// Args overrides the arguments passed to the container entrypoint.
	Args *[]string `json:"args,omitempty"`

	// WorkingDir sets the working directory for the container.
	WorkingDir *string `json:"workingDir,omitempty"`

	// Ports defines container ports.
	Ports *[]corev1.ContainerPort `json:"ports,omitempty"`

	// Env defines environment variables for the sidecar.
	Env *[]corev1.EnvVar `json:"env,omitempty"`

	// VolumeMounts defines mounts for pod volumes.
	VolumeMounts *[]corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// ImagePullPolicy defines container image pull policy.
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Resources defines CPU/memory requests and limits for the sidecar container.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// BrowserVersionConfigSpec defines per-browser-version overrides.
// Fields set to nil will inherit values from the Template.
type BrowserVersionConfigSpec struct {
	// Image is the browser container image.
	Image string `json:"image"`

	// Labels are additional pod labels.
	Labels *map[string]string `json:"labels,omitempty"`

	// Annotations are additional pod annotations.
	Annotations *map[string]string `json:"annotations,omitempty"`

	// Env defines environment variables for the main container.
	Env *[]corev1.EnvVar `json:"env,omitempty"`

	// Resources defines CPU/memory requests and limits for the main container.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ImagePullPolicy defines container image pull policy.
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

	// InitContainers defines initialization containers for the pod.
	InitContainers *[]Sidecar `json:"initContainers,omitempty"`

	// Sidecars defines additional containers in the pod.
	Sidecars *[]Sidecar `json:"sidecars,omitempty"`

	// Privileged indicates if the main container should run in privileged mode.
	Privileged *bool `json:"privileged,omitempty"`

	// ImagePullSecrets specifies secrets for pulling private images.
	ImagePullSecrets *[]corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// DNSConfig defines pod-level DNS settings.
	DNSConfig *corev1.PodDNSConfig `json:"dnsConfig,omitempty"`

	// SecurityContext defines security context for the pod.
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// Command overrides the entrypoint of the main browser container.
	Command *[]string `json:"command,omitempty"`

	// Args overrides the arguments passed to the main browser container entrypoint.
	Args *[]string `json:"args,omitempty"`

	// WorkingDir sets the working directory for the main browser container.
	WorkingDir *string `json:"workingDir,omitempty"`
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
	b.VolumeMounts = mergeVolumeMountsPtr(t.Template.VolumeMounts, b.VolumeMounts)

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

	b.InitContainers = mergeSidecarPtr(t.Template.InitContainers, b.InitContainers)

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

	if b.Command == nil {
		b.Command = t.Template.Command
	}

	if b.Args == nil {
		b.Args = t.Template.Args
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

	// Build merged slice preserving template order; override vars replace template vars in-place,
	// new override-only vars are appended at the end.
	index := make(map[string]int) // name -> position in merged
	merged := make([]corev1.EnvVar, 0)

	if template != nil {
		for _, env := range *template {
			index[env.Name] = len(merged)
			merged = append(merged, env)
		}
	}

	if override != nil {
		for _, env := range *override {
			if i, exists := index[env.Name]; exists {
				merged[i] = env
			} else {
				merged = append(merged, env)
			}
		}
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
	if template == nil && override == nil {
		return nil
	}

	index := make(map[string]int)
	merged := make([]corev1.Volume, 0)

	if template != nil {
		for _, v := range *template {
			index[v.Name] = len(merged)
			merged = append(merged, v)
		}
	}

	if override != nil {
		for _, v := range *override {
			if i, exists := index[v.Name]; exists {
				merged[i] = v
			} else {
				index[v.Name] = len(merged)
				merged = append(merged, v)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
}

func mergeTolerationPtr(template, override *[]corev1.Toleration) *[]corev1.Toleration {
	if template == nil && override == nil {
		return nil
	}

	index := make(map[string]int)
	merged := make([]corev1.Toleration, 0)

	if template != nil {
		for _, t := range *template {
			index[t.Key] = len(merged)
			merged = append(merged, t)
		}
	}

	if override != nil {
		for _, t := range *override {
			if i, exists := index[t.Key]; exists {
				merged[i] = t
			} else {
				index[t.Key] = len(merged)
				merged = append(merged, t)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
}

func mergeHostAliasPtr(template, override *[]corev1.HostAlias) *[]corev1.HostAlias {
	if template == nil && override == nil {
		return nil
	}

	index := make(map[string]int)
	merged := make([]corev1.HostAlias, 0)

	if template != nil {
		for _, h := range *template {
			index[h.IP] = len(merged)
			merged = append(merged, h)
		}
	}

	if override != nil {
		for _, h := range *override {
			if i, exists := index[h.IP]; exists {
				merged[i] = h
			} else {
				index[h.IP] = len(merged)
				merged = append(merged, h)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
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
	if template == nil && override == nil {
		return nil
	}

	index := make(map[string]int)
	merged := make([]corev1.VolumeMount, 0)

	if template != nil {
		for _, t := range *template {
			cp := t.DeepCopy()
			index[cp.MountPath] = len(merged)
			merged = append(merged, *cp)
		}
	}

	if override != nil {
		for _, o := range *override {
			cp := o.DeepCopy()
			if i, exists := index[cp.MountPath]; exists {
				merged[i] = *cp
			} else {
				index[cp.MountPath] = len(merged)
				merged = append(merged, *cp)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
}

func mergeLocalObjectRefPtr(template, override *[]corev1.LocalObjectReference) *[]corev1.LocalObjectReference {
	if template == nil && override == nil {
		return nil
	}

	seen := make(map[string]int)
	merged := make([]corev1.LocalObjectReference, 0)

	if template != nil {
		for _, r := range *template {
			seen[r.Name] = len(merged)
			merged = append(merged, r)
		}
	}

	if override != nil {
		for _, r := range *override {
			if i, exists := seen[r.Name]; exists {
				merged[i] = r
			} else {
				seen[r.Name] = len(merged)
				merged = append(merged, r)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
}

func (s *Sidecar) mergeWithTemplate(t *Sidecar) {
	if s.Command == nil {
		s.Command = t.Command
	}

	if s.Args == nil {
		s.Args = t.Args
	}

	if s.WorkingDir == nil {
		s.WorkingDir = t.WorkingDir
	}

	s.Env = mergeEnvPtr(t.Env, s.Env)
	s.Ports = mergeContainerPortPtr(t.Ports, s.Ports)
	s.VolumeMounts = mergeVolumeMountPtr(t.VolumeMounts, s.VolumeMounts)

	if s.Resources == nil {
		s.Resources = t.Resources
	}
}

func mergeContainerPortPtr(template, override *[]corev1.ContainerPort) *[]corev1.ContainerPort {
	if template == nil && override == nil {
		return nil
	}

	index := make(map[int32]int)
	merged := make([]corev1.ContainerPort, 0)

	if template != nil {
		for _, p := range *template {
			index[p.ContainerPort] = len(merged)
			merged = append(merged, p)
		}
	}

	if override != nil {
		for _, p := range *override {
			if i, exists := index[p.ContainerPort]; exists {
				merged[i] = p
			} else {
				index[p.ContainerPort] = len(merged)
				merged = append(merged, p)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
}

func mergeVolumeMountPtr(template, override *[]corev1.VolumeMount) *[]corev1.VolumeMount {
	if template == nil && override == nil {
		return nil
	}

	index := make(map[string]int)
	merged := make([]corev1.VolumeMount, 0)

	if template != nil {
		for _, m := range *template {
			index[m.MountPath] = len(merged)
			merged = append(merged, m)
		}
	}

	if override != nil {
		for _, m := range *override {
			if i, exists := index[m.MountPath]; exists {
				merged[i] = m
			} else {
				index[m.MountPath] = len(merged)
				merged = append(merged, m)
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return &merged
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
