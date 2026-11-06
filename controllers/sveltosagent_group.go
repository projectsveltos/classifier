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

package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/classifier/controllers/keymanager"
	"github.com/projectsveltos/classifier/pkg/agent"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/agentgroup"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

const (
	// sveltosAgentComponent is the component name used for the sveltos-agent groups.
	sveltosAgentComponent = sveltosAgent

	// doNotSendReportsMode is the mode sveltos-agent is started with when it does not send
	// reports (the default one: classifier collects the ClassifierReports)
	doNotSendReportsMode = "do-not-send-reports"
)

var (
	// sveltosAgentAssigner remembers the groups, and serializes the decisions taken about them
	// (several Classifiers are reconciled at the same time, also for the same cluster).
	sveltosAgentAssigner      *agentgroup.Assigner
	sveltosAgentAssignerMutex sync.Mutex
)

// getSveltosAgentAssigner returns the Assigner deciding the sveltos-agent group of each cluster.
// The groups are remembered by one instance for the life of the process, which requires a reader
// not served from a cache (see SetManagementClusterAPIReader). Without one a new instance, that
// remembers nothing, is returned each time.
func getSveltosAgentAssigner(c client.Client) *agentgroup.Assigner {
	sveltosAgentAssignerMutex.Lock()
	defer sveltosAgentAssignerMutex.Unlock()

	if managementClusterAPIReader == nil {
		return agentgroup.NewAssigner(c, c, getSveltosNamespace())
	}

	if sveltosAgentAssigner == nil {
		sveltosAgentAssigner = agentgroup.NewAssigner(managementClusterAPIReader, c, getSveltosNamespace())
	}

	return sveltosAgentAssigner
}

func resetSveltosAgentAssigner() {
	sveltosAgentAssignerMutex.Lock()
	defer sveltosAgentAssignerMutex.Unlock()

	sveltosAgentAssigner = nil
}

// isSveltosAgentGroupingEnabled returns true when one sveltos-agent must serve several clusters
// (agentless mode only).
func isSveltosAgentGroupingEnabled() bool {
	return getAgentInMgmtCluster() && getAgentClustersPerInstance() > 1
}

// getSveltosAgentPool returns the pool of groups a cluster is placed in: clusters of one pool share
// the namespace, as a sveltos-agent serves one namespace only.
//
// There is no shard in the pool: only the Classifier deployment without a shard key reconciles
// Classifiers, so it is the only one deploying sveltos-agent, for the clusters of every shard.
func getSveltosAgentPool(clusterNamespace string) agentgroup.Pool {
	return agentgroup.Pool{
		Component:        sveltosAgentComponent,
		ClusterNamespace: clusterNamespace,
	}
}

// getSveltosAgentGroupLabels returns the labels set on the Deployment of a group: the ones
// identifying the pool, plus the feature label (which makes it show up with the per cluster
// instances when listing all sveltos-agent Deployments).
func getSveltosAgentGroupLabels(pool agentgroup.Pool) map[string]string {
	lbls := pool.Labels()
	lbls[sveltosAgentFeatureLabelKey] = sveltosAgent
	return lbls
}

// getSveltosAgentPatchesForDeployment returns the patches to apply to the sveltos-agent of the
// cluster: the ones configured for the cluster, falling back to the global ones. When sveltos-agent
// serves a group of clusters (grouped), only the global ones apply: a Deployment serves several
// clusters, so a patch asked by one of them cannot be applied to it.
func getSveltosAgentPatchesForDeployment(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType, grouped bool,
	logger logr.Logger) ([]libsveltosv1beta1.Patch, error) {

	if !grouped {
		return getSveltosAgentPatches(ctx, c, clusterNamespace, clusterName, clusterType, logger)
	}

	patches, err := getPerClusterPatches(ctx, c, sveltosAgentOverrideAnnotation, clusterNamespace,
		clusterName, clusterType, logger)
	if err != nil {
		return nil, err
	}
	if patches != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf(
			"%s is ignored: sveltos-agent serves a group of clusters, only global patches apply",
			sveltosAgentOverrideAnnotation))
	}

	return getGlobalSveltosAgentPatches(ctx, c, logger)
}

// deploySveltosAgentInGroup adds the cluster to a group of the pool, then deploys (or updates) the
// sveltos-agent Deployment serving the group.
func deploySveltosAgentInGroup(ctx context.Context, c client.Client, restConfig *rest.Config,
	clusterNamespace, clusterName, classifierName, mode string, clusterType libsveltosv1beta1.ClusterType,
	watchNamespaces []string, patches []libsveltosv1beta1.Patch, logger logr.Logger) error {

	pool := getSveltosAgentPool(clusterNamespace)
	member := agentgroup.Member{Name: clusterName, ClusterType: clusterType, WatchNamespaces: watchNamespaces}

	result, err := getSveltosAgentAssigner(c).AssignCluster(ctx, pool, member, getAgentClustersPerInstance())
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to assign cluster to a sveltos-agent group: %v", err))
		return err
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("cluster is served by sveltos-agent group %s", result.GroupName))

	// The cluster might have had its own sveltos-agent before grouping was enabled
	if err := removeSveltosAgentDeploymentOfCluster(ctx, restConfig, clusterNamespace, clusterName,
		clusterType, logger); err != nil {
		return err
	}

	return deploySveltosAgentGroupDeployment(ctx, restConfig, result.GroupName, clusterNamespace, classifierName,
		getSveltosAgentGroupLabels(pool), mode, patches, logger)
}

// deploySveltosAgentOfCluster deploys the sveltos-agent serving this cluster only (agentless mode,
// one cluster per instance).
//
// The cluster may still be listed in a group, because grouping was turned off or the number of
// clusters per instance lowered to one. The ConfigMaps of the groups tell where the cluster is
// served, not the setting: the cluster is taken out of its group first, so that no two
// sveltos-agents serve it. Does nothing when the cluster is in no group.
func deploySveltosAgentOfCluster(ctx context.Context, c client.Client, restConfig *rest.Config,
	clusterNamespace, clusterName, classifierName, mode string, clusterType libsveltosv1beta1.ClusterType,
	patches []libsveltosv1beta1.Patch, watchNamespaces []string, logger logr.Logger) error {

	err := removeClusterFromSveltosAgentGroups(ctx, c, restConfig, clusterNamespace, clusterName,
		clusterType, logger)
	if err != nil {
		return err
	}

	return deploySveltosAgentInManagementCluster(ctx, restConfig, c, clusterNamespace, clusterName,
		classifierName, mode, clusterType, patches, watchNamespaces, logger)
}

// deploySveltosAgentGroupDeployment applies the sveltos-agent Deployment of a group. It is the
// Deployment, not the cluster, that is named after the group, and that is told which ConfigMap
// lists the clusters it serves.
func deploySveltosAgentGroupDeployment(ctx context.Context, restConfig *rest.Config,
	groupName, clusterNamespace, classifierName string, lbls map[string]string, mode string,
	patches []libsveltosv1beta1.Patch, logger logr.Logger) error {

	agentYAML := string(agent.GetSveltosAgentInMgmtClusterYAML())
	agentYAML = prepareSveltosAgentGroupYAML(agentYAML, groupName, clusterNamespace, mode)
	agentYAML = strings.ReplaceAll(agentYAML, "$NAME", groupName)

	return deploySveltosAgentResources(ctx, clusterNamespace, "", classifierName, restConfig, agentYAML, lbls,
		patches, false, logger)
}

// prepareSveltosAgentGroupYAML sets the arguments of the sveltos-agent serving a group. The cluster
// (name, type, watch namespaces) is not an argument: each cluster served is listed in the group
// ConfigMap.
func prepareSveltosAgentGroupYAML(agentYAML, groupName, clusterNamespace, mode string) string {
	if mode != doNotSendReportsMode {
		agentYAML = strings.ReplaceAll(agentYAML, doNotSendReportsMode, "send-reports")
	}
	agentYAML = strings.ReplaceAll(agentYAML, "v=5", "v=0")

	if getSveltosAgentEnableNATS() {
		agentYAML = strings.ReplaceAll(agentYAML, "enable-nats-watcher=false", "enable-nats-watcher=true")
	}

	perClusterArgs := []string{"--cluster-name=", "--cluster-type=", "--watch-namespaces="}

	lines := strings.Split(agentYAML, "\n")
	result := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isOneOf(trimmed, perClusterArgs) {
			continue
		}

		if strings.HasPrefix(trimmed, "- --cluster-namespace=") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			result = append(result,
				fmt.Sprintf("%s- --cluster-namespace=%s", indent, clusterNamespace),
				fmt.Sprintf("%s- --group-config-map=%s", indent, groupName))
			continue
		}
		result = append(result, line)
	}

	agentYAML = strings.Join(result, "\n")

	registry := getSveltosAgentRegistry()
	if registry != "" {
		agentYAML = replaceRegistry(agentYAML, registry)
	}

	return agentYAML
}

// isOneOf returns true if the line is the argument "- <prefix>" for one of the prefixes (with
// an empty value, as in the template).
func isOneOf(trimmedLine string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if trimmedLine == "- "+prefix {
			return true
		}
	}
	return false
}

// removeSveltosAgentDeploymentOfCluster deletes the Deployment of the per cluster sveltos-agent, if
// there is one. Unlike removeSveltosAgentFromManagementCluster it leaves everything else (the
// version ConfigMap, which the group instance keeps up to date) alone.
func removeSveltosAgentDeploymentOfCluster(ctx context.Context, restConfig *rest.Config,
	clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType,
	logger logr.Logger) error {

	lbls := getSveltosAgentLabels(clusterNamespace, clusterName, clusterType)
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	namespace := getSveltosAgentNamespace(getSveltosNamespace())
	err = clientset.AppsV1().Deployments(namespace).DeleteCollection(ctx, metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: labels.Set(lbls).String()})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove per cluster sveltos-agent: %v", err))
		return err
	}

	// The name registered for the Deployment of the cluster is not needed anymore
	manager, err := keymanager.GetKeyManagerInstance(ctx, getManagementClusterClient())
	if err != nil {
		return err
	}
	manager.RemoveSveltosAgentDeploymentName(clusterNamespace, clusterName, clusterType)

	return nil
}

// removeClusterFromSveltosAgentGroups takes the cluster out of its sveltos-agent group, if it is in
// one. The group Deployment is deleted only when the cluster was its last member: as long as the
// group has a cluster, its sveltos-agent stays up.
func removeClusterFromSveltosAgentGroups(ctx context.Context, c client.Client, restConfig *rest.Config,
	clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType, logger logr.Logger) error {

	emptied, err := getSveltosAgentAssigner(c).RemoveCluster(ctx, sveltosAgentComponent,
		clusterNamespace, clusterName, clusterType)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove cluster from sveltos-agent group: %v", err))
		return err
	}

	if len(emptied) == 0 {
		return nil
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	namespace := getSveltosAgentNamespace(getSveltosNamespace())
	for _, groupName := range emptied {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("last cluster left sveltos-agent group %s: removing it", groupName))
		err = clientset.AppsV1().Deployments(namespace).Delete(ctx, groupName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// isSveltosAgentGroupDeployment returns true if the Deployment serves a group of clusters.
func isSveltosAgentGroupDeployment(deployment *appsv1.Deployment) bool {
	return deployment.GetLabels()[agentgroup.ComponentLabel] == sveltosAgentComponent
}

// removeStaleGroupMembers removes from the group served by the Deployment the clusters that do not
// exist anymore. It is the equivalent, for a group, of what removeStaleClassifierResources does for
// the Deployment of a single cluster. The Deployment itself is deleted only when the last member
// leaves the group. A failure leaves the cluster in the group: it is retried at the next run.
func removeStaleGroupMembers(ctx context.Context, c client.Client, deployment *appsv1.Deployment,
	logger logr.Logger) {

	clusterNamespace := deployment.GetLabels()[agentgroup.ClusterNamespaceLabel]
	groups, err := agentgroup.ListGroups(ctx, c, getSveltosNamespace(), sveltosAgentComponent, clusterNamespace)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to list sveltos-agent groups: %v", err))
		return
	}

	for i := range groups {
		if groups[i].Name != deployment.Name {
			continue
		}
		for _, member := range groups[i].Members {
			if !isGroupMemberStale(ctx, c, groups[i].ClusterNamespace, member, logger) {
				continue
			}
			removeStaleGroupMember(ctx, c, groups[i].ClusterNamespace, member, logger)
		}
	}
}

// isGroupMemberStale returns true only when the cluster is known not to exist. Any other error
// (cluster API not installed, API server unreachable) keeps the cluster in the group.
func isGroupMemberStale(ctx context.Context, c client.Client, clusterNamespace string,
	member agentgroup.Member, logger logr.Logger) bool {

	_, err := clusterproxy.GetCluster(ctx, c, clusterNamespace, member.Name, member.ClusterType)
	if err == nil {
		return false
	}
	if apierrors.IsNotFound(err) {
		return true
	}
	if !meta.IsNoMatchError(err) {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get cluster %s:%s/%s: %v",
			member.ClusterType, clusterNamespace, member.Name, err))
	}
	return false
}

// removeStaleGroupMember cleans up after a cluster that does not exist anymore. This is what is
// done for a cluster deleted while classifier is running: its ClassifierReports are removed, then
// the cluster leaves the group (removeSveltosAgentFromManagementCluster), which also deletes the
// version ConfigMap. A failure leaves the cluster in the group: it is retried at the next run.
func removeStaleGroupMember(ctx context.Context, c client.Client, clusterNamespace string,
	member agentgroup.Member, logger logr.Logger) {

	logger.V(logs.LogInfo).Info(fmt.Sprintf("removing stale cluster %s:%s/%s from its sveltos-agent group",
		member.ClusterType, clusterNamespace, member.Name))

	_, err := cleanClusterStaleResources(ctx, c, clusterNamespace, member.Name, member.ClusterType, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove stale cluster from sveltos-agent group: %v", err))
	}
}
