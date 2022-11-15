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

package controllers_test

import (
	"context"
	"crypto/sha256"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/gdexlab/go-render/render"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/klogr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/deployer"
	fakedeployer "github.com/projectsveltos/libsveltos/lib/deployer/fake"
)

var _ = Describe("Classifier Deployer", func() {
	It("classifierHash returns Classifier hash", func() {
		classifier := getClassifierInstance(randomString())
		classifier.Spec.DeployedResourceConstraints = []libsveltosv1alpha1.DeployedResourceConstraint{
			{
				Namespace: randomString(),
				Group:     randomString(),
				Kind:      randomString(),
				Version:   randomString(),
				LabelFilters: []libsveltosv1alpha1.LabelFilter{
					{
						Key:   randomString(),
						Value: randomString(),
					},
				},
			},
			{
				Namespace: randomString(),
				Group:     randomString(),
				Kind:      randomString(),
				Version:   randomString(),
				LabelFilters: []libsveltosv1alpha1.LabelFilter{
					{
						Key:   randomString(),
						Value: randomString(),
					},
				},
			},
		}

		h := sha256.New()
		var config string

		config += render.AsCode(classifier.Spec)
		h.Write([]byte(config))
		hash := h.Sum(nil)

		currentHash := controllers.ClassifierHash(classifier)
		Expect(reflect.DeepEqual(currentHash, hash)).To(BeTrue())
	})

	It("deployClassifierCRD deploys Classifier CRD", func() {
		Expect(controllers.DeployClassifierCRD(context.TODO(), testEnv.Config, klogr.New())).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			classifierCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "classifiers.lib.projectsveltos.io"}, classifierCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployClassifierReportCRD deploys ClassifierReport CRD", func() {
		Expect(controllers.DeployClassifierReportCRD(context.TODO(), testEnv.Config, klogr.New())).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			classifierReportCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "classifierreports.lib.projectsveltos.io"}, classifierReportCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployClassifierInstance creates Classifier instance", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		classifier := getClassifierInstance(randomString())
		Expect(controllers.DeployClassifierInstance(ctx, c, classifier, klogr.New())).To(Succeed())

		currentClassifier := &libsveltosv1alpha1.Classifier{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
	})

	It("deployClassifierInstance updates Classifier instance", func() {
		classifier := getClassifierInstance(randomString())

		initObjects := []client.Object{
			classifier,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		classifier.Spec.ClassifierLabels = []libsveltosv1alpha1.ClassifierLabel{{Key: randomString(), Value: randomString()}}
		Expect(controllers.DeployClassifierInstance(ctx, c, classifier, klogr.New())).To(Succeed())

		currentClassifier := &libsveltosv1alpha1.Classifier{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
		Expect(reflect.DeepEqual(currentClassifier.Spec, classifier.Spec)).To(BeTrue())
	})

	It("deployClassifierInCluster deploys Classifier CRD and instance in remote cluster", func() {
		cluster := prepareCluster()
		classifier := getClassifierInstance(randomString())

		Expect(testEnv.Create(context.TODO(), classifier))

		// Just verify result is success (testEnv is used to simulate both management and workload cluster and because
		// classifier is expected in the management cluster, above line is required
		Expect(controllers.DeployClassifierInCluster(context.TODO(), testEnv.Client, cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1alpha1.FeatureClassifier, klogr.New())).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			classifierCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "classifiers.lib.projectsveltos.io"}, classifierCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("processClassifier detects classifier needs to be deployed in cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), klogr.New(), testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1alpha1.FeatureClassifier)).To(Succeed())

		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)
		classifier := getClassifierInstance(randomString())
		classifierScope := getClassifierScope(testEnv.Client, klogr.New(), classifier)

		cluster := prepareCluster()

		f := controllers.GetHandlersForFeature(libsveltosv1alpha1.FeatureClassifier)
		clusterInfo, err := controllers.ProcessClassifier(classifierReconciler, context.TODO(), classifierScope,
			cluster, f, klogr.New())
		Expect(err).To(BeNil())
		Expect(clusterInfo).ToNot(BeNil())
		Expect(clusterInfo.Status).To(Equal(libsveltosv1alpha1.ClassifierStatusProvisioning))
	})

	It("processClassifier detects classifier does not need to be deployed in cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), klogr.New(), testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1alpha1.FeatureClassifier)).To(Succeed())

		cluster := prepareCluster()

		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)
		classifier := getClassifierInstance(randomString())

		// Following are needed to make ProcessClassifier think all that is needed is already deployed
		// Deploy Classifier CRD
		Expect(controllers.DeployClassifierCRD(context.TODO(), testEnv.Config, klogr.New())).To(Succeed())
		// Deploy Classifier instance
		Expect(controllers.DeployClassifierInstance(ctx, testEnv, classifier, klogr.New())).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		hash := controllers.ClassifierHash(classifier)
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, classifier)).To(Succeed())
		classifier.Status = libsveltosv1alpha1.ClassifierStatus{
			ClusterInfo: []libsveltosv1alpha1.ClusterInfo{
				{
					Cluster: corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name},
					Status:  libsveltosv1alpha1.ClassifierStatusProvisioned,
					Hash:    hash,
				},
			},
		}
		Expect(testEnv.Status().Update(context.TODO(), classifier)).To(Succeed())

		Eventually(func() bool {
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, classifier)
			if err != nil {
				return false
			}
			return len(classifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		classifierScope := getClassifierScope(testEnv.Client, klogr.New(), classifier)

		f := controllers.GetHandlersForFeature(libsveltosv1alpha1.FeatureClassifier)
		clusterInfo, err := controllers.ProcessClassifier(classifierReconciler, context.TODO(), classifierScope,
			cluster, f, klogr.New())
		Expect(err).To(BeNil())
		Expect(clusterInfo).ToNot(BeNil())
		Expect(clusterInfo.Status).To(Equal(libsveltosv1alpha1.ClassifierStatusProvisioned))
	})

	It("removeClassifier queue job to remove Classifier from Cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), klogr.New(), testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1alpha1.FeatureClassifier)).To(Succeed())

		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)
		classifier := getClassifierInstance(randomString())
		classifierScope := getClassifierScope(testEnv.Client, klogr.New(), classifier)

		cluster := prepareCluster()
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		currentClassifier := &libsveltosv1alpha1.Classifier{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1alpha1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name},
				Status:  libsveltosv1alpha1.ClassifierStatusProvisioned,
				Hash:    []byte(randomString()),
			},
		}

		Expect(testEnv.Status().Update(context.TODO(), currentClassifier)).To(Succeed())
		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			currentClassifier := &libsveltosv1alpha1.Classifier{}
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err == nil && len(currentClassifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		f := controllers.GetHandlersForFeature(libsveltosv1alpha1.FeatureClassifier)
		err := controllers.RemoveClassifier(classifierReconciler, context.TODO(), classifierScope,
			cluster, f, klogr.New())
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(Equal("cleanup request is queued"))

		key := deployer.GetKey(cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1alpha1.FeatureClassifier, true)
		Expect(dep.IsKeyInProgress(key)).To(BeTrue())
	})

	It("undeployClassifierFromCluster removes Classifier from Cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), klogr.New(), testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1alpha1.FeatureClassifier)).To(Succeed())

		classifier := getClassifierInstance(randomString())

		cluster := prepareCluster()
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		currentClassifier := &libsveltosv1alpha1.Classifier{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1alpha1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name},
				Status:  libsveltosv1alpha1.ClassifierStatusProvisioned,
				Hash:    []byte(randomString()),
			},
		}

		Expect(testEnv.Status().Update(context.TODO(), currentClassifier)).To(Succeed())
		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			currentClassifier := &libsveltosv1alpha1.Classifier{}
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err == nil && len(currentClassifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		err := controllers.UndeployClassifierFromCluster(context.TODO(), testEnv, cluster.Namespace,
			cluster.Name, classifier.Name, libsveltosv1alpha1.FeatureClassifier, klogr.New())
		Expect(err).To(BeNil())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			currentClassifier := &libsveltosv1alpha1.Classifier{}
			err = testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("undeployClassifier queues a job to remove classifier from cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), klogr.New(), testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1alpha1.FeatureClassifier)).To(Succeed())
		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)

		classifier := getClassifierInstance(randomString())

		cluster := prepareCluster()

		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		currentClassifier := &libsveltosv1alpha1.Classifier{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1alpha1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{Namespace: cluster.Namespace, Name: cluster.Name},
				Status:  libsveltosv1alpha1.ClassifierStatusProvisioned,
				Hash:    []byte(randomString()),
			},
		}

		Expect(testEnv.Status().Update(context.TODO(), currentClassifier)).To(Succeed())
		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err == nil && len(currentClassifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		classifierScope := getClassifierScope(testEnv.Client, klogr.New(), currentClassifier)

		f := controllers.GetHandlersForFeature(libsveltosv1alpha1.FeatureClassifier)
		err := controllers.UndeployClassifier(classifierReconciler, context.TODO(), classifierScope, f, klogr.New())
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(Equal("still in the process of removing Classifier from 1 clusters"))

		key := deployer.GetKey(cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1alpha1.FeatureClassifier, true)
		Expect(dep.IsKeyInProgress(key)).To(BeTrue())
	})

	It("deployClassifierAgent deploys classifier agent", func() {
		Expect(controllers.DeployClassifierAgent(ctx, testEnv.Config, klogr.New())).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			currentClassifierAgent := &appsv1.Deployment{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Namespace: "projectsveltos", Name: "classifier-agent-manager"},
				currentClassifierAgent)
		}, timeout, pollingInterval).Should(BeNil())
	})
})

func prepareCluster() *clusterv1.Cluster {
	namespace := randomString()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      randomString(),
		},
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      randomString(),
			Labels: map[string]string{
				clusterv1.ClusterLabelName:             cluster.Name,
				clusterv1.MachineControlPlaneLabelName: "ok",
			},
		},
	}

	Expect(testEnv.Create(context.TODO(), cluster)).To(Succeed())
	Expect(testEnv.Create(context.TODO(), machine)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

	cluster.Status = clusterv1.ClusterStatus{
		InfrastructureReady: true,
		ControlPlaneReady:   true,
	}
	Expect(testEnv.Status().Update(context.TODO(), cluster)).To(Succeed())

	machine.Status = clusterv1.MachineStatus{
		Phase: string(clusterv1.MachinePhaseRunning),
	}
	Expect(testEnv.Status().Update(context.TODO(), machine)).To(Succeed())

	// Create a secret with cluster kubeconfig

	By("Create the secret with cluster kubeconfig")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      cluster.Name + "-kubeconfig",
		},
		Data: map[string][]byte{
			"data": testEnv.Kubeconfig,
		},
	}
	Expect(testEnv.Client.Create(context.TODO(), secret)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, secret)).To(Succeed())

	return cluster
}
