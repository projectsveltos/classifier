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

package fv_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/agentgroup"
)

// This test needs the environment created by "make create-cluster-agentless-grouping": classifier
// in agentless mode with --agent-clusters-per-instance=2, and two kind clusters registered as
// SveltosClusters in the same namespace. It is skipped when classifier is not started
// with --agent-clusters-per-instance greater than one.
const (
	agentClustersPerInstanceArg = "--agent-clusters-per-instance="

	// The first cluster is kindWorkloadCluster (clusterapi-workload)
	groupingSecondClusterName       = "clusterapi-workload-2"
	groupingSecondClusterKubeconfig = "workload2_kubeconfig"

	sveltosAgentComponent  = "sveltos-agent"
	sveltosAgentNamePrefix = "sveltos-agent-"

	groupingNamespaceLabelKey = "grouping-test"

	kindNamespace = "Namespace"
)

var _ = Describe("Agentless sveltos-agent serving several clusters", Serial, func() {
	const namePrefix = "grouping-"

	It("Classifies each cluster of a group on its own", Label("AGENTLESS-GROUPING"), func() {
		if getAgentClustersPerInstance() < 2 {
			Skip(fmt.Sprintf("classifier is not started with %s greater than one", agentClustersPerInstanceArg))
		}

		firstCluster := types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: kindWorkloadCluster.GetName()}
		secondCluster := types.NamespacedName{Namespace: firstCluster.Namespace, Name: groupingSecondClusterName}

		firstClient, err := getClientFromKubeconfigFile("workload_kubeconfig")
		Expect(err).To(BeNil())
		secondClient, err := getClientFromKubeconfigFile(groupingSecondClusterKubeconfig)
		Expect(err).To(BeNil())

		// A cluster matches when it has a Namespace with this label value. The Namespace is created
		// in the managed clusters one at a time: only the cluster having it must be labeled.
		groupingValue := randomString()
		labelKey := randomString()
		labelValue := randomString()

		classifier := getGroupingClassifier(namePrefix, groupingValue, labelKey, labelValue)
		Byf("Creating classifier %s in the management cluster", classifier.Name)
		Expect(k8sClient.Create(context.TODO(), classifier)).To(Succeed())

		By("Both clusters are served by a single sveltos-agent Deployment")
		verifyGroupServesClusters(firstCluster.Namespace, firstCluster.Name, secondCluster.Name)

		By("Both clusters report the Classifier as not matching")
		verifyGroupingClassifierReport(classifier.Name, firstCluster, false)
		verifyGroupingClassifierReport(classifier.Name, secondCluster, false)

		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   namePrefix + randomString(),
			Labels: map[string]string{groupingNamespaceLabelKey: groupingValue},
		}}

		Byf("Creating Namespace %s in cluster %s only", namespace.Name, firstCluster.Name)
		Expect(firstClient.Create(context.TODO(), namespace.DeepCopy())).To(Succeed())

		verifyGroupingClassifierReport(classifier.Name, firstCluster, true)
		verifyGroupingClusterLabel(firstCluster, labelKey, labelValue, true)
		verifyGroupingClassifierReport(classifier.Name, secondCluster, false)
		verifyGroupingClusterLabel(secondCluster, labelKey, labelValue, false)

		Byf("Creating Namespace %s in cluster %s", namespace.Name, secondCluster.Name)
		Expect(secondClient.Create(context.TODO(), namespace.DeepCopy())).To(Succeed())

		verifyGroupingClassifierReport(classifier.Name, secondCluster, true)
		verifyGroupingClusterLabel(secondCluster, labelKey, labelValue, true)

		Byf("Deleting Namespace %s from cluster %s", namespace.Name, firstCluster.Name)
		Expect(firstClient.Delete(context.TODO(), namespace.DeepCopy())).To(Succeed())

		verifyGroupingClassifierReport(classifier.Name, firstCluster, false)
		verifyGroupingClusterLabel(firstCluster, labelKey, labelValue, false)
		verifyGroupingClusterLabel(secondCluster, labelKey, labelValue, true)

		By("Still a single sveltos-agent Deployment")
		verifyGroupServesClusters(firstCluster.Namespace, firstCluster.Name, secondCluster.Name)

		Byf("Deleting classifier %s", classifier.Name)
		Expect(k8sClient.Delete(context.TODO(), classifier)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, &libsveltosv1beta1.Classifier{})
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Expect(secondClient.Delete(context.TODO(), namespace.DeepCopy())).To(Succeed())
	})
})

// getGroupingClassifier returns a Classifier matching the clusters having a Namespace labeled
// groupingNamespaceLabelKey=groupingValue.
func getGroupingClassifier(namePrefix, groupingValue, labelKey, labelValue string) *libsveltosv1beta1.Classifier {
	return &libsveltosv1beta1.Classifier{
		ObjectMeta: metav1.ObjectMeta{Name: namePrefix + randomString()},
		Spec: libsveltosv1beta1.ClassifierSpec{
			ClassifierLabels: []libsveltosv1beta1.ClassifierLabel{{Key: labelKey, Value: labelValue}},
			DeployedResourceConstraint: &libsveltosv1beta1.DeployedResourceConstraint{
				ResourceSelectors: []libsveltosv1beta1.ResourceSelector{
					{
						Group:   "",
						Version: coreAPIVersion,
						Kind:    kindNamespace,
						LabelFilters: []libsveltosv1beta1.LabelFilter{
							{Key: groupingNamespaceLabelKey, Operation: libsveltosv1beta1.OperationEqual, Value: groupingValue},
						},
					},
				},
			},
		},
	}
}

// verifyGroupServesClusters verifies the clusters, which all live in clusterNamespace, are the members
// of one group, that the group Deployment is available, and that no other sveltos-agent Deployment exists.
func verifyGroupServesClusters(clusterNamespace string, clusterNames ...string) {
	Eventually(func() error {
		groups, err := agentgroup.ListGroups(context.TODO(), k8sClient, deplNamespace, sveltosAgentComponent, clusterNamespace)
		if err != nil {
			return err
		}
		if len(groups) != 1 {
			return fmt.Errorf("expected one group, found %d", len(groups))
		}

		members := make([]string, 0, len(groups[0].Members))
		for i := range groups[0].Members {
			members = append(members, groups[0].Members[i].Name)
		}
		for _, name := range clusterNames {
			if !contains(members, name) {
				return fmt.Errorf("cluster %s is not a member of group %s: %v", name, groups[0].Name, members)
			}
		}

		deployments := &appsv1.DeploymentList{}
		if err := k8sClient.List(context.TODO(), deployments, client.InNamespace(deplNamespace)); err != nil {
			return err
		}
		agents := []appsv1.Deployment{}
		for i := range deployments.Items {
			if strings.HasPrefix(deployments.Items[i].Name, sveltosAgentNamePrefix) {
				agents = append(agents, deployments.Items[i])
			}
		}
		if len(agents) != 1 || agents[0].Name != groups[0].Name {
			return fmt.Errorf("expected only the Deployment of group %s, found %d sveltos-agent Deployments",
				groups[0].Name, len(agents))
		}
		if agents[0].Status.AvailableReplicas != *agents[0].Spec.Replicas {
			return fmt.Errorf("deployment %s is not available", agents[0].Name)
		}
		return nil
	}, timeout, pollingInterval).Should(BeNil())
}

// verifyGroupingClassifierReport verifies the ClassifierReport of the cluster is provisioned and,
// according to isMatch, says the cluster matches the Classifier.
func verifyGroupingClassifierReport(classifierName string, cluster types.NamespacedName, isMatch bool) {
	clusterType := libsveltosv1beta1.ClusterTypeSveltos
	reportName := libsveltosv1beta1.GetClassifierReportName(classifierName, cluster.Name, &clusterType)
	Byf("Verifying ClassifierReport %s for cluster %s: match is %t", reportName, cluster.Name, isMatch)
	Eventually(func() bool {
		report := &libsveltosv1beta1.ClassifierReport{}
		err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: cluster.Namespace, Name: reportName}, report)
		if err != nil {
			return false
		}
		return report.Spec.Match == isMatch &&
			report.Status.DeploymentStatus != nil &&
			*report.Status.DeploymentStatus == libsveltosv1beta1.SveltosStatusProvisioned
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyGroupingClusterLabel verifies the SveltosCluster has (or has not) the label.
func verifyGroupingClusterLabel(cluster types.NamespacedName, key, value string, present bool) {
	Byf("Verifying cluster %s has label %s=%s: %t", cluster.Name, key, value, present)
	Eventually(func() bool {
		currentCluster := &libsveltosv1beta1.SveltosCluster{}
		if err := k8sClient.Get(context.TODO(), cluster, currentCluster); err != nil {
			return false
		}
		v, ok := currentCluster.Labels[key]
		if present {
			return ok && v == value
		}
		return !ok
	}, timeout, pollingInterval).Should(BeTrue())
}

// getClientFromKubeconfigFile returns a client for the cluster the file in this directory
// (created by the Makefile) gives access to.
func getClientFromKubeconfigFile(kubeconfigPath string) (client.Client, error) {
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	restConfig, err := clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	return client.New(restConfig, client.Options{Scheme: scheme})
}

// getAgentClustersPerInstance returns the value of --agent-clusters-per-instance classifier is
// started with. It returns 1 (grouping disabled) when the flag is not set.
func getAgentClustersPerInstance() int {
	depl := &appsv1.Deployment{}
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: deplNamespace, Name: deplName}, depl)).To(Succeed())

	for i := range depl.Spec.Template.Spec.Containers {
		for _, arg := range depl.Spec.Template.Spec.Containers[i].Args {
			if value, found := strings.CutPrefix(arg, agentClustersPerInstanceArg); found {
				n, err := strconv.Atoi(value)
				Expect(err).To(BeNil())
				return n
			}
		}
	}

	return 1
}

func contains(items []string, item string) bool {
	for i := range items {
		if items[i] == item {
			return true
		}
	}
	return false
}
