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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

const (
	key   = "env"
	value = "fv"
)

// Byf is a simple wrapper around By.
func Byf(format string, a ...interface{}) {
	By(fmt.Sprintf(format, a...)) // ignore_by_check
}

func randomString() string {
	const length = 10
	return util.RandomString(length)
}

func getClassifier(namePrefix string, clusterLabels map[string]string) *libsveltosv1alpha1.Classifier {
	labels := make([]libsveltosv1alpha1.ClassifierLabel, 0)

	for k := range clusterLabels {
		labels = append(labels, libsveltosv1alpha1.ClassifierLabel{Key: k, Value: clusterLabels[k]})
	}

	classifier := &libsveltosv1alpha1.Classifier{
		ObjectMeta: metav1.ObjectMeta{
			Name: namePrefix + randomString(),
		},
		Spec: libsveltosv1alpha1.ClassifierSpec{
			ClassifierLabels: labels,
			KubernetesVersionConstraints: []libsveltosv1alpha1.KubernetesVersionConstraint{
				{
					Version:    "1.25.0",
					Comparison: string(libsveltosv1alpha1.ComparisonGreaterThanOrEqualTo),
				},
			},
		},
	}

	return classifier
}

// getKindWorkloadClusterKubeconfig returns client to access the kind cluster used as workload cluster
func getKindWorkloadClusterKubeconfig() (client.Client, error) {
	kubeconfigPath := "workload_kubeconfig" // this file is created in this directory by Makefile during cluster creation
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

func verifyClassifierReport(classifierName string, isMatch bool) {
	classifierReportName := libsveltosv1alpha1.GetClassifierReportName(classifierName, kindWorkloadCluster.Name)
	Byf("Verifing ClassifierReport %s for Classifier %s", classifierReportName, classifierName)
	Eventually(func() bool {
		currentClassifierReport := &libsveltosv1alpha1.ClassifierReport{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: classifierReportName},
			currentClassifierReport)
		return err == nil && currentClassifierReport.Spec.Match == isMatch
	}, timeout, pollingInterval).Should(BeTrue())
}

func verifyClusterLabels(classifier *libsveltosv1alpha1.Classifier) {
	Byf("Verifying Cluster labels are updated with labels from Classifier %s", classifier.Name)
	Eventually(func() bool {
		currentCuster := &clusterv1.Cluster{}
		err := k8sClient.Get(context.TODO(),
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
}

func removeLabels(classifier *libsveltosv1alpha1.Classifier) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())

		currentLabels := currentCluster.Labels
		if currentLabels == nil {
			return nil
		}

		for i := range classifier.Spec.ClassifierLabels {
			cLabel := classifier.Spec.ClassifierLabels[i]
			delete(currentCluster.Labels, cLabel.Key)
		}

		return k8sClient.Update(context.TODO(), currentCluster)
	})
	Expect(err).To(BeNil())
}
