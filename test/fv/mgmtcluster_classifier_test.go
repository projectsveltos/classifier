/*
Copyright 2024. projectsveltos.io. All rights reserved.

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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

const (
	mgmtLabelEnv    = "mgmt-env"
	mgmtLabelTier   = "mgmt-tier"
	capiClusterKind = "Cluster"
	coreAPIVersion  = "v1"
	kindConfigMap   = "ConfigMap"
)

var _ = Describe("ManagementClusterClassifier: labels on managed cluster", func() {
	const (
		namePrefix = "mgmt-classifier-"
	)

	It("adds, updates and removes labels on the managed cluster", Label("FV"), func() {
		verifyMgmtClassifierFlow(namePrefix)
	})
})

func verifyMgmtClassifierFlow(namePrefix string) {
	clusterNS := kindWorkloadCluster.GetNamespace()
	clusterName := kindWorkloadCluster.GetName()
	clusterKind := capiClusterKind
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterKind = libsveltosv1beta1.SveltosClusterKind
	}

	// Create a ConfigMap in the management cluster; classificationLua will use its presence
	// to decide which cluster to label.
	cmNamespace := deplNamespace
	cmName := namePrefix + randomString()
	cmLabelKey := "mgmt-classifier-trigger"
	cmLabelValue := randomString()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cmNamespace,
			Name:      cmName,
			Labels:    map[string]string{cmLabelKey: cmLabelValue},
		},
	}

	Byf("Creating trigger ConfigMap %s/%s in management cluster", cmNamespace, cmName)
	Expect(k8sClient.Create(context.TODO(), cm)).To(Succeed())
	defer func() {
		_ = k8sClient.Delete(context.TODO(), cm)
	}()

	// classificationLua: whenever at least one ConfigMap is present, label the managed cluster.
	classificationLua := fmt.Sprintf(`
function evaluate(resources)
  local result = {}
  if #resources > 0 then
    table.insert(result, {namespace=%q, name=%q, kind=%q})
  end
  return result
end
`, clusterNS, clusterName, clusterKind)

	initialLabels := map[string]string{
		mgmtLabelEnv:  "test",
		mgmtLabelTier: "dev",
	}

	mcc := buildManagementClusterClassifier(namePrefix, cmNamespace, cmLabelKey, cmLabelValue,
		classificationLua, initialLabels)

	Byf("Creating ManagementClusterClassifier %s", mcc.Name)
	Expect(k8sClient.Create(context.TODO(), mcc)).To(Succeed())

	// Wait for labels to appear on the managed cluster.
	Byf("Verifying initial labels are applied to cluster %s/%s", clusterNS, clusterName)
	verifyMgmtClusterLabels(mcc.Spec.ClassifierLabels)

	// Verify that a ManagementClusterClassifierReport was created in the cluster namespace.
	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if clusterKind == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}
	verifyMgmtClassifierReport(mcc.Name, clusterNS, clusterName, clusterType, initialLabels)

	// Update classifierLabels and verify the cluster picks up the new labels.
	updatedLabels := map[string]string{
		mgmtLabelEnv:  "staging",
		mgmtLabelTier: "prod",
	}
	Byf("Updating ManagementClusterClassifier %s classifierLabels", mcc.Name)
	currentMCC := &libsveltosv1beta1.ManagementClusterClassifier{}
	Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: mcc.Name}, currentMCC)).To(Succeed())
	currentMCC.Spec.ClassifierLabels = toClassifierLabels(updatedLabels)
	Expect(k8sClient.Update(context.TODO(), currentMCC)).To(Succeed())

	Byf("Verifying updated labels are applied to cluster %s/%s", clusterNS, clusterName)
	verifyMgmtClusterLabels(currentMCC.Spec.ClassifierLabels)
	verifyMgmtClassifierReport(mcc.Name, clusterNS, clusterName, clusterType, updatedLabels)

	// Delete the ManagementClusterClassifier and verify labels are removed.
	Byf("Deleting ManagementClusterClassifier %s", mcc.Name)
	deleteMCC(mcc.Name)

	Byf("Verifying labels are removed from cluster %s/%s", clusterNS, clusterName)
	verifyMgmtClusterLabelsGone(currentMCC.Spec.ClassifierLabels)

	// Clean up any residual labels on the cluster.
	removeMgmtLabels(currentMCC.Spec.ClassifierLabels)
}

// buildManagementClusterClassifier creates a ManagementClusterClassifier that watches ConfigMaps
// with the given label and applies classifierLabels to the cluster returned by classificationLua.
func buildManagementClusterClassifier(
	namePrefix, cmNamespace, cmLabelKey, cmLabelValue string,
	classificationLua string,
	clusterLabels map[string]string,
) *libsveltosv1beta1.ManagementClusterClassifier {

	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{cmLabelKey: cmLabelValue},
	}

	return &libsveltosv1beta1.ManagementClusterClassifier{
		ObjectMeta: metav1.ObjectMeta{
			Name: namePrefix + randomString(),
		},
		Spec: libsveltosv1beta1.ManagementClusterClassifierSpec{
			MatchResources: []libsveltosv1beta1.ResourceSelector{
				{
					Group:     "",
					Version:   coreAPIVersion,
					Kind:      kindConfigMap,
					Namespace: cmNamespace,
					Selector:  selector,
				},
			},
			ClassificationLua: classificationLua,
			ClassifierLabels:  toClassifierLabels(clusterLabels),
		},
	}
}

// toClassifierLabels converts a plain map to []ClassifierLabel.
func toClassifierLabels(m map[string]string) []libsveltosv1beta1.ClassifierLabel {
	labels := make([]libsveltosv1beta1.ClassifierLabel, 0, len(m))
	for k, v := range m {
		labels = append(labels, libsveltosv1beta1.ClassifierLabel{Key: k, Value: v})
	}
	return labels
}

// verifyMgmtClusterLabels waits until all classifierLabels are present on the managed cluster.
func verifyMgmtClusterLabels(classifierLabels []libsveltosv1beta1.ClassifierLabel) {
	Byf("Verifying management-cluster-classifier labels are applied to cluster %s/%s",
		kindWorkloadCluster.GetNamespace(), kindWorkloadCluster.GetName())
	Eventually(func() bool {
		current, err := getCluster()
		if err != nil {
			return false
		}
		lbls := current.GetLabels()
		if lbls == nil {
			return false
		}
		for i := range classifierLabels {
			cl := classifierLabels[i]
			v, ok := lbls[cl.Key]
			if !ok || v != cl.Value {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyMgmtClusterLabelsGone waits until all classifierLabels are absent from the managed cluster.
func verifyMgmtClusterLabelsGone(classifierLabels []libsveltosv1beta1.ClassifierLabel) {
	Byf("Verifying management-cluster-classifier labels are removed from cluster %s/%s",
		kindWorkloadCluster.GetNamespace(), kindWorkloadCluster.GetName())
	Eventually(func() bool {
		current, err := getCluster()
		if err != nil {
			return false
		}
		lbls := current.GetLabels()
		for i := range classifierLabels {
			if _, ok := lbls[classifierLabels[i].Key]; ok {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyMgmtClassifierReport waits until a ManagementClusterClassifierReport exists for the pair
// and its Status.ManagedLabels contains exactly the expected label keys.
func verifyMgmtClassifierReport(classifierName, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType, expectedLabels map[string]string) {

	reportName := libsveltosv1beta1.GetManagementClusterClassifierReportName(
		classifierName, clusterName, &clusterType)
	Byf("Verifying ManagementClusterClassifierReport %s/%s has ManagedLabels set",
		clusterNamespace, reportName)

	Eventually(func() bool {
		report := &libsveltosv1beta1.ManagementClusterClassifierReport{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: clusterNamespace, Name: reportName}, report); err != nil {
			return false
		}
		if len(report.Status.ManagedLabels) != len(expectedLabels) {
			return false
		}
		managed := make(map[string]bool, len(report.Status.ManagedLabels))
		for _, k := range report.Status.ManagedLabels {
			managed[k] = true
		}
		for k := range expectedLabels {
			if !managed[k] {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

var _ = Describe("ManagementClusterClassifier: label conflict between two instances", func() {
	const (
		conflictNamePrefix = "mgmt-conflict-"
	)

	It("first instance wins; after deletion second instance takes over", Label("FV"), func() {
		verifyMgmtClassifierConflictFlow(conflictNamePrefix)
	})
})

func verifyMgmtClassifierConflictFlow(namePrefix string) {
	clusterNS := kindWorkloadCluster.GetNamespace()
	clusterName := kindWorkloadCluster.GetName()
	clusterKind := capiClusterKind
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterKind = libsveltosv1beta1.SveltosClusterKind
	}

	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if clusterKind == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}

	// Shared ConfigMap trigger used by both classifiers.
	cmNamespace := deplNamespace
	cmName := namePrefix + randomString()
	cmLabelKey := "mgmt-conflict-trigger"
	cmLabelValue := randomString()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cmNamespace,
			Name:      cmName,
			Labels:    map[string]string{cmLabelKey: cmLabelValue},
		},
	}
	Byf("Creating shared trigger ConfigMap %s/%s", cmNamespace, cmName)
	Expect(k8sClient.Create(context.TODO(), cm)).To(Succeed())
	defer func() {
		_ = k8sClient.Delete(context.TODO(), cm)
	}()

	classificationLua := fmt.Sprintf(`
function evaluate(resources)
  local result = {}
  if #resources > 0 then
    table.insert(result, {namespace=%q, name=%q, kind=%q})
  end
  return result
end
`, clusterNS, clusterName, clusterKind)

	// Both MCCs claim the same label keys on the same cluster — a conflict.
	conflictKey1 := "mgmt-conflict-" + randomString()
	conflictKey2 := "mgmt-conflict-" + randomString()

	labels1 := map[string]string{conflictKey1: "v1-first", conflictKey2: "v2-first"}
	labels2 := map[string]string{conflictKey1: "v1-second", conflictKey2: "v2-second"}

	mcc1 := buildManagementClusterClassifier(namePrefix, cmNamespace, cmLabelKey, cmLabelValue,
		classificationLua, labels1)
	mcc2 := buildManagementClusterClassifier(namePrefix, cmNamespace, cmLabelKey, cmLabelValue,
		classificationLua, labels2)

	Byf("Creating first ManagementClusterClassifier %s", mcc1.Name)
	Expect(k8sClient.Create(context.TODO(), mcc1)).To(Succeed())

	Byf("Waiting for mcc1 labels to be applied to cluster %s/%s", clusterNS, clusterName)
	verifyMgmtClusterLabels(mcc1.Spec.ClassifierLabels)
	verifyMgmtClassifierReport(mcc1.Name, clusterNS, clusterName, clusterType, labels1)

	Byf("Creating second ManagementClusterClassifier %s (conflicting keys)", mcc2.Name)
	Expect(k8sClient.Create(context.TODO(), mcc2)).To(Succeed())

	conflictKeys := []string{conflictKey1, conflictKey2}

	Byf("Verifying mcc2 report has UnManagedLabels (conflict with mcc1)")
	verifyMgmtClassifierReportUnManagedLabels(mcc2.Name, clusterNS, clusterName, clusterType, conflictKeys)

	Byf("Verifying cluster still has mcc1 labels")
	verifyMgmtClusterLabels(mcc1.Spec.ClassifierLabels)

	Byf("Deleting first ManagementClusterClassifier %s", mcc1.Name)
	deleteMCC(mcc1.Name)

	Byf("Verifying mcc2 now manages the labels after mcc1 is gone")
	verifyMgmtClassifierReport(mcc2.Name, clusterNS, clusterName, clusterType, labels2)
	verifyMgmtClusterLabels(mcc2.Spec.ClassifierLabels)

	Byf("Deleting second ManagementClusterClassifier %s", mcc2.Name)
	deleteMCC(mcc2.Name)

	Byf("Verifying labels are removed from cluster %s/%s", clusterNS, clusterName)
	verifyMgmtClusterLabelsGone(mcc2.Spec.ClassifierLabels)

	removeMgmtLabels(mcc1.Spec.ClassifierLabels)
	removeMgmtLabels(mcc2.Spec.ClassifierLabels)
}

// multiGVKLuaTemplate labels a cluster only when both a ConfigMap and a Secret are present.
// %q placeholders receive (clusterNS, clusterName, clusterKind) at call time.
const multiGVKLuaTemplate = `
function evaluate(resources)
  local hasCM     = false
  local hasSecret = false
  for _, r in ipairs(resources) do
    if r.kind == "ConfigMap" then hasCM     = true end
    if r.kind == "Secret"    then hasSecret = true end
  end
  local result = {}
  if hasCM and hasSecret then
    table.insert(result, {namespace=%q, name=%q, kind=%q})
  end
  return result
end
`

var _ = Describe("ManagementClusterClassifier: multi-GVK requirement", func() {
	const (
		namePrefix = "mgmt-multigvk-"
	)

	It("applies labels only when resources of both GVKs are present", Label("FV"), func() {
		verifyMgmtMultiGVKFlow(namePrefix)
	})
})

func verifyMgmtMultiGVKFlow(namePrefix string) {
	clusterNS := kindWorkloadCluster.GetNamespace()
	clusterName := kindWorkloadCluster.GetName()
	clusterKind := capiClusterKind
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterKind = libsveltosv1beta1.SveltosClusterKind
	}

	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if clusterKind == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}

	// Unique label values per run so resources from parallel tests don't interfere.
	cmLabelKey := "mgmt-multigvk-cm"
	cmLabelValue := randomString()
	svcLabelKey := "mgmt-multigvk-svc"
	svcLabelValue := randomString()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: deplNamespace,
			Name:      namePrefix + randomString(),
			Labels:    map[string]string{cmLabelKey: cmLabelValue},
		},
	}
	Byf("Creating trigger ConfigMap %s/%s", deplNamespace, cm.Name)
	Expect(k8sClient.Create(context.TODO(), cm)).To(Succeed())
	defer func() { _ = k8sClient.Delete(context.TODO(), cm) }()

	svcRes := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: deplNamespace,
			Name:      namePrefix + randomString(),
			Labels:    map[string]string{svcLabelKey: svcLabelValue},
		},
	}
	Byf("Creating trigger Secret %s/%s", deplNamespace, svcRes.Name)
	Expect(k8sClient.Create(context.TODO(), svcRes)).To(Succeed())
	defer func() { _ = k8sClient.Delete(context.TODO(), svcRes) }()

	classificationLua := fmt.Sprintf(multiGVKLuaTemplate, clusterNS, clusterName, clusterKind)

	multiGVKLabels := map[string]string{"mgmt-multigvk-role": "combined"}

	cmSelector := libsveltosv1beta1.ResourceSelector{
		Group:     "",
		Version:   coreAPIVersion,
		Kind:      kindConfigMap,
		Namespace: deplNamespace,
		Selector:  &metav1.LabelSelector{MatchLabels: map[string]string{cmLabelKey: cmLabelValue}},
	}
	secretSelector := libsveltosv1beta1.ResourceSelector{
		Group:     "",
		Version:   coreAPIVersion,
		Kind:      "Secret",
		Namespace: deplNamespace,
		Selector:  &metav1.LabelSelector{MatchLabels: map[string]string{svcLabelKey: svcLabelValue}},
	}
	mcc := buildMCCFromSelectors(namePrefix,
		[]libsveltosv1beta1.ResourceSelector{cmSelector, secretSelector},
		classificationLua, multiGVKLabels)

	Byf("Creating ManagementClusterClassifier %s", mcc.Name)
	Expect(k8sClient.Create(context.TODO(), mcc)).To(Succeed())

	Byf("Verifying labels are applied when both ConfigMap and Secret are present")
	verifyMgmtClusterLabels(mcc.Spec.ClassifierLabels)
	verifyMgmtClassifierReport(mcc.Name, clusterNS, clusterName, clusterType, multiGVKLabels)

	Byf("Deleting ConfigMap %s/%s to break the multi-GVK requirement", deplNamespace, cm.Name)
	currentCM := &corev1.ConfigMap{}
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: deplNamespace, Name: cm.Name}, currentCM)).To(Succeed())
	Expect(k8sClient.Delete(context.TODO(), currentCM)).To(Succeed())

	Byf("Verifying labels are removed when only one GVK remains")
	verifyMgmtClusterLabelsGone(mcc.Spec.ClassifierLabels)

	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: deplNamespace,
			Name:      namePrefix + randomString(),
			Labels:    map[string]string{cmLabelKey: cmLabelValue},
		},
	}
	Byf("Re-creating ConfigMap %s/%s to restore both GVKs", deplNamespace, newCM.Name)
	Expect(k8sClient.Create(context.TODO(), newCM)).To(Succeed())
	defer func() { _ = k8sClient.Delete(context.TODO(), newCM) }()

	Byf("Verifying labels reappear once both GVKs are present again")
	verifyMgmtClusterLabels(mcc.Spec.ClassifierLabels)

	Byf("Deleting ManagementClusterClassifier %s", mcc.Name)
	deleteMCC(mcc.Name)

	verifyMgmtClusterLabelsGone(mcc.Spec.ClassifierLabels)
	removeMgmtLabels(mcc.Spec.ClassifierLabels)
}

// buildMCCFromSelectors creates a ManagementClusterClassifier with an arbitrary set of ResourceSelectors.
func buildMCCFromSelectors(
	namePrefix string,
	selectors []libsveltosv1beta1.ResourceSelector,
	classificationLua string,
	clusterLabels map[string]string,
) *libsveltosv1beta1.ManagementClusterClassifier {

	return &libsveltosv1beta1.ManagementClusterClassifier{
		ObjectMeta: metav1.ObjectMeta{
			Name: namePrefix + randomString(),
		},
		Spec: libsveltosv1beta1.ManagementClusterClassifierSpec{
			MatchResources:    selectors,
			ClassificationLua: classificationLua,
			ClassifierLabels:  toClassifierLabels(clusterLabels),
		},
	}
}

// deleteMCC deletes a ManagementClusterClassifier by name and waits until it is gone.
func deleteMCC(name string) {
	current := &libsveltosv1beta1.ManagementClusterClassifier{}
	Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: name}, current)).To(Succeed())
	Expect(k8sClient.Delete(context.TODO(), current)).To(Succeed())
	Eventually(func() bool {
		obj := &libsveltosv1beta1.ManagementClusterClassifier{}
		err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: name}, obj)
		return apierrors.IsNotFound(err)
	}, timeout, pollingInterval).Should(BeTrue())
}

// verifyMgmtClassifierReportUnManagedLabels waits until the report for the given (classifier, cluster)
// pair has UnManagedLabels containing exactly the expected keys.
func verifyMgmtClassifierReportUnManagedLabels(classifierName, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType, expectedKeys []string) {

	reportName := libsveltosv1beta1.GetManagementClusterClassifierReportName(
		classifierName, clusterName, &clusterType)
	Byf("Verifying ManagementClusterClassifierReport %s/%s has UnManagedLabels set",
		clusterNamespace, reportName)

	Eventually(func() bool {
		report := &libsveltosv1beta1.ManagementClusterClassifierReport{}
		if err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: clusterNamespace, Name: reportName}, report); err != nil {
			return false
		}
		if len(report.Status.UnManagedLabels) != len(expectedKeys) {
			return false
		}
		unmanaged := make(map[string]bool, len(report.Status.UnManagedLabels))
		for _, ul := range report.Status.UnManagedLabels {
			unmanaged[ul.Key] = true
		}
		for _, k := range expectedKeys {
			if !unmanaged[k] {
				return false
			}
		}
		return true
	}, timeout, pollingInterval).Should(BeTrue())
}

// removeMgmtLabels removes any residual classifier-applied labels from the cluster.
func removeMgmtLabels(classifierLabels []libsveltosv1beta1.ClassifierLabel) {
	current, err := getCluster()
	if err != nil {
		return
	}
	lbls := current.GetLabels()
	if lbls == nil {
		return
	}
	changed := false
	for i := range classifierLabels {
		if _, ok := lbls[classifierLabels[i].Key]; ok {
			delete(lbls, classifierLabels[i].Key)
			changed = true
		}
	}
	if !changed {
		return
	}
	current.SetLabels(lbls)
	_ = k8sClient.Update(context.TODO(), current)
}
