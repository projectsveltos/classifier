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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	managementClusterConfig *rest.Config
	managementClusterClient client.Client
	sveltosAgentConfigMap   string
	registry                string
	agentInMgmtCluster      bool
)

func SetManagementClusterAccess(config *rest.Config, c client.Client) {
	managementClusterConfig = config
	managementClusterClient = c
}

func SetSveltosAgentConfigMap(name string) {
	sveltosAgentConfigMap = name
}

func SetSveltosAgentRegistry(reg string) {
	registry = reg
}

func SetAgentInMgmtCluster(isInMgmtCluster bool) {
	agentInMgmtCluster = isInMgmtCluster
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

func GetSveltosAgentRegistry() string {
	return registry
}

func getAgentInMgmtCluster() bool {
	return agentInMgmtCluster
}

func collectSveltosAgentConfigMap(ctx context.Context, name string) (*corev1.ConfigMap, error) {
	c := getManagementClusterClient()
	configMap := &corev1.ConfigMap{}

	err := c.Get(ctx, types.NamespacedName{Namespace: projectsveltos, Name: name}, configMap)
	if err != nil {
		return nil, err
	}

	return configMap, nil
}
