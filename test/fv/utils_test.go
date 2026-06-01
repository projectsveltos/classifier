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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
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

func getClassifier(namePrefix string, clusterLabels map[string]string) *libsveltosv1beta1.Classifier {
	labels := make([]libsveltosv1beta1.ClassifierLabel, 0)

	for k := range clusterLabels {
		labels = append(labels, libsveltosv1beta1.ClassifierLabel{Key: k, Value: clusterLabels[k]})
	}

	classifier := &libsveltosv1beta1.Classifier{
		ObjectMeta: metav1.ObjectMeta{
			Name: namePrefix + randomString(),
		},
		Spec: libsveltosv1beta1.ClassifierSpec{
			ClassifierLabels: labels,
			KubernetesVersionConstraints: []libsveltosv1beta1.KubernetesVersionConstraint{
				{
					Version:    "1.30.0",
					Comparison: string(libsveltosv1beta1.ComparisonGreaterThanOrEqualTo),
				},
			},
		},
	}

	return classifier
}

func verifyClassfierIsProvisioned(classifier *libsveltosv1beta1.Classifier) {
	Byf("Verifying classifier %s is provisioned on cluster", classifier.Name)
	currentCuster, err := getCluster()
	Expect(err).To(BeNil())
	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}
	reportName := libsveltosv1beta1.GetClassifierReportName(classifier.Name, currentCuster.GetName(), &clusterType)
	Eventually(func() bool {
		report := &libsveltosv1beta1.ClassifierReport{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: currentCuster.GetNamespace(), Name: reportName}, report); err != nil {
			return false
		}
		return report.Status.DeploymentStatus != nil &&
			*report.Status.DeploymentStatus == libsveltosv1beta1.SveltosStatusProvisioned
	}, timeout, pollingInterval).Should(BeTrue())
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
	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}
	classifierReportName := libsveltosv1beta1.GetClassifierReportName(classifierName, kindWorkloadCluster.GetName(), &clusterType)
	Byf("Verifing ClassifierReport %s for Classifier %s", classifierReportName, classifierName)
	Eventually(func() bool {
		currentClassifierReport := &libsveltosv1beta1.ClassifierReport{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: classifierReportName},
			currentClassifierReport)
		if err != nil {
			return false
		}
		if currentClassifierReport.Spec.Match != isMatch {
			return false
		}
		return currentClassifierReport.Status.DeploymentStatus != nil &&
			*currentClassifierReport.Status.DeploymentStatus == libsveltosv1beta1.SveltosStatusProvisioned
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyClassifierReportManagedLabels checks that ClassifierReport.Status.ManagedLabels
// contains exactly the label keys from the Classifier spec. This validates the new
// post-migration storage location for managed-label tracking.
func verifyClassifierReportManagedLabels(classifier *libsveltosv1beta1.Classifier) {
	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}
	reportName := libsveltosv1beta1.GetClassifierReportName(classifier.Name, kindWorkloadCluster.GetName(), &clusterType)
	Byf("Verifying ClassifierReport %s has ManagedLabels set", reportName)
	expected := make(map[string]bool, len(classifier.Spec.ClassifierLabels))
	for i := range classifier.Spec.ClassifierLabels {
		expected[classifier.Spec.ClassifierLabels[i].Key] = true
	}
	Eventually(func() bool {
		report := &libsveltosv1beta1.ClassifierReport{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: reportName}, report); err != nil {
			return false
		}
		if len(report.Status.ManagedLabels) != len(expected) {
			return false
		}
		for _, k := range report.Status.ManagedLabels {
			if !expected[k] {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyClassifierReportUnManagedLabels checks that ClassifierReport.Status.UnManagedLabels
// contains exactly the label keys from the Classifier spec. Used to confirm conflict detection.
func verifyClassifierReportUnManagedLabels(classifier *libsveltosv1beta1.Classifier) {
	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}
	reportName := libsveltosv1beta1.GetClassifierReportName(classifier.Name, kindWorkloadCluster.GetName(), &clusterType)
	Byf("Verifying ClassifierReport %s has UnManagedLabels set", reportName)
	expected := make(map[string]bool, len(classifier.Spec.ClassifierLabels))
	for i := range classifier.Spec.ClassifierLabels {
		expected[classifier.Spec.ClassifierLabels[i].Key] = true
	}
	Eventually(func() bool {
		report := &libsveltosv1beta1.ClassifierReport{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: reportName}, report); err != nil {
			return false
		}
		if len(report.Status.UnManagedLabels) != len(expected) {
			return false
		}
		for _, ul := range report.Status.UnManagedLabels {
			if !expected[ul.Key] {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyClassifierStatusEmpty confirms that the old Classifier.Status fields
// (ClusterInfo and MachingClusterStatuses) are not being written to. After the
// migration these must remain empty — the data lives in ClassifierReport.Status.
func verifyClassifierStatusEmpty(classifierName string) {
	Byf("Verifying Classifier %s Status is empty (data lives in ClassifierReport.Status)", classifierName)
	Eventually(func() bool {
		classifier := &libsveltosv1beta1.Classifier{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: classifierName}, classifier); err != nil {
			return false
		}
		return len(classifier.Status.ClusterInfo) == 0 && //nolint:staticcheck // deprecated fields must stay empty after migration
			len(classifier.Status.MachingClusterStatuses) == 0 //nolint:staticcheck // deprecated fields must stay empty after migration
	}, timeout, pollingInterval).Should(BeTrue())
}

func verifyClusterLabels(classifier *libsveltosv1beta1.Classifier) {
	Byf("Verifying Cluster labels are updated with labels from Classifier %s", classifier.Name)
	Byf("Classifier labels: %v", classifier.Spec.ClassifierLabels)
	Eventually(func() bool {
		currentCuster, err := getCluster()
		if err != nil {
			return false
		}
		if currentCuster.GetLabels() == nil {
			return false
		}
		Byf("Current cluster labels: %v", currentCuster.GetLabels())
		for i := range classifier.Spec.ClassifierLabels {
			cLabel := classifier.Spec.ClassifierLabels[i]
			v, ok := currentCuster.GetLabels()[cLabel.Key]
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

func verifyClusterLabelsAreGone(classifier *libsveltosv1beta1.Classifier) {
	Byf("Verifying Classifier labels are removed from cluster %s", classifier.Name)
	Eventually(func() bool {
		currentCuster, err := getCluster()
		if err != nil {
			return false
		}
		labels := currentCuster.GetLabels()
		for i := range classifier.Spec.ClassifierLabels {
			cLabel := classifier.Spec.ClassifierLabels[i]
			if _, ok := labels[cLabel.Key]; ok {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

func removeLabels(classifier *libsveltosv1beta1.Classifier) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentCluster, err := getCluster()
		if err != nil {
			return err
		}

		currentLabels := currentCluster.GetLabels()
		if currentLabels == nil {
			return nil
		}

		for i := range classifier.Spec.ClassifierLabels {
			cLabel := classifier.Spec.ClassifierLabels[i]
			delete(currentLabels, cLabel.Key)
		}

		currentCluster.SetLabels(currentLabels)

		return k8sClient.Update(context.TODO(), currentCluster)
	})
	Expect(err).To(BeNil())
}

func addTypeInformationToObject(scheme *runtime.Scheme, obj client.Object) error {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("missing apiVersion or kind and cannot assign it; %w", err)
	}

	for _, gvk := range gvks {
		if gvk.Kind == "" {
			continue
		}
		if gvk.Version == "" || gvk.Version == runtime.APIVersionInternal {
			continue
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		break
	}

	return nil
}

func getCluster() (*unstructured.Unstructured, error) {
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		return getSveltosCluster()
	}

	return getCapiCluster()
}

func getSveltosCluster() (*unstructured.Unstructured, error) {
	currentCuster := &libsveltosv1beta1.SveltosCluster{}
	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{
			Namespace: kindWorkloadCluster.GetNamespace(),
			Name:      kindWorkloadCluster.GetName()},
		currentCuster)
	if err != nil {
		return nil, err
	}

	Expect(addTypeInformationToObject(scheme, currentCuster)).To(Succeed())

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&currentCuster)
	if err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(unstructuredObj)
	return u, nil
}

func getCapiCluster() (*unstructured.Unstructured, error) {
	currentCuster := &clusterv1.Cluster{}
	err := k8sClient.Get(context.TODO(),
		types.NamespacedName{
			Namespace: kindWorkloadCluster.GetNamespace(),
			Name:      kindWorkloadCluster.GetName()},
		currentCuster)
	if err != nil {
		return nil, err
	}

	Expect(addTypeInformationToObject(scheme, currentCuster)).To(Succeed())

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&currentCuster)
	if err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(unstructuredObj)
	return u, nil
}
