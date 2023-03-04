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

var (
	DeployClassifierCRD                 = deployClassifierCRD
	DeployClassifierReportCRD           = deployClassifierReportCRD
	DeployDebuggingConfigurationCRD     = deployDebuggingConfigurationCRD
	DeployClassifierInstance            = deployClassifierInstance
	DeployClassifierAgent               = deployClassifierAgent
	ClassifierHash                      = classifierHash
	DeployClassifierInCluster           = deployClassifierInCluster
	UndeployClassifierFromCluster       = undeployClassifierFromCluster
	RemoveClassifierReports             = removeClassifierReports
	RemoveClusterClassifierReports      = removeClusterClassifierReports
	CollectClassifierReportsFromCluster = collectClassifierReportsFromCluster

	CreateAccessRequest                        = createAccessRequest
	GetAccessRequestName                       = getAccessRequestName
	GetKubeconfigFromAccessRequest             = getKubeconfigFromAccessRequest
	UpdateSecretWithAccessManagementKubeconfig = updateSecretWithAccessManagementKubeconfig

	GetHandlersForFeature = getHandlersForFeature

	ProcessClassifier                      = (*ClassifierReconciler).processClassifier
	RemoveClassifier                       = (*ClassifierReconciler).removeClassifier
	RequeueClassifierForCluster            = (*ClassifierReconciler).requeueClassifierForCluster
	RequeueClassifierForMachine            = (*ClassifierReconciler).requeueClassifierForMachine
	RequeueClassifierForClassifierReport   = (*ClassifierReconciler).requeueClassifierForClassifierReport
	RequeueClassifierForClassifier         = (*ClassifierReconciler).requeueClassifierForClassifier
	UpdateMatchingClustersAndRegistrations = (*ClassifierReconciler).updateMatchingClustersAndRegistrations
	UpdateLabelsOnMatchingClusters         = (*ClassifierReconciler).updateLabelsOnMatchingClusters
	HandleLabelRegistrations               = (*ClassifierReconciler).handleLabelRegistrations
	UndeployClassifier                     = (*ClassifierReconciler).undeployClassifier
	RemoveAllRegistrations                 = (*ClassifierReconciler).removeAllRegistrations
	ClassifyLabels                         = (*ClassifierReconciler).classifyLabels
)

var (
	CreatFeatureHandlerMaps = creatFeatureHandlerMaps
)

const (
	Controlplaneendpoint = controlplaneendpoint
)
