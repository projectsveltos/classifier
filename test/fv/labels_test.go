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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	classifyv1alpha1 "github.com/projectsveltos/classifier/api/v1alpha1"
)

var _ = Describe("Classifier: update cluster labels", func() {
	const (
		namePrefix = "labels"
	)

	It("Deploy Classifier instance in CAPI clusters", Label("FV"), func() {
		clusterLabels := map[string]string{randomString(): randomString(), randomString(): randomString()}
		classifier := getClassifier(namePrefix, clusterLabels)

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
				types.NamespacedName{Name: "classifiers.classify.projectsveltos.io"}, classifierCRD)
		}, timeout, pollingInterval).Should(BeNil())

		Byf("Verifying Classifier instance is deployed in the workload cluster")
		Eventually(func() error {
			currentClassifier := &classifyv1alpha1.Classifier{}
			return workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier.Name}, currentClassifier)
		}, timeout, pollingInterval).Should(BeNil())

		By("Creatiing ClassifierReport indicating cluster is a match")
		classifierReportName := fmt.Sprintf("%s-%s", kindWorkloadCluster.Name, classifier.Name)
		classifierReport := classifyv1alpha1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: kindWorkloadCluster.Namespace,
				Name:      classifierReportName,
				Labels: map[string]string{
					classifyv1alpha1.ClassifierLabelName: classifier.Name,
				},
			},
			Spec: classifyv1alpha1.ClassifierReportSpec{
				ClusterNamespace: kindWorkloadCluster.Namespace,
				ClusterName:      kindWorkloadCluster.Name,
				ClassifierName:   classifier.Name,
				Match:            true,
			},
		}
		Expect(k8sClient.Create(context.TODO(), &classifierReport)).To(Succeed())

		Byf("Verifying Cluster labels are updated")
		Eventually(func() bool {
			currentCuster := &clusterv1.Cluster{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
				currentCuster)
			if err != nil {
				return false
			}
			if currentCuster.Labels == nil {
				return false
			}
			for i := range classifier.Spec.ClassifierLabels {
				cLabel := classifier.Spec.ClassifierLabels[i]
				v, ok := currentCuster.Labels[cLabel.Key]
				if !ok {
					return false
				}
				if v != cLabel.Value {
					return false
				}
			}
			return true
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Deleting classifier instance %s in the management cluster", classifier.Name)
		currentClassifier := &classifyv1alpha1.Classifier{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), currentClassifier)).To(Succeed())

		Byf("Verifying Classifier instance is removed from the workload cluster")
		Eventually(func() bool {
			currentClassifier := &classifyv1alpha1.Classifier{}
			err = workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Verifying Classifier instance is removed from the management cluster")
		Eventually(func() bool {
			currentClassifier := &classifyv1alpha1.Classifier{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Name: classifier.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Verifying Cluster labels are not updated because of Classifier being deleted")
		Eventually(func() bool {
			currentCuster := &clusterv1.Cluster{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
				currentCuster)
			if err != nil {
				return false
			}
			if currentCuster.Labels == nil {
				return false
			}
			for i := range classifier.Spec.ClassifierLabels {
				cLabel := classifier.Spec.ClassifierLabels[i]
				v, ok := currentCuster.Labels[cLabel.Key]
				if !ok {
					return false
				}
				if v != cLabel.Value {
					return false
				}
			}
			return true
		}, timeout, pollingInterval).Should(BeTrue())

	})

})
