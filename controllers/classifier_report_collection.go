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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/libsveltos/lib/sveltos_upgrade"
)

// Classifier instances reside in the same cluster as the sveltos-agent component.
// This function dynamically selects the appropriate Kubernetes client:
// - Management cluster's client if sveltos-agent is deployed there.
// - A managed cluster's client (obtained via clusterproxy) if sveltos-agent is in a managed cluster.
func getClassifierClient(ctx context.Context, clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType,
	logger logr.Logger) (client.Client, error) {

	if getAgentInMgmtCluster() {
		return getManagementClusterClient(), nil
	}

	// ResourceSummary is a Sveltos resource created in managed clusters.
	// Sveltos resources are always created using cluster-admin so that admin does not need to be
	// given such permissions.
	return clusterproxy.GetKubernetesClient(ctx, getManagementClusterClient(),
		clusterNamespace, clusterName, "", "", clusterType, logger)
}

// removeAccessRequest removes AccessRequest generated for SveltosAgent
func removeAccessRequest(ctx context.Context, c client.Client, logger logr.Logger) error {
	accessRequestList := &libsveltosv1beta1.AccessRequestList{}

	listOptions := []client.ListOption{
		client.MatchingLabels{
			accessRequestClassifierLabel: "ok",
		},
	}

	err := c.List(ctx, accessRequestList, listOptions...)
	if err != nil {
		return err
	}

	for i := range accessRequestList.Items {
		ar := &accessRequestList.Items[i]
		err = c.Delete(ctx, ar)
		if err != nil {
			return err
		}
	}

	logger.V(logs.LogDebug).Info("remove AccessRequest for SveltosAgents")
	return nil
}

// removeClassifierReports deletes all ClassifierReport corresponding to Classifier instance
func removeClassifierReports(ctx context.Context, c client.Client, classifier *libsveltosv1beta1.Classifier,
	logger logr.Logger) error {

	listOptions := []client.ListOption{
		client.MatchingLabels{
			libsveltosv1beta1.ClassifierlNameLabel: classifier.Name,
		},
	}

	classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
	err := c.List(ctx, classifierReportList, listOptions...)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to list ClassifierReports. Err: %v", err))
		return err
	}

	for i := range classifierReportList.Items {
		cr := &classifierReportList.Items[i]
		err = c.Delete(ctx, cr)
		if err != nil {
			return err
		}
	}

	return nil
}

// removeClusterClassifierReports deletes all ClassifierReport corresponding to Cluster instance
func removeClusterClassifierReports(ctx context.Context, c client.Client, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType, logger logr.Logger) error {

	listOptions := []client.ListOption{
		client.MatchingLabels{
			libsveltosv1beta1.ClassifierReportClusterNameLabel: clusterName,
			libsveltosv1beta1.ClassifierReportClusterTypeLabel: strings.ToLower(string(clusterType)),
		},
	}

	classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
	err := c.List(ctx, classifierReportList, listOptions...)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to list ClassifierReports. Err: %v", err))
		return err
	}

	for i := range classifierReportList.Items {
		cr := &classifierReportList.Items[i]
		err = c.Delete(ctx, cr)
		if err != nil {
			return err
		}
	}

	return nil
}

// Periodically collects ClassifierReports from each cluster.
// If sharding is used, it will collect only from clusters matching shard.
func collectClassifierReports(c client.Client, shardKey, capiOnboardAnnotation, version string, logger logr.Logger) {
	interval := 10 * time.Second
	if shardKey != "" {
		// This controller will only fetch ClassifierReport instances
		// so it can be more aggressive
		interval = 5 * time.Second
	}

	ctx := context.TODO()
	for {
		logger.V(logs.LogDebug).Info("collecting ClassifierReports")
		// Get a selectors that matches everything
		clusterList, err := clusterproxy.GetListOfClustersForShardKey(ctx, c, "", capiOnboardAnnotation, shardKey, logger)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get clusters: %v", err))
		}

		for i := range clusterList {
			cluster := &clusterList[i]
			err = collectClassifierReportsFromCluster(ctx, c, cluster, version, logger)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to collect ClassifierReports from cluster: %s/%s %v",
					cluster.Namespace, cluster.Name, err))
			}
		}

		time.Sleep(interval)
	}
}

func collectClassifierReportsFromCluster(ctx context.Context, c client.Client,
	cluster *corev1.ObjectReference, version string, logger logr.Logger) error {

	isPullMode, err := clusterproxy.IsClusterInPullMode(ctx, c, cluster.Namespace, cluster.Name,
		clusterproxy.GetClusterType(cluster), logger)
	if err != nil {
		return err
	}

	if isPullMode {
		return nil
	}

	logger = logger.WithValues("cluster", fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Name))
	ready, err := clusterproxy.IsClusterReadyToBeConfigured(ctx, c, cluster, logger)
	if err != nil {
		logger.V(logs.LogDebug).Info("cluster is not ready yet")
		return err
	}

	if !ready {
		return nil
	}

	if !sveltos_upgrade.IsSveltosAgentVersionCompatible(ctx, getManagementClusterClient(), version, cluster.Namespace,
		cluster.Name, clusterproxy.GetClusterType(cluster), getAgentInMgmtCluster(), logger) {

		msg := "compatibility checks failed"
		logger.V(logs.LogDebug).Info(msg)
		return errors.New(msg)
	}

	// Classifier instance location depends on sveltos-agent: management cluster if it's running there,
	// otherwise managed cluster.
	clusterClient, err := getClassifierClient(ctx, cluster.Namespace, cluster.Name,
		clusterproxy.GetClusterType(cluster), logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("collecting ClassifierReports from cluster")

	var listOptions []client.ListOption
	if getAgentInMgmtCluster() || isPullMode {
		clusterType := clusterproxy.GetClusterType(cluster)
		// If agent is in the management cluster or in pull mode, ClassifierReport for this
		// cluster are also in the management cluuster in the cluster namespace.
		listOptions = []client.ListOption{
			client.InNamespace(cluster.Namespace),
			client.MatchingLabels{
				libsveltosv1beta1.ClassifierReportClusterNameLabel: cluster.Name,
				libsveltosv1beta1.ClassifierReportClusterTypeLabel: strings.ToLower(string(clusterType)),
			},
		}
	}

	classifierReportList := libsveltosv1beta1.ClassifierReportList{}
	err = clusterClient.List(ctx, &classifierReportList, listOptions...)
	if err != nil {
		return err
	}

	for i := range classifierReportList.Items {
		cr := &classifierReportList.Items[i]
		if !cr.DeletionTimestamp.IsZero() {
			// ignore deleted ClassifierReport
			continue
		}
		if cr.Spec.ClusterName != "" {
			// if ClusterName is set, this is coming from a
			// managed cluster. If management cluster is in turn
			// managed by another cluster, do not pull those.
			continue
		}
		l := logger.WithValues("classifierReport", cr.Name)
		err = updateClassifierReport(ctx, c, cluster, cr, l)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to process ClassifierReport. Err: %v", err))
		}
	}

	return nil
}

func updateClassifierReport(ctx context.Context, c client.Client, cluster *corev1.ObjectReference,
	classiferReport *libsveltosv1beta1.ClassifierReport, logger logr.Logger) error {

	if classiferReport.Labels == nil {
		msg := "classifierReport is malformed. Labels is empty"
		logger.V(logs.LogInfo).Info(msg)
		return errors.New(msg)
	}

	classifierName, ok := classiferReport.Labels[libsveltosv1beta1.ClassifierlNameLabel]
	if !ok {
		msg := "classifierReport is malformed. Label missing"
		logger.V(logs.LogInfo).Info(msg)
		return errors.New(msg)
	}

	// Verify Classifier still exists
	currentClassifier := libsveltosv1beta1.Classifier{}
	err := c.Get(ctx, types.NamespacedName{Name: classifierName}, &currentClassifier)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
	}
	if !currentClassifier.DeletionTimestamp.IsZero() {
		return nil
	}

	clusterType := clusterproxy.GetClusterType(cluster)
	classifierReportName := libsveltosv1beta1.GetClassifierReportName(classifierName, cluster.Name, &clusterType)

	currentClassifierReport := &libsveltosv1beta1.ClassifierReport{}
	err = c.Get(ctx,
		types.NamespacedName{Namespace: cluster.Namespace, Name: classifierReportName},
		currentClassifierReport)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(logs.LogDebug).Info("create ClassifierReport in management cluster")
			currentClassifierReport.Namespace = cluster.Namespace
			currentClassifierReport.Name = classifierReportName
			currentClassifierReport.Labels = libsveltosv1beta1.GetClassifierReportLabels(
				classifierName, cluster.Name, &clusterType)
			currentClassifierReport.Spec = classiferReport.Spec
			currentClassifierReport.Spec.ClusterNamespace = cluster.Namespace
			currentClassifierReport.Spec.ClusterName = cluster.Name
			currentClassifierReport.Spec.ClusterType = clusterType
			return c.Create(ctx, currentClassifierReport)
		}
		return err
	}

	logger.V(logs.LogDebug).Info("update ClassifierReport in management cluster")
	currentClassifierReport.Spec = classiferReport.Spec
	currentClassifierReport.Spec.ClusterNamespace = cluster.Namespace
	currentClassifierReport.Spec.ClusterName = cluster.Name
	currentClassifierReport.Spec.ClusterType = clusterType
	currentClassifierReport.Labels = libsveltosv1beta1.GetClassifierReportLabels(
		classifierName, cluster.Name, &clusterType)
	return c.Update(ctx, currentClassifierReport)
}
