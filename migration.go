/*
Copyright 2026. projectsveltos.io. All rights reserved.

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

// runMigration is a one-shot operation run as a Kubernetes init container
// (via /manager --migrate) before the classifier controller starts.
// It moves per-cluster state previously stored in Classifier.Status
// (ClusterInfo and MachingClusterStatuses) into the Status subresource of the
// corresponding ClassifierReport objects.
//
// A Classifier instance is skipped when both arrays are already empty, so the
// operation is safe to re-run if the init container restarts.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// runMigration lists all Classifier instances and migrates any that still carry
// per-cluster data in the deprecated ClusterInfo / MachingClusterStatuses fields.
func runMigration(ctx context.Context, c client.Client, log logr.Logger) error {
	classifierList := &libsveltosv1beta1.ClassifierList{}
	if err := c.List(ctx, classifierList); err != nil {
		return fmt.Errorf("listing Classifier instances: %w", err)
	}

	for i := range classifierList.Items {
		classifier := &classifierList.Items[i]

		//nolint:staticcheck // deprecated, migration only
		if len(classifier.Status.ClusterInfo) == 0 && len(classifier.Status.MachingClusterStatuses) == 0 {
			log.V(1).Info("skipping (already migrated or no data)", "classifier", classifier.Name)
			continue
		}

		log.Info("migrating", "classifier", classifier.Name,
			"clusterInfoCount", len(classifier.Status.ClusterInfo), //nolint:staticcheck // deprecated, migration only
			"matchingCount", len(classifier.Status.MachingClusterStatuses)) //nolint:staticcheck // deprecated, migration only

		if err := migrateOneClassifier(ctx, c, classifier, log); err != nil {
			return fmt.Errorf("migrating classifier %s: %w", classifier.Name, err)
		}

		log.Info("migrated successfully", "classifier", classifier.Name)
	}

	log.Info("migration complete")
	return nil
}

// migrateOneClassifier migrates one Classifier instance and clears the deprecated arrays.
func migrateOneClassifier(ctx context.Context, c client.Client,
	classifier *libsveltosv1beta1.Classifier, log logr.Logger) error {

	type clusterData struct {
		ref              corev1.ObjectReference
		hash             []byte
		deploymentStatus *libsveltosv1beta1.SveltosFeatureStatus
		failureMessage   *string
		managedLabels    []string
		unmanagedLabels  []libsveltosv1beta1.UnManagedLabel
	}

	byCluster := make(map[string]*clusterData)

	key := func(ref corev1.ObjectReference) string {
		return fmt.Sprintf("%s/%s/%s", ref.Kind, ref.Namespace, ref.Name)
	}

	for i := range classifier.Status.ClusterInfo { //nolint:staticcheck // deprecated, migration only
		ci := &classifier.Status.ClusterInfo[i] //nolint:staticcheck // deprecated, migration only
		d := &clusterData{ref: ci.Cluster}
		if len(ci.Hash) > 0 {
			d.hash = ci.Hash
		}
		s := ci.Status
		d.deploymentStatus = &s
		d.failureMessage = ci.FailureMessage
		byCluster[key(ci.Cluster)] = d
	}

	for i := range classifier.Status.MachingClusterStatuses { //nolint:staticcheck // deprecated, migration only
		ms := &classifier.Status.MachingClusterStatuses[i] //nolint:staticcheck // deprecated, migration only
		k := key(ms.ClusterRef)
		d, ok := byCluster[k]
		if !ok {
			d = &clusterData{ref: ms.ClusterRef}
			byCluster[k] = d
		}
		d.managedLabels = ms.ManagedLabels
		d.unmanagedLabels = ms.UnManagedLabels
	}

	for _, d := range byCluster {
		exists, err := migrationClusterExists(ctx, c, &d.ref)
		if err != nil {
			return fmt.Errorf("checking cluster %s/%s: %w", d.ref.Namespace, d.ref.Name, err)
		}
		if !exists {
			log.V(logs.LogDebug).Info("skipping stale cluster entry (cluster gone)",
				"classifier", classifier.Name,
				"cluster", d.ref.Name,
				"namespace", d.ref.Namespace)
			continue
		}
		if err := upsertMigrationClassifierReport(ctx, c, classifier.Name, &d.ref,
			d.hash, d.deploymentStatus, d.failureMessage,
			d.managedLabels, d.unmanagedLabels); err != nil {
			return fmt.Errorf("upserting ClassifierReport for cluster %s/%s: %w",
				d.ref.Namespace, d.ref.Name, err)
		}
	}

	patch := client.MergeFrom(classifier.DeepCopy())
	classifier.Status.ClusterInfo = nil            //nolint:staticcheck // deprecated, migration only
	classifier.Status.MachingClusterStatuses = nil //nolint:staticcheck // deprecated, migration only
	return c.Status().Patch(ctx, classifier, patch)
}

// upsertMigrationClassifierReport creates or updates the ClassifierReport for the given
// (classifier, cluster) pair with the migrated status data.
func upsertMigrationClassifierReport(ctx context.Context, c client.Client,
	classifierName string, cluster *corev1.ObjectReference,
	hash []byte, deploymentStatus *libsveltosv1beta1.SveltosFeatureStatus, failureMessage *string,
	managedLabels []string, unmanagedLabels []libsveltosv1beta1.UnManagedLabel) error {

	clusterType := migrationDeriveClusterType(cluster)
	reportName := libsveltosv1beta1.GetClassifierReportName(classifierName, cluster.Name, &clusterType)

	existing := &libsveltosv1beta1.ClassifierReport{}
	err := c.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: reportName}, existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting ClassifierReport: %w", err)
		}
		existing = &libsveltosv1beta1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Namespace,
				Name:      reportName,
				Labels: libsveltosv1beta1.GetClassifierReportLabels(
					classifierName, cluster.Name, &clusterType),
			},
			Spec: libsveltosv1beta1.ClassifierReportSpec{
				ClusterNamespace: cluster.Namespace,
				ClusterName:      cluster.Name,
				ClusterType:      clusterType,
				ClassifierName:   classifierName,
			},
		}
		if err := c.Create(ctx, existing); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating ClassifierReport: %w", err)
		}
		if err := c.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: reportName}, existing); err != nil {
			return fmt.Errorf("re-fetching ClassifierReport after create: %w", err)
		}
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status.Hash = hash
	existing.Status.DeploymentStatus = deploymentStatus
	existing.Status.FailureMessage = failureMessage
	existing.Status.ManagedLabels = managedLabels
	existing.Status.UnManagedLabels = unmanagedLabels
	return c.Status().Patch(ctx, existing, patch)
}

func migrationDeriveClusterType(ref *corev1.ObjectReference) libsveltosv1beta1.ClusterType {
	if strings.EqualFold(ref.Kind, "SveltosCluster") {
		return libsveltosv1beta1.ClusterTypeSveltos
	}
	return libsveltosv1beta1.ClusterTypeCapi
}

// migrationClusterExists reports whether the cluster a deprecated status entry refers to
// still exists. Checking the cluster itself (rather than its namespace) reuses RBAC the
// controller already has on sveltosclusters/clusters, instead of requiring a new grant on
// the cluster-scoped namespaces resource.
func migrationClusterExists(ctx context.Context, c client.Client, ref *corev1.ObjectReference) (bool, error) {
	key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}

	var obj client.Object
	if migrationDeriveClusterType(ref) == libsveltosv1beta1.ClusterTypeSveltos {
		obj = &libsveltosv1beta1.SveltosCluster{}
	} else {
		obj = &clusterv1.Cluster{}
	}

	if err := c.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
