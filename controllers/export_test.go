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
	corev1 "k8s.io/api/core/v1"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

// GroupClassifierReportsByCluster is the test-facing wrapper around groupClassifierReportsByCluster.
// It accepts a plain cluster list (instead of the internal shard map) and returns a map from
// cluster ref to its reports so tests don't need to know about internal types.
func GroupClassifierReportsByCluster(
	reports []libsveltosv1beta1.ClassifierReport,
	clusterList []corev1.ObjectReference,
) map[corev1.ObjectReference][]*libsveltosv1beta1.ClassifierReport {

	groups := groupClassifierReportsByCluster(reports, buildShardClustersMap(clusterList))
	result := make(map[corev1.ObjectReference][]*libsveltosv1beta1.ClassifierReport, len(groups))
	for _, g := range groups {
		result[g.ref] = g.items
	}
	return result
}

var (
	DeployClassifierCRD                      = deployClassifierCRD
	DeployClassifierReportCRD                = deployClassifierReportCRD
	DeployHealthCheckCRD                     = deployHealthCheckCRD
	DeployHealthCheckReportCRD               = deployHealthCheckReportCRD
	DeployEventSourceCRD                     = deployEventSourceCRD
	DeployEventReportCRD                     = deployEventReportCRD
	DeployReloaderCRD                        = deployReloaderCRD
	DeployReloaderReportCRD                  = deployReloaderReportCRD
	DeployDebuggingConfigurationCRD          = deployDebuggingConfigurationCRD
	DeployClassifierInstance                 = deployClassifierInstance
	DeploySveltosAgentInManagedCluster       = deploySveltosAgentInManagedCluster
	ClassifierHash                           = classifierHash
	DeployClassifierInCluster                = deployClassifierInCluster
	UndeployClassifierFromCluster            = undeployClassifierFromCluster
	RemoveClassifierReports                  = removeClassifierReports
	RemoveClusterClassifierReports           = removeClusterClassifierReports
	PruneClassifierReportsForDeletedClusters = pruneClassifierReportsForDeletedClusters
	CollectClassifierReportsFromCluster      = collectClassifierReportsFromCluster
	DeploySveltosAgentInManagementCluster    = deploySveltosAgentInManagementCluster
	RemoveSveltosAgentFromManagementCluster  = removeSveltosAgentFromManagementCluster
	GetSveltosAgentLabels                    = getSveltosAgentLabels
	GetSveltosAgentNamespace                 = getSveltosAgentNamespace
	GetSveltosAgentPatches                   = getSveltosAgentPatches
	GetSveltosApplierPatches                 = getSveltosApplierPatches

	CreateAccessRequest                        = createAccessRequest
	GetAccessRequestName                       = getAccessRequestName
	GetKubeconfigFromAccessRequest             = getKubeconfigFromAccessRequest
	UpdateSecretWithAccessManagementKubeconfig = updateSecretWithAccessManagementKubeconfig

	GetHandlersForFeature = getHandlersForFeature

	ProcessClassifier                      = (*ClassifierReconciler).processClassifier
	RemoveClassifier                       = (*ClassifierReconciler).removeClassifier
	RequeueClassifierForCluster            = (*ClassifierReconciler).requeueClassifierForCluster
	RequeueClassifierForClassifierReport   = (*ClassifierReconciler).requeueClassifierForClassifierReport
	UpdateMatchingClustersAndRegistrations = (*ClassifierReconciler).updateMatchingClustersAndRegistrations
	UpdateLabelsOnMatchingClusters         = (*ClassifierReconciler).updateLabelsOnMatchingClusters
	RegisterMatchingClusters               = (*ClassifierReconciler).registerMatchingClusters
	UndeployClassifier                     = (*ClassifierReconciler).undeployClassifier
	RemoveAllRegistrations                 = (*ClassifierReconciler).removeAllRegistrations
	ClassifyLabels                         = (*ClassifierReconciler).classifyLabels
	CleanUpNonMatchingClusters             = (*ClassifierReconciler).cleanUpNonMatchingClusters
)

var (
	CreatFeatureHandlerMaps = creatFeatureHandlerMaps
)

const (
	Controlplaneendpoint = controlplaneendpoint
)

// ManagementClusterClassifier exports for unit tests.
var (
	DoesMatchLabelFilters      = doesMatchLabelFilters
	RunClassificationLua       = runClassificationLua
	ClusterTypeFromKind        = clusterTypeFromKind
	MgmtClassifierAsClassifier = mgmtClassifierAsClassifier
	EnsureMgmtClassifierReport = ensureMgmtClassifierReport
	ClassifierLabelKeys        = classifierLabelKeys
	FetchResourcesForSelector  = fetchResourcesForSelector
	ListMgmtClassifierReports  = listMgmtClassifierReports
	GetMgmtClassifierReport    = getMgmtClassifierReport
	DeleteMgmtClassifierReport = deleteMgmtClassifierReport
	ApplyLabelsToCluster       = applyLabelsToCluster
	RemoveLabelsFromCluster    = removeLabelsFromCluster
)
