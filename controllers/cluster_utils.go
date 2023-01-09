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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/libsveltos/lib/clusterproxy"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

// getSveltosCluster returns SveltosCluster
func getSveltosCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string) (*libsveltosv1alpha1.SveltosCluster, error) {

	clusterNamespacedName := types.NamespacedName{
		Namespace: clusterNamespace,
		Name:      clusterName,
	}

	cluster := &libsveltosv1alpha1.SveltosCluster{}
	if err := c.Get(ctx, clusterNamespacedName, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// getCAPICluster returns CAPI Cluster
func getCAPICluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string) (*clusterv1.Cluster, error) {

	clusterNamespacedNamed := types.NamespacedName{
		Namespace: clusterNamespace,
		Name:      clusterName,
	}

	cluster := &clusterv1.Cluster{}
	if err := c.Get(ctx, clusterNamespacedNamed, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// getCluster returns the cluster associated to ClusterSummary.
// ClusterSummary can be created for either a CAPI Cluster or a Sveltos Cluster.
func getCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) (client.Object, error) {

	if clusterType == libsveltosv1alpha1.ClusterTypeSveltos {
		return getSveltosCluster(ctx, c, clusterNamespace, clusterName)
	}
	return getCAPICluster(ctx, c, clusterNamespace, clusterName)
}

// isCAPIClusterPaused returns true if CAPI Cluster is paused
func isCAPIClusterPaused(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string) (bool, error) {

	cluster, err := getCAPICluster(ctx, c, clusterNamespace, clusterName)
	if err != nil {
		return false, err
	}

	return cluster.Spec.Paused, nil
}

// isSveltosClusterPaused returns true if CAPI Cluster is paused
func isSveltosClusterPaused(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string) (bool, error) {

	cluster, err := getSveltosCluster(ctx, c, clusterNamespace, clusterName)
	if err != nil {
		return false, err
	}

	return cluster.Spec.Paused, nil
}

func isClusterPaused(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) (bool, error) {

	if clusterType == libsveltosv1alpha1.ClusterTypeSveltos {
		return isSveltosClusterPaused(ctx, c, clusterNamespace, clusterName)
	}
	return isCAPIClusterPaused(ctx, c, clusterNamespace, clusterName)
}

func getKubernetesRestConfig(ctx context.Context, c client.Client, clusterNamespace, clusterName string,
	clusterType libsveltosv1alpha1.ClusterType, logger logr.Logger) (*rest.Config, error) {

	if clusterType == libsveltosv1alpha1.ClusterTypeSveltos {
		return clusterproxy.GetSveltosKubernetesRestConfig(ctx, logger, c, clusterNamespace, clusterName)
	}
	return clusterproxy.GetCAPIKubernetesRestConfig(ctx, logger, c, clusterNamespace, clusterName)
}

func getKubernetesClient(ctx context.Context, c client.Client, s *runtime.Scheme,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType, logger logr.Logger) (client.Client, error) {

	if clusterType == libsveltosv1alpha1.ClusterTypeSveltos {
		return clusterproxy.GetSveltosKubernetesClient(ctx, logger, c, s, clusterNamespace, clusterName)
	}
	return clusterproxy.GetCAPIKubernetesClient(ctx, logger, c, s, clusterNamespace, clusterName)
}

func getClusterType(cluster *corev1.ObjectReference) libsveltosv1alpha1.ClusterType {
	// TODO: remove this
	if cluster.APIVersion != libsveltosv1alpha1.GroupVersion.String() &&
		cluster.APIVersion != clusterv1.GroupVersion.String() {

		panic(1)
	}

	clusterType := libsveltosv1alpha1.ClusterTypeCapi
	if cluster.APIVersion == libsveltosv1alpha1.GroupVersion.String() {
		clusterType = libsveltosv1alpha1.ClusterTypeSveltos
	}
	return clusterType
}

// getListOfCAPIClusters returns all CAPI Clusters where Classifier needs to be deployed.
// Currently a Classifier instance needs to be deployed in every existing CAPI cluster.
func getListOfCAPICluster(ctx context.Context, c client.Client, logger logr.Logger,
) ([]corev1.ObjectReference, error) {

	clusterList := &clusterv1.ClusterList{}
	if err := c.List(ctx, clusterList); err != nil {
		logger.Error(err, "failed to list all Cluster")
		return nil, err
	}

	clusters := make([]corev1.ObjectReference, 0)

	for i := range clusterList.Items {
		cluster := &clusterList.Items[i]

		if !cluster.DeletionTimestamp.IsZero() {
			// Only existing cluster can match
			continue
		}

		addTypeInformationToObject(c.Scheme(), cluster)

		clusters = append(clusters, corev1.ObjectReference{
			Namespace:  cluster.Namespace,
			Name:       cluster.Name,
			APIVersion: cluster.APIVersion,
			Kind:       cluster.Kind,
		})
	}

	return clusters, nil
}

// getListOfSveltosClusters returns all Sveltos Clusters where Classifier needs to be deployed.
// Currently a Classifier instance needs to be deployed in every existing sveltosCluster.
func getListOfSveltosCluster(ctx context.Context, c client.Client, logger logr.Logger,
) ([]corev1.ObjectReference, error) {

	clusterList := &libsveltosv1alpha1.SveltosClusterList{}
	if err := c.List(ctx, clusterList); err != nil {
		logger.Error(err, "failed to list all Cluster")
		return nil, err
	}

	clusters := make([]corev1.ObjectReference, 0)

	for i := range clusterList.Items {
		cluster := &clusterList.Items[i]

		if !cluster.DeletionTimestamp.IsZero() {
			// Only existing cluster can match
			continue
		}

		addTypeInformationToObject(c.Scheme(), cluster)

		clusters = append(clusters, corev1.ObjectReference{
			Namespace:  cluster.Namespace,
			Name:       cluster.Name,
			APIVersion: cluster.APIVersion,
			Kind:       cluster.Kind,
		})
	}

	return clusters, nil
}

// getListOfClusters returns all Sveltos/CAPI Clusters where Classifier needs to be deployed.
// Currently a Classifier instance needs to be deployed in every existing clusters.
func getListOfClusters(ctx context.Context, c client.Client, logger logr.Logger,
) ([]corev1.ObjectReference, error) {

	clusters, err := getListOfCAPICluster(ctx, c, logger)
	if err != nil {
		return nil, err
	}

	var tmpClusters []corev1.ObjectReference
	tmpClusters, err = getListOfSveltosCluster(ctx, c, logger)
	if err != nil {
		return nil, err
	}

	clusters = append(clusters, tmpClusters...)
	return clusters, nil
}

func getClusterRefFromClassifierReport(report *libsveltosv1alpha1.ClassifierReport) *corev1.ObjectReference {
	cluster := corev1.ObjectReference{
		Namespace: report.Spec.ClusterNamespace,
		Name:      report.Spec.ClusterName,
	}
	switch report.Spec.ClusterType {
	case libsveltosv1alpha1.ClusterTypeCapi:
		cluster.APIVersion = clusterv1.GroupVersion.String()
		cluster.Kind = "Cluster"
	case libsveltosv1alpha1.ClusterTypeSveltos:
		cluster.APIVersion = libsveltosv1alpha1.GroupVersion.String()
		cluster.Kind = libsveltosv1alpha1.SveltosClusterKind
	default:
		panic(1)
	}
	return &cluster
}
