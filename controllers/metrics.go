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

package controllers

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/projectsveltos/libsveltos/lib/clusterproxy"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

const (
	// metricNamespace prefixes every custom metric in this file, purely to avoid name collisions with
	// unrelated tools scraped by the same Prometheus instance. Deliberately a fixed literal, not derived
	// from getSveltosNamespace() (the Kubernetes namespace this component happens to be deployed into,
	// which is user-configurable per install): a metric name prefix should stay stable across every
	// install so dashboards/alerts built against it work the same way everywhere, regardless of which
	// Kubernetes namespace a given cluster chose to deploy classifier into.
	metricNamespace = "projectsveltos"

	metricClusterNameLabel      = "cluster_name"
	metricClusterNamespaceLabel = "cluster_namespace"
	metricClusterTypeLabel      = "cluster_type"
	metricStatusLabel           = "status"
	metricClassifierNameLabel   = "classifier_name"

	statusSuccess = "success"
	statusFailure = "failure"
)

var (
	// reconcileDurationHistogram tracks how long it takes to program a Classifier (deploy sveltos-agent,
	// required CRDs, and the Classifier instance itself) on a workload cluster. Labeled by cluster so it
	// can be filtered to a single cluster, aggregated to a namespace, or averaged fleet-wide.
	//
	// Not labeled by Classifier name: this is observed from programDuration, which is invoked as a
	// libsveltos/lib/deployer.MetricHandler callback — a type shared with several other components
	// (addon-controller, event-manager, healthcheck-manager, etc.). That callback only carries cluster
	// identity and featureID, not the Classifier that requested the work, so adding that label here would
	// require changing the shared MetricHandler signature across all of them. reconcileOutcomeCounter and
	// reconcileLastSuccessTimestampGauge below get the Classifier name instead, since both are recorded
	// from classifier-local code (deployClassifier) that already has it on hand.
	//
	// Replaces the previous programClassifierDurationHistogram/newClassifierHistogram pair, which had the
	// same per-cluster metric-name cardinality bug fixed in addon-controller and event-manager: baking
	// cluster identity into the Prometheus metric Namespace (and thus its exported name) instead of using
	// a label, producing one distinct, never-unregistered metric per cluster.
	reconcileDurationHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "reconcile_duration_seconds",
			Help:      "Duration distribution of programming a Classifier on a workload cluster",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 120},
		},
		[]string{metricClusterTypeLabel, metricClusterNamespaceLabel, metricClusterNameLabel},
	)

	// reconcileOutcomeCounter tracks terminal reconcile outcomes (success/failure) per Classifier and
	// cluster. Incremented from deployClassifier, the one place that already knows whether processing a
	// cluster ended up Provisioned (success) or Failed (failure).
	reconcileOutcomeCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reconcile_outcome_total",
			Help:      "Total number of terminal reconcile outcomes for a Classifier on a cluster, by outcome",
		},
		[]string{metricClusterTypeLabel, metricClusterNamespaceLabel, metricClusterNameLabel, metricStatusLabel,
			metricClassifierNameLabel},
	)

	// reconcileLastSuccessTimestampGauge records when a Classifier last reached a successful terminal
	// state (Provisioned) for a cluster. Only ever moves forward on success and is left untouched on
	// failure, so it answers "how long has it been since this last worked" even for something that fails
	// intermittently rather than in a tight consecutive streak.
	reconcileLastSuccessTimestampGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "reconcile_last_success_timestamp_seconds",
			Help:      "Unix timestamp of the last successful (Provisioned) terminal outcome for a Classifier on a cluster",
		},
		[]string{metricClusterTypeLabel, metricClusterNamespaceLabel, metricClusterNameLabel, metricClassifierNameLabel},
	)

	// matchingClustersGauge tracks how many clusters currently match a given Classifier's rules
	// (kubernetesVersionConstraints, deployedResourceConstraint) and so receive its classifierLabels. Set
	// from ClassifierReconciler right after it computes the matching set, independent of any per-cluster
	// deploy activity.
	matchingClustersGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "matching_clusters",
			Help:      "Number of clusters currently matching a Classifier's rules",
		},
		[]string{metricClassifierNameLabel},
	)

	// labelConflictsGauge tracks how many clusters currently have at least one label key this Classifier
	// wants to manage but cannot, because another Classifier already owns that key on that cluster (see
	// controllers/keymanager). A gauge, not a counter: it reflects the current conflict state, not a
	// historical count, so it answers "is this Classifier losing label ownership right now."
	labelConflictsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "label_conflicts",
			Help:      "Number of clusters where this Classifier has at least one label key it cannot manage due to a conflict",
		},
		[]string{metricClassifierNameLabel},
	)

	// mgmtClusterMatchingClustersGauge is the ManagementClusterClassifier equivalent of
	// matchingClustersGauge. Kept as a separate metric (rather than an added label on
	// matchingClustersGauge) because ManagementClusterClassifier is a distinct CRD/reconciler with its own
	// matching semantics (a Lua function evaluated over management-cluster resources, not
	// kubernetesVersionConstraints/deployedResourceConstraint against managed clusters).
	mgmtClusterMatchingClustersGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "mgmt_cluster_matching_clusters",
			Help:      "Number of clusters currently matching a ManagementClusterClassifier's classificationLua",
		},
		[]string{metricClassifierNameLabel},
	)

	// mgmtClusterLabelConflictsGauge is the ManagementClusterClassifier equivalent of
	// labelConflictsGauge, kept separate for the same reason as mgmtClusterMatchingClustersGauge above.
	mgmtClusterLabelConflictsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "mgmt_cluster_label_conflicts",
			Help:      "Number of clusters where this ManagementClusterClassifier has at least one label key it cannot manage due to a conflict",
		},
		[]string{metricClassifierNameLabel},
	)
)

//nolint:gochecknoinits // forced pattern, can't workaround
func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(reconcileDurationHistogram, reconcileOutcomeCounter,
		reconcileLastSuccessTimestampGauge, matchingClustersGauge, labelConflictsGauge,
		mgmtClusterMatchingClustersGauge, mgmtClusterLabelConflictsGauge)
}

func programDuration(elapsed time.Duration, clusterNamespace, clusterName, featureID string,
	clusterType libsveltosv1beta1.ClusterType, logger logr.Logger) {

	if featureID != string(libsveltosv1beta1.FeatureClassifier) {
		return
	}

	reconcileDurationHistogram.With(prometheus.Labels{
		metricClusterTypeLabel:      string(clusterType),
		metricClusterNamespaceLabel: clusterNamespace,
		metricClusterNameLabel:      clusterName,
	}).Observe(elapsed.Seconds())

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("recorded duration for %s/%s %s: %s",
		clusterNamespace, clusterName, featureID, elapsed))
}

// trackReconcileOutcome records a terminal reconcile outcome for a Classifier on a cluster. success is
// true for SveltosStatusProvisioned, false for SveltosStatusFailed. Non-terminal statuses (Provisioning,
// Removing, Removed) must not call this.
func trackReconcileOutcome(clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType,
	success bool, classifierName string, logger logr.Logger) {

	status := statusFailure
	if success {
		status = statusSuccess
	}

	reconcileOutcomeCounter.With(prometheus.Labels{
		metricClusterTypeLabel:      string(clusterType),
		metricClusterNamespaceLabel: clusterNamespace,
		metricClusterNameLabel:      clusterName,
		metricStatusLabel:           status,
		metricClassifierNameLabel:   classifierName,
	}).Inc()

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Tracking reconcile outcome for %s %s/%s classifier %s: %s",
		clusterType, clusterNamespace, clusterName, classifierName, status))
}

// trackLastSuccess records the timestamp of the most recent successful (Provisioned) terminal outcome for
// a Classifier on a cluster. Only called from the success branch.
func trackLastSuccess(clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType,
	timestamp time.Time, classifierName string, logger logr.Logger) {

	reconcileLastSuccessTimestampGauge.With(prometheus.Labels{
		metricClusterTypeLabel:      string(clusterType),
		metricClusterNamespaceLabel: clusterNamespace,
		metricClusterNameLabel:      clusterName,
		metricClassifierNameLabel:   classifierName,
	}).Set(float64(timestamp.Unix()))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Tracking last success for %s %s/%s classifier %s: %s",
		clusterType, clusterNamespace, clusterName, classifierName, timestamp))
}

// trackMatchingClusters records how many clusters currently match a Classifier's rules.
func trackMatchingClusters(classifierName string, count int, logger logr.Logger) {
	matchingClustersGauge.With(prometheus.Labels{
		metricClassifierNameLabel: classifierName,
	}).Set(float64(count))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Tracking matching clusters for classifier %s: %d",
		classifierName, count))
}

// trackLabelConflicts records how many clusters currently have at least one label key this Classifier
// cannot manage due to a conflict with another Classifier.
func trackLabelConflicts(classifierName string, count int, logger logr.Logger) {
	labelConflictsGauge.With(prometheus.Labels{
		metricClassifierNameLabel: classifierName,
	}).Set(float64(count))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Tracking label conflicts for classifier %s: %d",
		classifierName, count))
}

// trackMgmtClusterMatchingClusters records how many clusters currently match a
// ManagementClusterClassifier's classificationLua.
func trackMgmtClusterMatchingClusters(classifierName string, count int, logger logr.Logger) {
	mgmtClusterMatchingClustersGauge.With(prometheus.Labels{
		metricClassifierNameLabel: classifierName,
	}).Set(float64(count))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Tracking matching clusters for managementclusterclassifier %s: %d",
		classifierName, count))
}

// trackMgmtClusterLabelConflicts records how many clusters currently have at least one label key this
// ManagementClusterClassifier cannot manage due to a conflict with another Classifier.
func trackMgmtClusterLabelConflicts(classifierName string, count int, logger logr.Logger) {
	mgmtClusterLabelConflictsGauge.With(prometheus.Labels{
		metricClassifierNameLabel: classifierName,
	}).Set(float64(count))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Tracking label conflicts for managementclusterclassifier %s: %d",
		classifierName, count))
}

// trackClassifierOutcome records reconcile_outcome_total/reconcile_last_success_timestamp_seconds for
// terminal Classifier statuses (Provisioned/Failed). Non-terminal statuses (Provisioning, Removing,
// Removed) are left untouched: they are not outcomes, they are still in flight.
func trackClassifierOutcome(classifierName string, cluster *corev1.ObjectReference,
	clusterInfo *libsveltosv1beta1.ClusterInfo, logger logr.Logger) {

	clusterType := clusterproxy.GetClusterType(cluster)

	switch clusterInfo.Status {
	case libsveltosv1beta1.SveltosStatusProvisioned:
		trackReconcileOutcome(cluster.Namespace, cluster.Name, clusterType, true, classifierName, logger)
		trackLastSuccess(cluster.Namespace, cluster.Name, clusterType, time.Now(), classifierName, logger)
	case libsveltosv1beta1.SveltosStatusFailed:
		trackReconcileOutcome(cluster.Namespace, cluster.Name, clusterType, false, classifierName, logger)
	case libsveltosv1beta1.SveltosStatusProvisioning, libsveltosv1beta1.SveltosStatusFailedNonRetriable,
		libsveltosv1beta1.SveltosStatusRemoving, libsveltosv1beta1.SveltosStatusRemoved:
		// Not a terminal outcome; nothing to record.
	}
}
