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
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=debuggingconfigurations,verbs=get;list;watch
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

const (
	accessRequestClassifierLabel = "projectsveltos.io/classifierrequest"
)

var (
	version string
)

func InitScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := libsveltosv1beta1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

// getKeyFromObject returns the Key that can be used in the internal reconciler maps.
func getKeyFromObject(scheme *runtime.Scheme, obj client.Object) *corev1.ObjectReference {
	addTypeInformationToObject(scheme, obj)

	return &corev1.ObjectReference{
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
		APIVersion: obj.GetObjectKind().GroupVersionKind().String(),
	}
}

func addTypeInformationToObject(scheme *runtime.Scheme, obj client.Object) {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		panic(1)
	}

	for _, gvk := range gvks {
		if gvk.Kind == "" {
			continue
		}
		if gvk.Version == "" || gvk.Version == runtime.APIVersionInternal {
			continue
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		break
	}
}

func getClusterRefFromClassifierReport(report *libsveltosv1beta1.ClassifierReport) *corev1.ObjectReference {
	cluster := corev1.ObjectReference{
		Namespace: report.Spec.ClusterNamespace,
		Name:      report.Spec.ClusterName,
	}
	switch report.Spec.ClusterType {
	case libsveltosv1beta1.ClusterTypeCapi:
		cluster.APIVersion = clusterv1.GroupVersion.String()
		cluster.Kind = "Cluster"
	case libsveltosv1beta1.ClusterTypeSveltos:
		cluster.APIVersion = libsveltosv1beta1.GroupVersion.String()
		cluster.Kind = libsveltosv1beta1.SveltosClusterKind
	default:
		panic(1)
	}
	return &cluster
}

func SetVersion(v string) {
	version = v
}

func getVersion() string {
	return version
}

func convertPointerSliceToValueSlice(pointerSlice []*unstructured.Unstructured) []unstructured.Unstructured {
	valueSlice := make([]unstructured.Unstructured, len(pointerSlice))
	for i, ptr := range pointerSlice {
		if ptr != nil {
			valueSlice[i] = *ptr
		}
	}
	return valueSlice
}

// Identifies and removes sveltos-agent deployments that are no longer associated with active clusters
// Identifies and removes classifierReports instances from clusters that are no longer existing
func removeStaleClassifierResources(ctx context.Context, logger logr.Logger) {
	listOptions := []client.ListOption{
		client.MatchingLabels{
			sveltosAgentFeatureLabelKey: sveltosAgent,
		},
	}

	const interval = 5 * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping detection and removal of stale classifier resources")
			return
		case <-ticker.C:
			c := getManagementClusterClient()
			sveltosAgentDeployments := &appsv1.DeploymentList{}
			err := c.List(ctx, sveltosAgentDeployments, listOptions...)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to collect sveltos-agent deployment: %v", err))
				continue
			}

			for i := range sveltosAgentDeployments.Items {
				depl := &sveltosAgentDeployments.Items[i]

				exist, clusterNs, clusterName, clusterType := deplAssociatedClusterExist(ctx, c, depl, logger)
				if !exist {
					_, err = cleanClusterStaleResources(ctx, c, clusterNs, clusterName, clusterType, logger)
					if err != nil {
						logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove sveltos-agent resources: %v", err))
						continue
					}
				}
			}
		}
	}
}

// removeStaleClassifierReports periodically removes ClassifierReports whose referenced cluster
// no longer exists. This complements the cleanup done in syncAndGetMatchingClusters, which only
// catches reports with Spec.Match set to true; a report left over with Spec.Match false (or unset)
// for a since-deleted cluster is otherwise never removed and gets retried by every Classifier
// reconcile forever.
func removeStaleClassifierReports(ctx context.Context, logger logr.Logger) {
	const interval = 5 * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping detection and removal of stale ClassifierReports")
			return
		case <-ticker.C:
			if err := pruneClassifierReportsForDeletedClusters(ctx, getManagementClusterClient(), logger); err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to prune stale ClassifierReports: %v", err))
			}
		}
	}
}

// pruneClassifierReportsForDeletedClusters removes every ClassifierReport referencing a cluster
// that no longer exists, regardless of the report's Spec.Match value.
func pruneClassifierReportsForDeletedClusters(ctx context.Context, c client.Client, logger logr.Logger) error {
	classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
	if err := c.List(ctx, classifierReportList); err != nil {
		return fmt.Errorf("failed to list ClassifierReports: %w", err)
	}

	// Multiple ClassifierReports (from different Classifier instances) can reference the same
	// cluster; check each distinct cluster only once per sweep.
	checked := make(map[corev1.ObjectReference]bool)

	for i := range classifierReportList.Items {
		report := &classifierReportList.Items[i]
		if report.Spec.ClusterNamespace == "" {
			continue
		}

		cluster := getClusterRefFromClassifierReport(report)
		if checked[*cluster] {
			continue
		}
		checked[*cluster] = true

		_, err := clusterproxy.GetCluster(ctx, c, cluster.Namespace, cluster.Name, clusterproxy.GetClusterType(cluster))
		if err == nil {
			continue
		}
		if !apierrors.IsNotFound(err) {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get cluster %s:%s/%s: %v",
				cluster.Kind, cluster.Namespace, cluster.Name, err))
			continue
		}

		logger.V(logs.LogInfo).Info(fmt.Sprintf(
			"cluster %s:%s/%s no longer exists, removing its ClassifierReports",
			cluster.Kind, cluster.Namespace, cluster.Name))
		if err := removeClusterClassifierReports(ctx, c, cluster.Namespace, cluster.Name,
			clusterproxy.GetClusterType(cluster), logger); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove stale ClassifierReports: %v", err))
		}
	}

	return nil
}

func deplAssociatedClusterExist(ctx context.Context, c client.Client, depl *appsv1.Deployment,
	logger logr.Logger) (exist bool, clusterName, clusterNamespace string, clusterType libsveltosv1beta1.ClusterType) {

	if depl.Labels == nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("driftDetection %s/%s has no label",
			depl.Namespace, depl.Name))
		return true, "", "", ""
	}

	clusterNamespace, ok := depl.Labels[sveltosAgentClusterNamespaceLabel]
	if !ok {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("sveltos-agent %s/%s has no %s label",
			depl.Namespace, depl.Name, sveltosAgentClusterNamespaceLabel))
		return true, "", "", ""
	}

	clusterName, ok = depl.Labels[sveltosAgentClusterNameLabel]
	if !ok {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("sveltos-agent %s/%s has no %s label",
			depl.Namespace, depl.Name, sveltosAgentClusterNameLabel))
		return true, "", "", ""
	}

	clusterTypeString, ok := depl.Labels[sveltosAgentClusterTypeLabel]
	if !ok {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("sveltos-agent %s/%s has no %s label",
			depl.Namespace, depl.Name, sveltosAgentClusterTypeLabel))
		return true, "", "", ""
	}

	if strings.EqualFold(clusterTypeString, string(libsveltosv1beta1.ClusterTypeSveltos)) {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	} else if strings.EqualFold(clusterTypeString, string(libsveltosv1beta1.ClusterTypeCapi)) {
		clusterType = libsveltosv1beta1.ClusterTypeCapi
	}

	_, err := clusterproxy.GetCluster(ctx, c, clusterNamespace, clusterName, clusterType)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, clusterNamespace, clusterName, clusterType
		}
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get cluster %s:%s/%s: %v",
			clusterNamespace, clusterName, clusterTypeString, err))
	}

	return true, "", "", ""
}

func stringPtr(s string) *string {
	return &s
}

// listClassifierReportsForClassifier returns all ClassifierReports labeled for the given classifier.
func listClassifierReportsForClassifier(ctx context.Context, c client.Client,
	classifierName string) ([]libsveltosv1beta1.ClassifierReport, error) {

	reportList := &libsveltosv1beta1.ClassifierReportList{}
	if err := c.List(ctx, reportList, client.MatchingLabels{
		libsveltosv1beta1.ClassifierlNameLabel: classifierName,
	}); err != nil {
		return nil, err
	}
	return reportList.Items, nil
}

// getClassifierReportForCluster returns the ClassifierReport for a (classifier, cluster) pair.
func getClassifierReportForCluster(ctx context.Context, c client.Client,
	classifierName string, cluster *corev1.ObjectReference) (*libsveltosv1beta1.ClassifierReport, error) {

	clusterType := clusterproxy.GetClusterType(cluster)
	reportName := libsveltosv1beta1.GetClassifierReportName(classifierName, cluster.Name, &clusterType)
	report := &libsveltosv1beta1.ClassifierReport{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: reportName}, report); err != nil {
		return nil, err
	}
	return report, nil
}

// ensureClassifierReportForCluster creates a ClassifierReport for a cluster if one does not already exist.
// The report is initialized with Match=false; the agent updates Spec.Match on its next reconcile.
func ensureClassifierReportForCluster(ctx context.Context, c client.Client,
	classifierName string, cluster *corev1.ObjectReference) error {

	clusterType := clusterproxy.GetClusterType(cluster)
	reportName := libsveltosv1beta1.GetClassifierReportName(classifierName, cluster.Name, &clusterType)

	existing := &libsveltosv1beta1.ClassifierReport{}
	err := c.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: reportName}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	report := &libsveltosv1beta1.ClassifierReport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      reportName,
			Labels:    libsveltosv1beta1.GetClassifierReportLabels(classifierName, cluster.Name, &clusterType),
		},
		Spec: libsveltosv1beta1.ClassifierReportSpec{
			ClusterNamespace: cluster.Namespace,
			ClusterName:      cluster.Name,
			ClusterType:      clusterType,
			ClassifierName:   classifierName,
			Match:            false,
		},
	}
	if createErr := c.Create(ctx, report); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
		return createErr
	}
	return nil
}

// updateClassifierReportDeploymentStatus patches ClassifierReport.Status with deployment tracking data
// (hash, deployment status, failure message) from a processClassifier result.
func updateClassifierReportDeploymentStatus(ctx context.Context, c client.Client,
	classifierName string, cluster *corev1.ObjectReference,
	clusterInfo *libsveltosv1beta1.ClusterInfo) error {

	report, err := getClassifierReportForCluster(ctx, c, classifierName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	patch := client.MergeFrom(report.DeepCopy())
	report.Status.Hash = clusterInfo.Hash
	report.Status.DeploymentStatus = &clusterInfo.Status
	report.Status.FailureMessage = clusterInfo.FailureMessage
	return c.Status().Patch(ctx, report, patch)
}

// updateClassifierReportLabelStatus patches ClassifierReport.Status with label management data.
// Pass nil slices to clear the tracking when a cluster stops matching.
func updateClassifierReportLabelStatus(ctx context.Context, c client.Client,
	classifierName string, cluster *corev1.ObjectReference,
	managed []string, unmanaged []libsveltosv1beta1.UnManagedLabel) error {

	report, err := getClassifierReportForCluster(ctx, c, classifierName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	patch := client.MergeFrom(report.DeepCopy())
	report.Status.ManagedLabels = managed
	report.Status.UnManagedLabels = unmanaged
	return c.Status().Patch(ctx, report, patch)
}

func sortPatches(patches []libsveltosv1beta1.Patch) {
	sort.Slice(patches, func(i, j int) bool {
		p1 := patches[i].Target
		p2 := patches[j].Target

		// Handle cases where Target might be nil
		if p1 == nil && p2 == nil {
			return false
		}
		if p1 == nil {
			return true // Nil targets first
		}
		if p2 == nil {
			return false
		}

		// Comparison chain
		if p1.Group != p2.Group {
			return p1.Group < p2.Group
		}
		if p1.Version != p2.Version {
			return p1.Version < p2.Version
		}
		if p1.Kind != p2.Kind {
			return p1.Kind < p2.Kind
		}
		if p1.Namespace != p2.Namespace {
			return p1.Namespace < p2.Namespace
		}
		if p1.Name != p2.Name {
			return p1.Name < p2.Name
		}

		// Finally, sort by the patch content itself if targets are identical
		return patches[i].Patch < patches[j].Patch
	})
}
