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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	managementClusterConfig *rest.Config
	managementClusterClient client.Client
	// managementClusterAPIReader reads from the API server, never from a cache
	managementClusterAPIReader client.Reader
	sveltosAgentConfigMap      string
	sveltosApplierConfigMap    string
	registry                   string
	agentInMgmtCluster         bool
	agentClustersPerInstance   int
	enableNatsWatcher          bool
	sveltosNamespace           string
)

func SetManagementClusterAccess(config *rest.Config, c client.Client) {
	managementClusterConfig = config
	managementClusterClient = c
}

// SetManagementClusterAPIReader sets the reader, not served from a cache, used to decide in which
// sveltos-agent group a cluster is placed. Without it the management cluster client is used.
func SetManagementClusterAPIReader(reader client.Reader) {
	managementClusterAPIReader = reader
	resetSveltosAgentAssigner()
}

func SetSveltosAgentConfigMap(name string) {
	sveltosAgentConfigMap = name
}

func SetSveltosApplierConfigMap(name string) {
	sveltosApplierConfigMap = name
}

func SetSveltosAgentRegistry(reg string) {
	registry = reg
}

func SetAgentInMgmtCluster(isInMgmtCluster bool) {
	agentInMgmtCluster = isInMgmtCluster
}

// SetAgentClustersPerInstance sets how many clusters one sveltos-agent serves in agentless mode.
// A value lower than 2 keeps one instance per cluster.
func SetAgentClustersPerInstance(clustersPerInstance int) {
	agentClustersPerInstance = clustersPerInstance
}

func SetSveltosAgentEnableNATS(enable bool) {
	enableNatsWatcher = enable
}

func SetSveltosNamespace(ns string) {
	sveltosNamespace = ns
}

func getManagementClusterConfig() *rest.Config {
	return managementClusterConfig
}

func getManagementClusterClient() client.Client {
	return managementClusterClient
}

func getManagementClusterScheme() *runtime.Scheme {
	return managementClusterClient.Scheme()
}

func getSveltosAgentConfigMap() string {
	return sveltosAgentConfigMap
}

func getSveltosApplierConfigMap() string {
	return sveltosApplierConfigMap
}

func getSveltosAgentRegistry() string {
	return registry
}

func getAgentInMgmtCluster() bool {
	return agentInMgmtCluster
}

func getAgentClustersPerInstance() int {
	return agentClustersPerInstance
}

func getSveltosAgentEnableNATS() bool {
	return enableNatsWatcher
}

func getSveltosNamespace() string {
	return sveltosNamespace
}
