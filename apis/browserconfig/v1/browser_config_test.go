package v1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestMergeWithTemplateInheritsImagePullPolicyFromTemplate(t *testing.T) {
	templateEnv := []corev1.EnvVar{{Name: "TZ", Value: "UTC"}}
	spec := BrowserConfigSpec{
		Template: &Template{
			ImagePullPolicy: corev1.PullIfNotPresent,
			Env:             &templateEnv,
		},
		Browsers: map[string]map[string]*BrowserVersionConfigSpec{
			"chrome": {
				"123.0": {Image: "chrome:123"},
			},
		},
	}

	spec.MergeWithTemplate()

	cfg := spec.Browsers["chrome"]["123.0"]
	if cfg.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("expected imagePullPolicy=%q, got %q", corev1.PullIfNotPresent, cfg.ImagePullPolicy)
	}
	if cfg.Env == nil || len(*cfg.Env) != 1 || (*cfg.Env)[0].Name != "TZ" {
		t.Fatalf("expected env to be merged from template, got %+v", cfg.Env)
	}
}

func TestMergeWithTemplateDoesNotOverrideImagePullPolicy(t *testing.T) {
	spec := BrowserConfigSpec{
		Template: &Template{
			ImagePullPolicy: corev1.PullIfNotPresent,
		},
		Browsers: map[string]map[string]*BrowserVersionConfigSpec{
			"chrome": {
				"124.0": {
					Image:           "chrome:124",
					ImagePullPolicy: corev1.PullAlways,
				},
			},
		},
	}

	spec.MergeWithTemplate()

	cfg := spec.Browsers["chrome"]["124.0"]
	if cfg.ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("expected explicit imagePullPolicy=%q to be preserved, got %q", corev1.PullAlways, cfg.ImagePullPolicy)
	}
}

func TestMergeMapPtrOverrideWins(t *testing.T) {
	template := map[string]string{
		"shared":   "template",
		"template": "value",
	}
	override := map[string]string{
		"shared":   "override",
		"override": "value",
	}

	merged := mergeMapPtr(&template, &override)
	if merged == nil {
		t.Fatalf("expected merged map, got nil")
	}

	if (*merged)["shared"] != "override" {
		t.Fatalf("expected override value for shared key, got %q", (*merged)["shared"])
	}
	if (*merged)["template"] != "value" || (*merged)["override"] != "value" {
		t.Fatalf("expected both template and override unique keys, got %+v", *merged)
	}
}

func TestMergeEnvPtrOverrideWinsByName(t *testing.T) {
	template := []corev1.EnvVar{
		{Name: "TZ", Value: "UTC"},
		{Name: "DEBUG", Value: "false"},
	}
	override := []corev1.EnvVar{
		{Name: "DEBUG", Value: "true"},
		{Name: "CUSTOM", Value: "42"},
	}

	merged := mergeEnvPtr(&template, &override)
	if merged == nil {
		t.Fatalf("expected merged env, got nil")
	}

	got := map[string]string{}
	for _, env := range *merged {
		got[env.Name] = env.Value
	}

	if got["TZ"] != "UTC" || got["DEBUG"] != "true" || got["CUSTOM"] != "42" {
		t.Fatalf("unexpected merged env: %+v", got)
	}
}

func TestMergeSidecarPtrMergesByName(t *testing.T) {
	template := []Sidecar{
		{Name: "metrics", Image: "metrics:1.0"},
		{Name: "logger", Image: "logger:1.0"},
	}
	override := []Sidecar{
		{Name: "logger", Image: "logger:2.0"},
		{Name: "debug", Image: "debug:latest"},
	}

	merged := mergeSidecarPtr(&template, &override)
	if merged == nil {
		t.Fatalf("expected merged sidecars, got nil")
	}

	if len(*merged) != 3 {
		t.Fatalf("expected 3 merged sidecars, got %d", len(*merged))
	}

	got := map[string]string{}
	for _, sc := range *merged {
		got[sc.Name] = sc.Image
	}

	if got["metrics"] != "metrics:1.0" {
		t.Fatalf("expected template-only sidecar to be present, got %+v", got)
	}
	if got["logger"] != "logger:2.0" {
		t.Fatalf("expected override sidecar to win, got %+v", got)
	}
	if got["debug"] != "debug:latest" {
		t.Fatalf("expected override-only sidecar to be present, got %+v", got)
	}
}

func TestMergeWithTemplateNoTemplateNoop(t *testing.T) {
	spec := BrowserConfigSpec{
		Browsers: map[string]map[string]*BrowserVersionConfigSpec{
			"chrome": {
				"125.0": {Image: "chrome:125"},
			},
		},
	}

	spec.MergeWithTemplate()

	if got := spec.Browsers["chrome"]["125.0"].ImagePullPolicy; got != "" {
		t.Fatalf("expected no changes when template is nil, got imagePullPolicy=%q", got)
	}
}

func TestMergeWithTemplateSkipsNilVersionConfig(t *testing.T) {
	spec := BrowserConfigSpec{
		Template: &Template{
			ImagePullPolicy: corev1.PullIfNotPresent,
		},
		Browsers: map[string]map[string]*BrowserVersionConfigSpec{
			"chrome": {
				"nil-entry": nil,
				"126.0":     {Image: "chrome:126"},
			},
		},
	}

	spec.MergeWithTemplate()

	if spec.Browsers["chrome"]["nil-entry"] != nil {
		t.Fatalf("expected nil browser version config to stay nil")
	}
	if got := spec.Browsers["chrome"]["126.0"].ImagePullPolicy; got != corev1.PullIfNotPresent {
		t.Fatalf("expected non-nil browser config to be merged, got imagePullPolicy=%q", got)
	}
}

func TestBrowserVersionConfigSpecMergeWithSpecMergesAllPaths(t *testing.T) {
	templateLabels := map[string]string{"from-template": "1"}
	overrideLabels := map[string]string{"from-override": "1"}
	templateAnnotations := map[string]string{"ta": "tv"}
	overrideAnnotations := map[string]string{"oa": "ov"}
	templateEnv := []corev1.EnvVar{{Name: "TEMPLATE_ENV", Value: "1"}}
	overrideEnv := []corev1.EnvVar{{Name: "OVERRIDE_ENV", Value: "1"}}
	templateResources := corev1.ResourceRequirements{}
	templateVolumes := []corev1.Volume{{Name: "template-vol"}}
	overrideVolumes := []corev1.Volume{{Name: "override-vol"}}
	templateNodeSelector := map[string]string{"pool": "template"}
	templateAffinity := &corev1.Affinity{}
	templateTolerations := []corev1.Toleration{{Key: "template"}}
	overrideTolerations := []corev1.Toleration{{Key: "override"}}
	templateHostAliases := []corev1.HostAlias{{IP: "10.0.0.1", Hostnames: []string{"template"}}}
	overrideHostAliases := []corev1.HostAlias{{IP: "10.0.0.2", Hostnames: []string{"override"}}}
	templateMounts := []corev1.VolumeMount{{Name: "template-vol", MountPath: "/template"}}
	overrideMounts := []corev1.VolumeMount{{Name: "override-vol", MountPath: "/override"}}
	templateCommand := []string{"tmpl-cmd"}
	templateSidecarEnv := []corev1.EnvVar{{Name: "TMPL_SC_ENV", Value: "1"}}
	overrideSidecarEnv := []corev1.EnvVar{{Name: "OVR_SC_ENV", Value: "1"}}
	templatePorts := []corev1.ContainerPort{{ContainerPort: 8080}}
	templateSidecarMounts := []corev1.VolumeMount{{Name: "template-vol", MountPath: "/sc-template"}}
	templateSidecars := []Sidecar{
		{
			Name:         "shared-sidecar",
			Image:        "template-sc",
			Command:      &templateCommand,
			WorkingDir:   strPtr("/template-sidecar-workdir"),
			Env:          &templateSidecarEnv,
			Ports:        &templatePorts,
			VolumeMounts: &templateSidecarMounts,
			Resources:    &templateResources,
		},
		{Name: "template-only-sidecar", Image: "template-only"},
	}
	overrideSidecars := []Sidecar{
		{
			Name:  "shared-sidecar",
			Image: "override-sc",
			Env:   &overrideSidecarEnv,
		},
		{Name: "override-only-sidecar", Image: "override-only"},
	}
	templateInit := []Sidecar{
		{
			Name:       "shared-init",
			Image:      "template-init",
			Command:    &templateCommand,
			WorkingDir: strPtr("/template-init-workdir"),
		},
		{Name: "template-only-init", Image: "template-only-init"},
	}
	overrideInit := []Sidecar{
		{Name: "shared-init", Image: "override-init"},
	}
	templatePrivileged := true
	templatePullSecrets := []corev1.LocalObjectReference{{Name: "template-secret"}}
	overridePullSecrets := []corev1.LocalObjectReference{{Name: "override-secret"}}
	templateDNSConfig := &corev1.PodDNSConfig{}
	templateSecurityContext := &corev1.PodSecurityContext{}
	templateWorkingDir := "/template-workdir"

	spec := BrowserConfigSpec{
		Template: &Template{
			Labels:           &templateLabels,
			Annotations:      &templateAnnotations,
			Env:              &templateEnv,
			Resources:        &templateResources,
			ImagePullPolicy:  corev1.PullIfNotPresent,
			Volumes:          &templateVolumes,
			VolumeMounts:     &templateMounts,
			NodeSelector:     &templateNodeSelector,
			Affinity:         templateAffinity,
			Tolerations:      &templateTolerations,
			HostAliases:      &templateHostAliases,
			Sidecars:         &templateSidecars,
			InitContainers:   &templateInit,
			Privileged:       &templatePrivileged,
			ImagePullSecrets: &templatePullSecrets,
			DNSConfig:        templateDNSConfig,
			SecurityContext:  templateSecurityContext,
			WorkingDir:       &templateWorkingDir,
		},
	}
	b := &BrowserVersionConfigSpec{
		Image:            "browser",
		Labels:           &overrideLabels,
		Annotations:      &overrideAnnotations,
		Env:              &overrideEnv,
		Volumes:          &overrideVolumes,
		VolumeMounts:     &overrideMounts,
		Tolerations:      &overrideTolerations,
		HostAliases:      &overrideHostAliases,
		Sidecars:         &overrideSidecars,
		InitContainers:   &overrideInit,
		ImagePullSecrets: &overridePullSecrets,
	}

	b.mergeWithSpec(&spec)

	if b.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("expected imagePullPolicy to be inherited, got %q", b.ImagePullPolicy)
	}
	if b.Resources != &templateResources {
		t.Fatalf("expected resources to inherit from template")
	}
	if b.Affinity != templateAffinity {
		t.Fatalf("expected affinity to inherit from template")
	}
	if b.Privileged == nil || !*b.Privileged {
		t.Fatalf("expected privileged to inherit from template")
	}
	if b.DNSConfig != templateDNSConfig || b.SecurityContext != templateSecurityContext || b.WorkingDir == nil || *b.WorkingDir != templateWorkingDir {
		t.Fatalf("expected dns/securityContext/workingDir to inherit from template")
	}

	if b.Labels == nil || (*b.Labels)["from-template"] != "1" || (*b.Labels)["from-override"] != "1" {
		t.Fatalf("expected labels to merge, got %+v", b.Labels)
	}
	if b.Annotations == nil || (*b.Annotations)["ta"] != "tv" || (*b.Annotations)["oa"] != "ov" {
		t.Fatalf("expected annotations to merge, got %+v", b.Annotations)
	}
	if b.Env == nil || !hasEnv(*b.Env, "TEMPLATE_ENV", "1") || !hasEnv(*b.Env, "OVERRIDE_ENV", "1") {
		t.Fatalf("expected env to merge, got %+v", b.Env)
	}
	if b.Volumes == nil || len(*b.Volumes) != 2 {
		t.Fatalf("expected volumes to merge, got %+v", b.Volumes)
	}
	if b.VolumeMounts == nil || len(*b.VolumeMounts) != 2 {
		t.Fatalf("expected volumeMounts to merge, got %+v", b.VolumeMounts)
	}
	if b.Tolerations == nil || len(*b.Tolerations) != 2 {
		t.Fatalf("expected tolerations to merge, got %+v", b.Tolerations)
	}
	if b.HostAliases == nil || len(*b.HostAliases) != 2 {
		t.Fatalf("expected hostAliases to merge, got %+v", b.HostAliases)
	}
	if b.ImagePullSecrets == nil || len(*b.ImagePullSecrets) != 2 {
		t.Fatalf("expected imagePullSecrets to merge, got %+v", b.ImagePullSecrets)
	}
	if b.Sidecars == nil || len(*b.Sidecars) != 3 {
		t.Fatalf("expected sidecars to merge, got %+v", b.Sidecars)
	}
	shared := getSidecarByName(*b.Sidecars, "shared-sidecar")
	if shared == nil {
		t.Fatalf("expected shared sidecar in merged result")
	}
	if shared.Command == nil || len(*shared.Command) != 1 || (*shared.Command)[0] != "tmpl-cmd" {
		t.Fatalf("expected shared sidecar command from template, got %+v", shared.Command)
	}
	if shared.Resources != &templateResources {
		t.Fatalf("expected shared sidecar resources from template")
	}
	if shared.Ports == nil || len(*shared.Ports) != 1 || (*shared.Ports)[0].ContainerPort != 8080 {
		t.Fatalf("expected shared sidecar ports from template, got %+v", shared.Ports)
	}
	if shared.VolumeMounts == nil || len(*shared.VolumeMounts) != 1 {
		t.Fatalf("expected shared sidecar volume mounts from template, got %+v", shared.VolumeMounts)
	}
	if shared.Env == nil || !hasEnv(*shared.Env, "TMPL_SC_ENV", "1") || !hasEnv(*shared.Env, "OVR_SC_ENV", "1") {
		t.Fatalf("expected shared sidecar env merge, got %+v", shared.Env)
	}
	if b.InitContainers == nil || len(*b.InitContainers) != 2 {
		t.Fatalf("expected initContainers to merge, got %+v", b.InitContainers)
	}
}

func TestMergeHelperFunctionsNilAndNonNilPaths(t *testing.T) {
	if got := mergeMapPtr(nil, nil); got != nil {
		t.Fatalf("expected nil map for nil inputs, got %+v", got)
	}
	if got := mergeEnvPtr(nil, nil); got != nil {
		t.Fatalf("expected nil env for nil inputs, got %+v", got)
	}
	if got := mergeVolumePtr(nil, nil); got != nil {
		t.Fatalf("expected nil volumes for nil inputs, got %+v", got)
	}
	if got := mergeTolerationPtr(nil, nil); got != nil {
		t.Fatalf("expected nil tolerations for nil inputs, got %+v", got)
	}
	if got := mergeHostAliasPtr(nil, nil); got != nil {
		t.Fatalf("expected nil hostAliases for nil inputs, got %+v", got)
	}
	if got := mergeVolumeMountsPtr(nil, nil); got != nil {
		t.Fatalf("expected nil volumeMounts for nil inputs, got %+v", got)
	}
	if got := mergeLocalObjectRefPtr(nil, nil); got != nil {
		t.Fatalf("expected nil imagePullSecrets for nil inputs, got %+v", got)
	}
	if got := mergeContainerPortPtr(nil, nil); got != nil {
		t.Fatalf("expected nil containerPorts for nil inputs, got %+v", got)
	}
	if got := mergeVolumeMountPtr(nil, nil); got != nil {
		t.Fatalf("expected nil volumeMounts for nil inputs, got %+v", got)
	}

	templateRes := &corev1.ResourceRequirements{}
	overrideRes := &corev1.ResourceRequirements{}
	if got := firstNonNilResource(templateRes, nil); got != templateRes {
		t.Fatalf("expected template resource when override is nil")
	}
	if got := firstNonNilResource(templateRes, overrideRes); got != overrideRes {
		t.Fatalf("expected override resource when override is set")
	}
}

func TestMergeSidecarPtrNilCases(t *testing.T) {
	template := []Sidecar{{Name: "template", Image: "tmpl"}}
	override := []Sidecar{{Name: "override", Image: "ovr"}}

	if got := mergeSidecarPtr(nil, nil); got != nil {
		t.Fatalf("expected nil when both sidecar lists are nil")
	}
	if got := mergeSidecarPtr(&template, nil); got == nil || len(*got) != 1 || (*got)[0].Name != "template" {
		t.Fatalf("expected template sidecars when override is nil, got %+v", got)
	}
	if got := mergeSidecarPtr(nil, &override); got == nil || len(*got) != 1 || (*got)[0].Name != "override" {
		t.Fatalf("expected override sidecars when template is nil, got %+v", got)
	}
}

func TestFindTemplateSidecar(t *testing.T) {
	if got := findTemplateSidecar(nil, "x"); got != nil {
		t.Fatalf("expected nil result for nil template")
	}

	template := []Sidecar{{Name: "metrics", Image: "metrics:1.0"}}
	if got := findTemplateSidecar(&template, "missing"); got != nil {
		t.Fatalf("expected nil for missing sidecar")
	}
	if got := findTemplateSidecar(&template, "metrics"); got == nil || got.Name != "metrics" {
		t.Fatalf("expected to find sidecar metrics, got %+v", got)
	}
}

func TestSidecarMergeWithTemplate(t *testing.T) {
	templateCommand := []string{"run"}
	templateEnv := []corev1.EnvVar{{Name: "TMPL", Value: "1"}}
	overrideEnv := []corev1.EnvVar{{Name: "OVR", Value: "1"}}
	templatePorts := []corev1.ContainerPort{{ContainerPort: 8080}}
	templateMounts := []corev1.VolumeMount{{Name: "v", MountPath: "/t"}}
	templateResources := corev1.ResourceRequirements{}

	s := Sidecar{
		Name: "s",
		Env:  &overrideEnv,
	}
	tmpl := Sidecar{
		Name:         "s",
		Command:      &templateCommand,
		WorkingDir:   strPtr("/work"),
		Env:          &templateEnv,
		Ports:        &templatePorts,
		VolumeMounts: &templateMounts,
		Resources:    &templateResources,
	}

	s.mergeWithTemplate(&tmpl)

	if s.Command == nil || len(*s.Command) != 1 || (*s.Command)[0] != "run" {
		t.Fatalf("expected command from template, got %+v", s.Command)
	}
	if s.WorkingDir == nil || *s.WorkingDir != "/work" {
		t.Fatalf("expected workingDir from template, got %+v", s.WorkingDir)
	}
	if s.Env == nil || !hasEnv(*s.Env, "TMPL", "1") || !hasEnv(*s.Env, "OVR", "1") {
		t.Fatalf("expected env merge, got %+v", s.Env)
	}
	if s.Ports == nil || len(*s.Ports) != 1 || (*s.Ports)[0].ContainerPort != 8080 {
		t.Fatalf("expected ports from template, got %+v", s.Ports)
	}
	if s.VolumeMounts == nil || len(*s.VolumeMounts) != 1 || (*s.VolumeMounts)[0].MountPath != "/t" {
		t.Fatalf("expected volumeMounts from template, got %+v", s.VolumeMounts)
	}
	if s.Resources != &templateResources {
		t.Fatalf("expected resources from template")
	}
}

func TestMergeContainerPortPtrAndMergeVolumeMountPtr(t *testing.T) {
	templatePorts := []corev1.ContainerPort{{ContainerPort: 80}}
	overridePorts := []corev1.ContainerPort{{ContainerPort: 443}}
	mergedPorts := mergeContainerPortPtr(&templatePorts, &overridePorts)
	if mergedPorts == nil || len(*mergedPorts) != 2 {
		t.Fatalf("expected merged container ports, got %+v", mergedPorts)
	}

	templateMounts := []corev1.VolumeMount{{Name: "t", MountPath: "/t"}}
	overrideMounts := []corev1.VolumeMount{{Name: "o", MountPath: "/o"}}
	mergedMounts := mergeVolumeMountPtr(&templateMounts, &overrideMounts)
	if mergedMounts == nil || len(*mergedMounts) != 2 {
		t.Fatalf("expected merged volume mounts, got %+v", mergedMounts)
	}
}

func strPtr(v string) *string {
	return &v
}

func hasEnv(env []corev1.EnvVar, name, value string) bool {
	for _, item := range env {
		if item.Name == name && item.Value == value {
			return true
		}
	}
	return false
}

func getSidecarByName(sidecars []Sidecar, name string) *Sidecar {
	for i := range sidecars {
		if sidecars[i].Name == name {
			return &sidecars[i]
		}
	}
	return nil
}
