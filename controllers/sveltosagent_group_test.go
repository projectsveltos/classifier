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

package controllers_test

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/classifier/controllers"
	"github.com/projectsveltos/classifier/controllers/keymanager"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/agentgroup"
)

const (
	groupSveltosAgentComponent = "sveltos-agent"
	perClusterPatchAnnotation  = "sveltosagent.projectsveltos.io/config-override-ref"
	groupCapacity              = 2
)

var _ = Describe("sveltos-agent serving a group of clusters", func() {
	var logger logr.Logger
	var clusterNamespace string

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig())
		clusterNamespace = randomString()

		_, err := keymanager.GetKeyManagerInstance(context.TODO(), testEnv.Client)
		Expect(err).To(BeNil())

		controllers.SetAgentInMgmtCluster(true)
		controllers.SetAgentClustersPerInstance(groupCapacity)
	})

	AfterEach(func() {
		controllers.SetAgentClustersPerInstance(0)
		controllers.SetAgentInMgmtCluster(false)
	})

	// getDeployment returns the Deployment with the given name, if any.
	getDeployment := func(name string) (*appsv1.Deployment, error) {
		deployment := &appsv1.Deployment{}
		err := testEnv.Get(context.TODO(),
			types.NamespacedName{Namespace: controllers.GetSveltosAgentNamespace(sveltosNamespace), Name: name},
			deployment)
		return deployment, err
	}

	deployInGroup := func(clusterName string) {
		Expect(controllers.DeploySveltosAgentInGroup(context.TODO(), testEnv.Client, testEnv.Config,
			clusterNamespace, clusterName, randomString(), "do-not-send-reports",
			libsveltosv1beta1.ClusterTypeSveltos, nil, nil, logger)).To(Succeed())
	}

	removeFromGroup := func(clusterName string) {
		Expect(controllers.RemoveClusterFromSveltosAgentGroups(context.TODO(), testEnv.Client, testEnv.Config,
			clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeSveltos, logger)).To(Succeed())
	}

	groupsOf := func() []agentgroup.Group {
		groups, err := agentgroup.ListGroups(context.TODO(), testEnv.Client, sveltosNamespace,
			groupSveltosAgentComponent, clusterNamespace)
		Expect(err).To(BeNil())
		return groups
	}

	It("grouping is enabled only in agentless mode with more than one cluster per instance", func() {
		Expect(controllers.IsSveltosAgentGroupingEnabled()).To(BeTrue())

		controllers.SetAgentClustersPerInstance(1)
		Expect(controllers.IsSveltosAgentGroupingEnabled()).To(BeFalse())

		controllers.SetAgentClustersPerInstance(groupCapacity)
		controllers.SetAgentInMgmtCluster(false)
		Expect(controllers.IsSveltosAgentGroupingEnabled()).To(BeFalse())
	})

	It("prepareSveltosAgentGroupYAML replaces the per cluster arguments with the group ones", func() {
		template := `        - --cluster-namespace=
        - --cluster-name=
        - --cluster-type=
        - --watch-namespaces=
        - --run-mode=do-not-send-reports
        - --v=5`
		groupName := randomString()

		result := controllers.PrepareSveltosAgentGroupYAML(template, groupName, clusterNamespace, "do-not-send-reports")

		Expect(result).To(ContainSubstring("- --cluster-namespace=" + clusterNamespace))
		Expect(result).To(ContainSubstring("- --group-config-map=" + groupName))
		Expect(result).ToNot(ContainSubstring("--cluster-name="))
		Expect(result).ToNot(ContainSubstring("--cluster-type="))
		Expect(result).ToNot(ContainSubstring("--watch-namespaces="))
		Expect(result).To(ContainSubstring("--run-mode=do-not-send-reports"))
		Expect(result).To(ContainSubstring("--v=0"))
	})

	It("clusters of a namespace share a Deployment up to the capacity, then a new one is created", func() {
		clusterNames := []string{randomString(), randomString(), randomString()}
		for _, clusterName := range clusterNames {
			deployInGroup(clusterName)
		}

		groups := groupsOf()
		Expect(groups).To(HaveLen(2))

		sizes := []int{len(groups[0].Members), len(groups[1].Members)}
		Expect(sizes).To(ConsistOf(groupCapacity, 1))

		for i := range groups {
			deployment, err := getDeployment(groups[i].Name)
			Expect(err).To(BeNil())
			Expect(controllers.IsSveltosAgentGroupDeployment(deployment)).To(BeTrue())

			args := deployment.Spec.Template.Spec.Containers[0].Args
			Expect(args).To(ContainElement("--group-config-map=" + groups[i].Name))
			Expect(args).To(ContainElement("--cluster-namespace=" + clusterNamespace))
		}

		for _, clusterName := range clusterNames {
			removeFromGroup(clusterName)
		}
	})

	It("deploying a cluster again does not create another Deployment", func() {
		clusterName := randomString()
		deployInGroup(clusterName)
		deployInGroup(clusterName)

		groups := groupsOf()
		Expect(groups).To(HaveLen(1))
		Expect(groups[0].Members).To(HaveLen(1))

		removeFromGroup(clusterName)
	})

	It("keeps the Deployment while the group has a cluster, removes it with the last one", func() {
		first := randomString()
		second := randomString()
		deployInGroup(first)
		deployInGroup(second)

		groups := groupsOf()
		Expect(groups).To(HaveLen(1))
		groupName := groups[0].Name

		removeFromGroup(first)
		_, err := getDeployment(groupName)
		Expect(err).To(BeNil())

		removeFromGroup(second)
		Eventually(func() bool {
			_, err := getDeployment(groupName)
			return apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())
		Expect(groupsOf()).To(BeEmpty())
	})

	It("removing a cluster that is in no group is a no-op", func() {
		removeFromGroup(randomString())
		Expect(groupsOf()).To(BeEmpty())
	})

	It("removes the Deployment the cluster had of its own before grouping was enabled", func() {
		clusterName := randomString()
		clusterType := libsveltosv1beta1.ClusterTypeSveltos

		Expect(controllers.DeploySveltosAgentInManagementCluster(context.TODO(), testEnv.Config, testEnv.Client,
			clusterNamespace, clusterName, randomString(), "do-not-send-reports", clusterType, nil, nil,
			logger)).To(Succeed())

		lbls := controllers.GetSveltosAgentLabels(clusterNamespace, clusterName, clusterType)
		perClusterDeployments := func() int {
			deployments := &appsv1.DeploymentList{}
			Expect(testEnv.List(context.TODO(), deployments,
				client.InNamespace(controllers.GetSveltosAgentNamespace(sveltosNamespace)),
				client.MatchingLabels(lbls))).To(Succeed())
			return len(deployments.Items)
		}
		Eventually(perClusterDeployments, timeout, pollingInterval).Should(Equal(1))

		deployInGroup(clusterName)

		Eventually(perClusterDeployments, timeout, pollingInterval).Should(BeZero())
		Expect(groupsOf()).To(HaveLen(1))

		removeFromGroup(clusterName)
	})

	It("a cluster still in a group when grouping is turned off is served by its own Deployment only", func() {
		clusterName := randomString()
		clusterType := libsveltosv1beta1.ClusterTypeSveltos
		other := randomString()
		deployInGroup(clusterName)
		deployInGroup(other)

		groups := groupsOf()
		Expect(groups).To(HaveLen(1))
		groupName := groups[0].Name

		controllers.SetAgentClustersPerInstance(1)
		Expect(controllers.DeploySveltosAgentOfCluster(context.TODO(), testEnv.Client, testEnv.Config,
			clusterNamespace, clusterName, randomString(), "do-not-send-reports", clusterType, nil, nil,
			logger)).To(Succeed())

		// The cluster left the group, the other cluster is still served by it
		groups = groupsOf()
		Expect(groups).To(HaveLen(1))
		Expect(groups[0].Name).To(Equal(groupName))
		Expect(groups[0].Members).To(HaveLen(1))
		Expect(groups[0].Members[0].Name).To(Equal(other))
		_, err := getDeployment(groupName)
		Expect(err).To(BeNil())

		lbls := controllers.GetSveltosAgentLabels(clusterNamespace, clusterName, clusterType)
		Eventually(func() int {
			deployments := &appsv1.DeploymentList{}
			Expect(testEnv.List(context.TODO(), deployments,
				client.InNamespace(controllers.GetSveltosAgentNamespace(sveltosNamespace)),
				client.MatchingLabels(lbls))).To(Succeed())
			return len(deployments.Items)
		}, timeout, pollingInterval).Should(Equal(1))

		removeFromGroup(other)
		Expect(controllers.RemoveSveltosAgentFromManagementCluster(context.TODO(), clusterNamespace, clusterName,
			clusterType, logger)).To(Succeed())
	})

	It("removeSveltosAgentFromManagementCluster takes the cluster out of its group", func() {
		clusterName := randomString()
		deployInGroup(clusterName)
		groups := groupsOf()
		Expect(groups).To(HaveLen(1))
		groupName := groups[0].Name

		Expect(controllers.RemoveSveltosAgentFromManagementCluster(context.TODO(), clusterNamespace, clusterName,
			libsveltosv1beta1.ClusterTypeSveltos, logger)).To(Succeed())

		Expect(groupsOf()).To(BeEmpty())
		Eventually(func() bool {
			_, err := getDeployment(groupName)
			return apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())
	})

	Context("patches", func() {
		var clusterName string
		var configMap *corev1.ConfigMap

		BeforeEach(func() {
			clusterName = randomString()

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNamespace}}
			Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

			configMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: clusterNamespace, Name: randomString()},
				Data: map[string]string{
					randomString(): `- op: add
  path: /spec/template/spec/containers/0/args/-
  value: --extra-argument`,
				},
			}
			Expect(testEnv.Create(context.TODO(), configMap)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, configMap)).To(Succeed())

			cluster := &libsveltosv1beta1.SveltosCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   clusterNamespace,
					Name:        clusterName,
					Annotations: map[string]string{perClusterPatchAnnotation: configMap.Name},
				},
			}
			Expect(testEnv.Create(context.TODO(), cluster)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, cluster)).To(Succeed())
		})

		getPatches := func(grouped bool) []libsveltosv1beta1.Patch {
			patches, err := controllers.GetSveltosAgentPatchesForDeployment(context.TODO(), testEnv.Client,
				clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeSveltos, grouped, logger)
			Expect(err).To(BeNil())
			return patches
		}

		It("a per cluster Deployment gets the patches asked by its cluster", func() {
			Expect(getPatches(false)).To(HaveLen(1))
		})

		It("a group Deployment gets only the global patches", func() {
			Expect(getPatches(true)).To(BeEmpty())
		})
	})

	Context("clusters deleted while they are in a group", func() {
		// existing is a cluster that exists: it stays in the group
		var existing string

		BeforeEach(func() {
			existing = randomString()

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNamespace}}
			Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

			cluster := &libsveltosv1beta1.SveltosCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: clusterNamespace, Name: existing},
			}
			Expect(testEnv.Create(context.TODO(), cluster)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, cluster)).To(Succeed())
		})

		getGroupDeployment := func() *appsv1.Deployment {
			groups := groupsOf()
			Expect(groups).To(HaveLen(1))
			deployment, err := getDeployment(groups[0].Name)
			Expect(err).To(BeNil())
			return deployment
		}

		It("a cluster that does not exist anymore leaves its group, the others stay", func() {
			deleted := randomString()
			deployInGroup(existing)
			deployInGroup(deleted)
			deployment := getGroupDeployment()

			controllers.RemoveStaleGroupMembers(context.TODO(), testEnv.Client, deployment, logger)

			groups := groupsOf()
			Expect(groups).To(HaveLen(1))
			Expect(groups[0].Members).To(HaveLen(1))
			Expect(groups[0].Members[0].Name).To(Equal(existing))
			_, err := getDeployment(deployment.Name)
			Expect(err).To(BeNil())

			removeFromGroup(existing)
		})

		It("the Deployment is removed with the last cluster of the group", func() {
			deleted := randomString()
			deployInGroup(deleted)
			deployment := getGroupDeployment()

			controllers.RemoveStaleGroupMembers(context.TODO(), testEnv.Client, deployment, logger)

			Expect(groupsOf()).To(BeEmpty())
			Eventually(func() bool {
				_, err := getDeployment(deployment.Name)
				return apierrors.IsNotFound(err)
			}, timeout, pollingInterval).Should(BeTrue())
		})

		It("an unrelated Deployment is not a group Deployment", func() {
			Expect(controllers.IsSveltosAgentGroupDeployment(&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Labels: controllers.GetSveltosAgentLabels(clusterNamespace, existing,
						libsveltosv1beta1.ClusterTypeSveltos),
				},
			})).To(BeFalse())
			Expect(strings.TrimSpace(groupSveltosAgentComponent)).ToNot(BeEmpty())
		})
	})
})
