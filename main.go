/*
Copyright 2022. projectsveltos.io. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"
	"regexp"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/deployer"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"

	"github.com/projectsveltos/classifier/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	setupLog                              = ctrl.Log.WithName("setup")
	metricsAddr                           string
	enableLeaderElection                  bool
	probeAddr                             string
	concurrentReconciles                  int
	workers                               int
	reportMode                            controllers.ReportMode
	tmpReportMode                         int
	managementClusterControlPlaneEndpoint string
)

const (
	defaultReconcilers  = 10
	defaultWorkers      = 10
	defaulReportMode    = int(controllers.CollectFromManagementCluster)
	cpEndpointREPattern = `^https://[0-9a-zA-Z][0-9a-zA-Z-.]+[0-9a-zA-Z]:\d+$`
)

func main() {
	scheme, err := controllers.InitScheme()
	if err != nil {
		os.Exit(1)
	}

	klog.InitFlags(nil)

	initFlags(pflag.CommandLine)
	pflag.CommandLine.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	reportMode = controllers.ReportMode(tmpReportMode)

	ctrl.SetLogger(klog.Background())

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "1aea9208.projectsveltos.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup the context that's going to be used in controllers and for the manager.
	ctx := ctrl.SetupSignalHandler()

	d := deployer.GetClient(ctx, ctrl.Log.WithName("deployer"), mgr.GetClient(), workers)
	controllers.RegisterFeatures(d, setupLog)

	logs.RegisterForLogSettings(ctx,
		libsveltosv1alpha1.ComponentClassifier, ctrl.Log.WithName("log-setter"),
		ctrl.GetConfigOrDie())

	if managementClusterControlPlaneEndpoint != "" {
		serverRegExp := regexp.MustCompile(cpEndpointREPattern)
		if !serverRegExp.MatchString(managementClusterControlPlaneEndpoint) {
			setupLog.WithValues("controlPlaneEndpoint", managementClusterControlPlaneEndpoint).Info("incorrect format")
			os.Exit(1)
		}
	}

	if err = (&controllers.ClassifierReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		ConcurrentReconciles: concurrentReconciles,
		ClusterMap:           make(map[corev1.ObjectReference]*libsveltosset.Set),
		ClassifierMap:        make(map[corev1.ObjectReference]*libsveltosset.Set),
		Deployer:             d,
		ClassifierReportMode: reportMode,
		ControlPlaneEndpoint: managementClusterControlPlaneEndpoint,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Classifier")
		os.Exit(1)
	}
	if err = (&controllers.ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Cluster")
		os.Exit(1)
	}
	if err = (&controllers.SveltosClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SveltosCluster")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	setupChecks(mgr)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func initFlags(fs *pflag.FlagSet) {
	fs.IntVar(&tmpReportMode,
		"report-mode",
		defaulReportMode,
		"Indicates how ClassifierReport needs to be collected")

	fs.StringVar(&managementClusterControlPlaneEndpoint,
		"control-plane-endpoint",
		"",
		"The management cluster controlplane endpoint. Format <ip>:<port>.")

	fs.StringVar(&metricsAddr,
		"metrics-bind-address",
		":8080",
		"The address the metric endpoint binds to.")

	fs.StringVar(&probeAddr,
		"health-probe-bind-address",
		":8081",
		"The address the probe endpoint binds to.")

	fs.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	fs.IntVar(&workers,
		"worker-number",
		defaultWorkers,
		"Number of worker. Workers are used to deploy classifiers in CAPI clusters")

	fs.IntVar(&concurrentReconciles,
		"concurrent-reconciles",
		defaultReconcilers,
		"concurrent reconciles is the maximum number of concurrent Reconciles which can be run. Defaults to 10")
}

func setupChecks(mgr ctrl.Manager) {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}
}
