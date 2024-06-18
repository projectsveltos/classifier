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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Classifier: update cluster labels", func() {
	const (
		namePrefix = "labels-"
	)

	It("Cluster labels are updated when classifier starts matching", Label("FV"), func() {
		clusterLabels := map[string]string{randomString(): randomString(), randomString(): randomString()}
		classifier := getClassifier(namePrefix, clusterLabels)
		// Cluster won't be a match for this Cassifier
		classifier.Spec.KubernetesVersionConstraints = []libsveltosv1beta1.KubernetesVersionConstraint{
			{
				Version:    "1.25.0",
				Comparison: string(libsveltosv1beta1.ComparisonLessThan),
			},
		}

		Byf("Creating classifier instance %s in the management cluster", classifier.Name)
		Expect(k8sClient.Create(context.TODO(), classifier)).To(Succeed())

		Byf("Getting client to access the workload cluster")
		workloadClient, err := getKindWorkloadClusterKubeconfig()
		Expect(err).To(BeNil())
		Expect(workloadClient).ToNot(BeNil())

		Byf("Verifying Classifier CRD is installed in the workload cluster")
		Eventually(func() error {
			classifierCRD := &apiextensionsv1.CustomResourceDefinition{}
			return workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: "classifiers.lib.projectsveltos.io"}, classifierCRD)
		}, timeout, pollingInterval).Should(BeNil())

		Byf("Verifying Classifier instance is deployed in the workload cluster")
		Eventually(func() error {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			return workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier.Name}, currentClassifier)
		}, timeout, pollingInterval).Should(BeNil())

		verifyClassifierReport(classifier.Name, false)

		Byf("Verifying cluster labels are not set by Classifier %s", classifier.Name)
		Consistently(func() bool {
			currentCuster := &clusterv1.Cluster{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
				currentCuster)
			if err != nil {
				return false
			}
			if currentCuster.Labels == nil {
				return true
			}
			for i := range classifier.Spec.ClassifierLabels {
				cLabel := classifier.Spec.ClassifierLabels[i]
				_, ok := currentCuster.Labels[cLabel.Key]
				if ok {
					return false
				}
			}
			return true
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Change Classifier %s Kubernetes constraints so that cluster is a match", classifier.Name)
		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
		currentClassifier.Spec.KubernetesVersionConstraints = []libsveltosv1beta1.KubernetesVersionConstraint{
			{
				Version:    "1.25.0",
				Comparison: string(libsveltosv1beta1.ComparisonGreaterThanOrEqualTo),
			},
		}
		Expect(k8sClient.Update(context.TODO(), currentClassifier)).To(Succeed())

		verifyClassifierReport(classifier.Name, true)

		verifyClusterLabels(classifier)

		Byf("Deleting classifier instance %s in the management cluster", classifier.Name)
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), currentClassifier)).To(Succeed())

		Byf("Verifying Classifier instance is removed from the workload cluster")
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err = workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Verifying Classifier instance is removed from the management cluster")
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Verifying Cluster labels are not updated because of Classifier being deleted")
		verifyClusterLabels(classifier)

		removeLabels(classifier)
	})
})
