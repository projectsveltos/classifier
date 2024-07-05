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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Classifier v1alpha1 and verify all is deployed", func() {
	const (
		namePrefix = "v1alpha1-"
	)

	It("Deploy Classifier.v1alpha1 ", Label("FV"), func() {
		key := randomString()
		value := randomString()

		v1alpha1Classifier := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: namePrefix + randomString(),
			},
			Spec: libsveltosv1alpha1.ClassifierSpec{
				ClassifierLabels: []libsveltosv1alpha1.ClassifierLabel{
					{
						Key:   key,
						Value: value,
					},
				},
				KubernetesVersionConstraints: []libsveltosv1alpha1.KubernetesVersionConstraint{
					{
						Version:    "1.29.0",
						Comparison: string(libsveltosv1alpha1.ComparisonGreaterThanOrEqualTo),
					},
				},
			},
		}

		Byf("Creating classifier instance %s in the management cluster", v1alpha1Classifier.Name)
		Expect(k8sClient.Create(context.TODO(), v1alpha1Classifier)).To(Succeed())

		Byf("Getting classifier v1beta1 %s", v1alpha1Classifier.Name)
		v1beta1Classifier := &libsveltosv1beta1.Classifier{}
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: v1alpha1Classifier.Name}, v1beta1Classifier)).To(Succeed())

		Byf("Getting client to access the workload cluster")
		workloadClient, err := getKindWorkloadClusterKubeconfig()
		Expect(err).To(BeNil())
		Expect(workloadClient).ToNot(BeNil())

		Byf("Verifying Classifier instance is deployed in the workload cluster")
		Eventually(func() error {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			return workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: v1alpha1Classifier.Name}, currentClassifier)
		}, timeout, pollingInterval).Should(BeNil())

		verifyClassifierReport(v1beta1Classifier.Name, true)

		verifyClusterLabels(v1beta1Classifier)

		Byf("Deleting classifier instance %s in the management cluster", v1alpha1Classifier.Name)
		currentClassifier := &libsveltosv1alpha1.Classifier{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: v1alpha1Classifier.Name}, currentClassifier)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), currentClassifier)).To(Succeed())

		Byf("Verifying Classifier instance is removed from the workload cluster")
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err = workloadClient.Get(context.TODO(),
				types.NamespacedName{Name: v1alpha1Classifier.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Verifying Classifier instance is removed from the management cluster")
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err = k8sClient.Get(context.TODO(),
				types.NamespacedName{Name: v1alpha1Classifier.Name}, currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())

		Byf("Verifying Cluster labels are not updated because of Classifier being deleted")
		verifyClusterLabels(v1beta1Classifier)

		removeLabels(v1beta1Classifier)
	})
})
