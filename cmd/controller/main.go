package main

import (
	"flag"
	"os"
	"time"

	browserv1 "github.com/alcounit/browser-controller/apis/browser/v1"
	configv1 "github.com/alcounit/browser-controller/apis/browserconfig/v1"
	"github.com/alcounit/browser-controller/controllers/browser"
	"github.com/alcounit/browser-controller/controllers/browserconfig"
	"github.com/alcounit/browser-controller/store"
	"github.com/go-logr/logr"
	"github.com/rs/zerolog"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = browserv1.AddToScheme(scheme)
	_ = configv1.AddToScheme(scheme)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager.")
	flag.Parse()

	// zerolog setup
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	zlogger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Wrap zerolog
	ctrl.SetLogger(logr.New(&zerologSink{zl: zlogger}))
	log := ctrl.Log

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}

	// Create manager
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "controller.selenosis.io",
	})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Add BrowserConfig controller
	browserCfg := browserconfig.NewBrowserConfigReconciler(mgr.GetClient(), mgr.GetScheme())
	if err = browserCfg.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create browser config controller")
		os.Exit(1)
	}

	// Create BrowserConfigStore and register it as a manager runnable
	browserCfgStore := store.NewBrowserConfigStore()
	if err := mgr.Add(browserCfgStore.WithCache(mgr.GetCache(), ctrl.Log)); err != nil {
		log.Error(err, "unable to add browser config store to manager")
		os.Exit(1)
	}

	// Add Browser controller
	browserCtrl := browser.NewBrowserReconciler(mgr.GetClient(), browserCfgStore, mgr.GetScheme())
	if err = browserCtrl.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create browser controller")
		os.Exit(1)
	}

	// Setup health and readiness probes
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type zerologSink struct {
	zl zerolog.Logger
}

func (s *zerologSink) Init(info logr.RuntimeInfo) {}

func (s *zerologSink) Enabled(level int) bool {
	if level <= 0 {
		return true
	}
	if level == 1 {
		return s.zl.GetLevel() <= zerolog.DebugLevel
	}
	if level >= 2 {
		return s.zl.GetLevel() <= zerolog.TraceLevel
	}
	return false
}

func (s *zerologSink) Info(level int, msg string, keysAndValues ...interface{}) {
	event := s.zl.Info()
	if level == 1 {
		event = s.zl.Debug()
	} else if level >= 2 {
		event = s.zl.Trace()
	}
	for i := 0; i < len(keysAndValues)-1; i += 2 {
		if key, ok := keysAndValues[i].(string); ok {
			event = event.Interface(key, keysAndValues[i+1])
		}
	}
	event.Msg(msg)
}

func (s *zerologSink) Error(err error, msg string, keysAndValues ...interface{}) {
	event := s.zl.Error().Err(err)
	for i := 0; i < len(keysAndValues)-1; i += 2 {
		if key, ok := keysAndValues[i].(string); ok {
			event = event.Interface(key, keysAndValues[i+1])
		}
	}
	event.Msg(msg)
}

func (s *zerologSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	child := s.zl.With()
	for i := 0; i < len(keysAndValues)-1; i += 2 {
		if key, ok := keysAndValues[i].(string); ok {
			child = child.Interface(key, keysAndValues[i+1])
		}
	}
	return &zerologSink{zl: child.Logger()}
}

func (s *zerologSink) WithName(name string) logr.LogSink {
	return &zerologSink{zl: s.zl.With().Str("logger", name).Logger()}
}
