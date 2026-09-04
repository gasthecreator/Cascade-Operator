/*
Copyright 2026 Gideon Sanni.

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
	"crypto/tls"
	"flag"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/controller"
	spv1alpha2 "github.com/gasthecreator/Cascade-Operator/internal/mesh/linkerd/serviceprofile/v1alpha2"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
	"github.com/gasthecreator/Cascade-Operator/internal/notify"
	webhookv1alpha1 "github.com/gasthecreator/Cascade-Operator/internal/webhook/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(cascadev1alpha1.AddToScheme(scheme))
	utilruntime.Must(networkingv1.AddToScheme(scheme))
	utilruntime.Must(spv1alpha2.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var prometheusURL string
	var prometheusURLIstio string
	var prometheusURLLinkerd string
	var notifyWebhookURL string
	var watchNamespaces string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&prometheusURL, "prometheus-url", os.Getenv("PROMETHEUS_URL"),
		"Base URL of the Prometheus HTTP API (e.g. http://prometheus.istio-system.svc:9090). "+
			"Also read from PROMETHEUS_URL. Used for every CascadePolicy regardless of spec.mesh "+
			"unless the mesh-specific flag below is also set for that mesh. Empty disables metrics "+
			"polling entirely unless a mesh-specific flag is set.")
	flag.StringVar(&prometheusURLIstio, "prometheus-url-istio", os.Getenv("PROMETHEUS_URL_ISTIO"),
		"Base URL of the Prometheus HTTP API that scrapes Istio's proxies, for CascadePolicies with "+
			"spec.mesh: Istio (or unset, Istio being the default). Also read from PROMETHEUS_URL_ISTIO. "+
			"Overrides --prometheus-url for Istio-mesh policies only; set this (and/or "+
			"--prometheus-url-linkerd) instead of --prometheus-url when one operator process "+
			"reconciles CascadePolicies for both meshes at once, since each mesh's proxies are "+
			"typically scraped by a different Prometheus instance — a single --prometheus-url "+
			"pointed at only one mesh's Prometheus silently starves the other mesh's policies of "+
			"real data (they keep reconciling, they just never see a genuine reading).")
	flag.StringVar(&prometheusURLLinkerd, "prometheus-url-linkerd", os.Getenv("PROMETHEUS_URL_LINKERD"),
		"Base URL of the Prometheus HTTP API that scrapes Linkerd's proxies (e.g. linkerd-viz's own "+
			"Prometheus), for CascadePolicies with spec.mesh: Linkerd. Also read from "+
			"PROMETHEUS_URL_LINKERD. See --prometheus-url-istio's own help text for why this exists.")
	flag.StringVar(&notifyWebhookURL, "notify-webhook-url", os.Getenv("NOTIFY_WEBHOOK_URL"),
		"Slack-compatible incoming webhook URL for trip/restore notifications. "+
			"Also read from NOTIFY_WEBHOOK_URL. Empty disables notifications.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", os.Getenv("WATCH_NAMESPACES"),
		"Comma-separated list of namespaces to restrict CascadePolicy (and the mesh objects it "+
			"manages) watches to. Also read from WATCH_NAMESPACES. Empty (the default) watches every "+
			"namespace cluster-wide, matching the cluster-scoped ClusterRole config/rbac/role.yaml "+
			"grants by default. Setting this lets a deployment instead use "+
			"config/rbac-namespaced's per-namespace Role/RoleBinding pairs "+
			"(docs/security-threat-model.md's namespace-scoped-RBAC hardening step) — every namespace "+
			"named here needs a matching Role/RoleBinding, generated by "+
			"hack/generate-namespaced-rbac.sh, or CascadePolicy reconciliation in that namespace will "+
			"fail with a Forbidden error.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	// cacheOptions is left at its zero value (cluster-wide watches, the
	// ctrl.Manager default) unless --watch-namespaces/WATCH_NAMESPACES
	// names specific namespaces to restrict to — see that flag's own help
	// text for why a deployment would set this.
	var cacheOptions cache.Options
	if watchNamespaces != "" {
		namespaces := strings.Split(watchNamespaces, ",")
		defaultNamespaces := make(map[string]cache.Config, len(namespaces))
		for _, ns := range namespaces {
			ns = strings.TrimSpace(ns)
			if ns == "" {
				continue
			}
			defaultNamespaces[ns] = cache.Config{}
		}
		cacheOptions.DefaultNamespaces = defaultNamespaces
		setupLog.Info("Restricting CascadePolicy watches to specific namespaces", "namespaces", namespaces)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "3417ff6e.gideonsanni.dev",
		Cache:                  cacheOptions,
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
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	reconciler := &controller.CascadePolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if prometheusURL != "" {
		promClient, err := metrics.NewClient(prometheusURL, nil)
		if err != nil {
			setupLog.Error(err, "Invalid prometheus-url")
			os.Exit(1)
		}
		reconciler.Metrics = promClient
		setupLog.Info("Prometheus metrics client configured", "url", prometheusURL)
	} else if prometheusURLIstio == "" && prometheusURLLinkerd == "" {
		setupLog.Info("Prometheus URL not set; metrics polling disabled")
	}
	if prometheusURLIstio != "" {
		promClient, err := metrics.NewClient(prometheusURLIstio, nil)
		if err != nil {
			setupLog.Error(err, "Invalid prometheus-url-istio")
			os.Exit(1)
		}
		reconciler.MetricsIstio = promClient
		setupLog.Info("Istio-mesh Prometheus metrics client configured", "url", prometheusURLIstio)
	}
	if prometheusURLLinkerd != "" {
		promClient, err := metrics.NewClient(prometheusURLLinkerd, nil)
		if err != nil {
			setupLog.Error(err, "Invalid prometheus-url-linkerd")
			os.Exit(1)
		}
		reconciler.MetricsLinkerd = promClient
		setupLog.Info("Linkerd-mesh Prometheus metrics client configured", "url", prometheusURLLinkerd)
	}
	if notifyWebhookURL != "" {
		reconciler.Notify = notify.NewWebhookNotifier(notifyWebhookURL)
		setupLog.Info("Trip/restore notifications configured")
	} else {
		setupLog.Info("Notify webhook URL not set; trip/restore notifications disabled")
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "cascadepolicy")
		os.Exit(1)
	}
	// nolint:goconst
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err := webhookv1alpha1.SetupCascadePolicyWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "CascadePolicy")
			os.Exit(1)
		}
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
