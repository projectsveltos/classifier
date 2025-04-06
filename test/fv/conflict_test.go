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

package fv_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Classifier: update cluster labels", func() {
	const (
		namePrefix = "conflict-"
	)

	It("Deploy Classifier two conflicting instances in CAPI clusters", Label("FV"), func() {
		clusterLabels := map[string]string{randomString(): randomString(), randomString(): randomString()}
		classifier1 := getClassifier(namePrefix, clusterLabels)
		classifier2 := getClassifier(namePrefix, clusterLabels)
		for i := range classifier2.Spec.ClassifierLabels {
			classifier2.Spec.ClassifierLabels[i].Value += randomString()
		}

		Byf("Creating classifier instance %s in the management cluster", classifier1.Name)
		Expect(k8sClient.Create(context.TODO(), classifier1)).To(Succeed())

		Byf("Getting client to access the workload cluster")
		workloadClient, err := getKindWorkloadClusterKubeconfig()
		Expect(err).To(BeNil())
		Expect(workloadClient).ToNot(BeNil())

		// In agentless mode, classifier instances are only in the management cluster
		if !isAgentLessMode() {
			Byf("Verifying Classifier CRD is installed in the workload cluster")
			Eventually(func() error {
				classifierCRD := &apiextensionsv1.CustomResourceDefinition{}
				return workloadClient.Get(context.TODO(),
					types.NamespacedName{Name: "classifiers.lib.projectsveltos.io"}, classifierCRD)
			}, timeout, pollingInterval).Should(BeNil())

			Byf("Verifying Classifier instance %s is deployed in the workload cluster", classifier1.Name)
			Eventually(func() error {
				currentClassifier := &libsveltosv1beta1.Classifier{}
				return workloadClient.Get(context.TODO(),
					types.NamespacedName{Name: classifier1.Name}, currentClassifier)
			}, timeout, pollingInterval).Should(BeNil())
		}

		verifyClassifierReport(classifier1.Name, true)

		verifyClusterLabels(classifier1)

		Byf("Creating classifier instance %s in the management cluster", classifier2.Name)
		Expect(k8sClient.Create(context.TODO(), classifier2)).To(Succeed())

		if !isAgentLessMode() {
			Byf("Verifying Classifier instance %s is deployed in the workload cluster", classifier2.Name)
			Eventually(func() error {
				currentClassifier := &libsveltosv1beta1.Classifier{}
				return workloadClient.Get(context.TODO(),
					types.NamespacedName{Name: classifier2.Name}, currentClassifier)
			}, timeout, pollingInterval).Should(BeNil())
		}

		verifyClassifierReport(classifier2.Name, true)

		verifyClusterLabels(classifier1)

		Byf("Deleting classifier instance %s in the management cluster", classifier1.Name)
		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: classifier1.Name}, currentClassifier)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), currentClassifier)).To(Succeed())

		if !isAgentLessMode() {
			Byf("Verifying Classifier %s instance is removed from the workload cluster", classifier1.Name)
			Eventually(func() bool {
				err = workloadClient.Get(context.TODO(),
					types.NamespacedName{Name: classifier1.Name}, currentClassifier)
				return err != nil && apierrors.IsNotFound(err)
			}, timeout, pollingInterval).Should(BeTrue())
		}

		Byf("Verifying Classifier %s instance is removed from the management cluster", classifier1.Name)
		Eventually(func() bool {
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier1.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		clusterType := libsveltosv1beta1.ClusterTypeCapi
		classifierReportName := libsveltosv1beta1.GetClassifierReportName(classifier1.Name, kindWorkloadCluster.Name, &clusterType)
		Byf("Verifying ClassifierReports instance %s is removed from the management cluster", classifierReportName)
		Eventually(func() bool {
			classifierReport := &libsveltosv1beta1.ClassifierReport{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: classifierReportName}, classifierReport)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		verifyClusterLabels(classifier2)

		Byf("Deleting classifier instance %s in the management cluster", classifier2.Name)
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: classifier2.Name}, currentClassifier)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), currentClassifier)).To(Succeed())

		if !isAgentLessMode() {
			Byf("Verifying Classifier instance is removed from the workload cluster")
			Eventually(func() bool {
				currentClassifier := &libsveltosv1beta1.Classifier{}
				err = workloadClient.Get(context.TODO(),
					types.NamespacedName{Name: classifier2.Name}, currentClassifier)
				return err != nil && apierrors.IsNotFound(err)
			}, timeout, pollingInterval).Should(BeTrue())
		}

		Byf("Verifying Classifier instance is removed from the management cluster")
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier2.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		verifyClusterLabels(classifier2)

		removeLabels(classifier1)
		removeLabels(classifier2)
	})
})
