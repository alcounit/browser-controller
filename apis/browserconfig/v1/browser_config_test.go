package v1

// import (
// 	"testing"
// 	"time"

// 	corev1 "k8s.io/api/core/v1"
// 	"k8s.io/apimachinery/pkg/api/resource"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// )

// func TestMergeWithTemplate_Timeouts(t *testing.T) {
// 	defaultTimeout := metav1.Duration{Duration: 10 * time.Minute}
// 	overrideTimeout := metav1.Duration{Duration: 20 * time.Minute}

// 	spec := BrowserConfigSpec{
// 		Template: &Template{
// 			SessionTimeout: &defaultTimeout,
// 		},
// 		Browsers: map[string]map[string]*BrowserVersionConfig{
// 			"chrome": {
// 				"100.0": {
// 					Image:          "chrome:100",
// 					Path:           "/wd/hub",
// 					SessionTimeout: &overrideTimeout,
// 				},
// 			},
// 		},
// 	}

// 	spec.MergeWithTemplate()

// 	cfg := spec.Browsers["chrome"]["100.0"]
// 	if cfg.SessionTimeout.Duration != 20*time.Minute {
// 		t.Errorf("expected override sessionTimeout=20m, got %v", cfg.SessionTimeout.Duration)
// 	}
// }

// func TestMergeWithTemplate_Labels(t *testing.T) {
// 	templateLabels := map[string]string{"app": "browsers", "tier": "backend"}
// 	overrideLabels := map[string]string{"tier": "frontend", "env": "test"}

// 	spec := BrowserConfigSpec{
// 		Template: &Template{Labels: &templateLabels},
// 		Browsers: map[string]map[string]*BrowserVersionConfig{
// 			"firefox": {
// 				"101.0": {
// 					Image:  "firefox:101",
// 					Path:   "/wd/hub",
// 					Labels: &overrideLabels,
// 				},
// 			},
// 		},
// 	}

// 	spec.MergeWithTemplate()
// 	cfg := spec.Browsers["firefox"]["101.0"]

// 	if (*cfg.Labels)["app"] != "browsers" {
// 		t.Errorf("expected template label app=browsers, got %v", (*cfg.Labels)["app"])
// 	}
// 	if (*cfg.Labels)["tier"] != "frontend" {
// 		t.Errorf("expected override tier=frontend, got %v", (*cfg.Labels)["tier"])
// 	}
// 	if (*cfg.Labels)["env"] != "test" {
// 		t.Errorf("expected override env=test, got %v", (*cfg.Labels)["env"])
// 	}
// }

// func TestMergeWithTemplate_Env(t *testing.T) {
// 	templateEnv := []corev1.EnvVar{{Name: "TZ", Value: "UTC"}, {Name: "DEBUG", Value: "false"}}
// 	overrideEnv := []corev1.EnvVar{{Name: "DEBUG", Value: "true"}, {Name: "CUSTOM", Value: "42"}}

// 	spec := BrowserConfigSpec{
// 		Template: &Template{Env: &templateEnv},
// 		Browsers: map[string]map[string]*BrowserVersionConfig{
// 			"edge": {
// 				"110.0": {Image: "edge:110", Path: "/wd/hub", Env: &overrideEnv},
// 			},
// 		},
// 	}
// 	spec.MergeWithTemplate()
// 	cfg := spec.Browsers["edge"]["110.0"]

// 	envs := map[string]string{}
// 	for _, e := range *cfg.Env {
// 		envs[e.Name] = e.Value
// 	}

// 	if envs["TZ"] != "UTC" {
// 		t.Errorf("expected TZ=UTC from template, got %v", envs["TZ"])
// 	}
// 	if envs["DEBUG"] != "true" {
// 		t.Errorf("expected DEBUG=true override, got %v", envs["DEBUG"])
// 	}
// 	if envs["CUSTOM"] != "42" {
// 		t.Errorf("expected CUSTOM=42 override, got %v", envs["CUSTOM"])
// 	}
// }

// func TestMergeWithTemplate_Sidecars(t *testing.T) {
// 	templateSC := []Sidecar{{Name: "metrics", Image: "metrics:1.0"}, {Name: "logger", Image: "logger:1.0"}}
// 	overrideSC := []Sidecar{{Name: "logger", Image: "logger:2.0"}, {Name: "debug", Image: "debug:latest"}}

// 	spec := BrowserConfigSpec{
// 		Template: &Template{Sidecars: &templateSC},
// 		Browsers: map[string]map[string]*BrowserVersionConfig{
// 			"safari": {
// 				"15.0": {Image: "safari:15", Path: "/wd/hub", Sidecars: &overrideSC},
// 			},
// 		},
// 	}
// 	spec.MergeWithTemplate()
// 	cfg := spec.Browsers["safari"]["15.0"]

// 	names := map[string]string{}
// 	for _, sc := range *cfg.Sidecars {
// 		names[sc.Name] = sc.Image
// 	}

// 	if names["metrics"] != "metrics:1.0" {
// 		t.Errorf("expected template sidecar metrics:1.0, got %v", names["metrics"])
// 	}
// 	if names["logger"] != "logger:2.0" {
// 		t.Errorf("expected override logger:2.0, got %v", names["logger"])
// 	}
// 	if names["debug"] != "debug:latest" {
// 		t.Errorf("expected override debug:latest, got %v", names["debug"])
// 	}
// }

// func TestMergeWithTemplate_Resources(t *testing.T) {
// 	templateRes := corev1.ResourceRequirements{
// 		Requests: corev1.ResourceList{"cpu": resource.MustParse("100m")},
// 		Limits:   corev1.ResourceList{"cpu": resource.MustParse("200m")},
// 	}
// 	overrideRes := corev1.ResourceRequirements{
// 		Requests: corev1.ResourceList{"cpu": resource.MustParse("300m")},
// 		Limits:   corev1.ResourceList{"cpu": resource.MustParse("500m")},
// 	}

// 	spec := BrowserConfigSpec{
// 		Template: &Template{Resources: &templateRes},
// 		Browsers: map[string]map[string]*BrowserVersionConfig{
// 			"opera": {
// 				"12.0": {Image: "opera:12", Path: "/wd/hub", Resources: &overrideRes},
// 			},
// 		},
// 	}
// 	spec.MergeWithTemplate()
// 	cfg := spec.Browsers["opera"]["12.0"]

// 	if cfg.Resources.Requests.Cpu().String() != "300m" {
// 		t.Errorf("expected override requests cpu=300m, got %v", cfg.Resources.Requests.Cpu().String())
// 	}
// 	if cfg.Resources.Limits.Cpu().String() != "500m" {
// 		t.Errorf("expected override limits cpu=500m, got %v", cfg.Resources.Limits.Cpu().String())
// 	}
// }

// func TestMergeWithTemplate_SecurityContext(t *testing.T) {
// 	runAsNonRoot := true
// 	templateSC := corev1.SecurityContext{RunAsNonRoot: &runAsNonRoot}

// 	spec := BrowserConfigSpec{
// 		Template: &Template{SecurityContext: &templateSC},
// 		Browsers: map[string]map[string]*BrowserVersionConfig{
// 			"brave": {
// 				"1.0": {Image: "brave:1", Path: "/wd/hub"},
// 			},
// 		},
// 	}
// 	spec.MergeWithTemplate()
// 	cfg := spec.Browsers["brave"]["1.0"]

// 	if cfg.SecurityContext == nil || cfg.SecurityContext.RunAsNonRoot == nil || !*cfg.SecurityContext.RunAsNonRoot {
// 		t.Errorf("expected SecurityContext.RunAsNonRoot=true inherited from template, got %+v", cfg.SecurityContext)
// 	}
// }
