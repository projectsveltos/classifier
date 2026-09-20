/*
Copyright 2026. Sveltos SRL. All rights reserved.

This file is part of Sveltos Enterprise. See the LICENSE file at the root
of this repository.
*/

package fv_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Classifier: sveltos-agent evaluation failure is surfaced", func() {
	const namePrefix = "agent-failure-"

	It("AgentFailureMessage is set when sveltos-agent's evaluation fails, and cleared once fixed, "+
		"without disturbing the last known-good Spec.Match", Label("FV", "PULLMODE"), func() {

		classifier := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: namePrefix + randomString(),
			},
			Spec: libsveltosv1beta1.ClassifierSpec{
				ClassifierLabels: []libsveltosv1beta1.ClassifierLabel{
					{Key: key, Value: value},
				},
				KubernetesVersionConstraints: []libsveltosv1beta1.KubernetesVersionConstraint{
					{
						Version:    "1.20.0",
						Comparison: string(libsveltosv1beta1.ComparisonGreaterThanOrEqualTo),
					},
				},
				DeployedResourceConstraint: &libsveltosv1beta1.DeployedResourceConstraint{
					// Namespace is cluster-scoped and always has at least one instance
					// (kube-system, default, ...), so this is a guaranteed match as long
					// as Evaluate does not error out.
					ResourceSelectors: []libsveltosv1beta1.ResourceSelector{
						{
							Group:   "",
							Version: "v1",
							Kind:    "Namespace",
						},
					},
				},
			},
		}

		Byf("Creating classifier instance %s in the management cluster", classifier.Name)
		Expect(k8sClient.Create(context.TODO(), classifier)).To(Succeed())

		Byf("Verifying the classifier is a match before any agent-side error is introduced")
		verifyClassfierIsProvisioned(classifier)
		verifyClassifierReport(classifier.Name, true)
		verifyAgentFailureMessage(classifier.Name, false)

		Byf("Introducing a broken Lua evaluate script, so sveltos-agent's evaluation errors out")
		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Spec.DeployedResourceConstraint.ResourceSelectors[0].Evaluate = "this is not valid lua {{{"
		Expect(k8sClient.Update(context.TODO(), currentClassifier)).To(Succeed())

		Byf("Verifying AgentFailureMessage gets set, while Spec.Match stays at its last known-good value")
		verifyAgentFailureMessage(classifier.Name, true)
		verifyClassifierReport(classifier.Name, true)

		Byf("Fixing the Lua evaluate script")
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Spec.DeployedResourceConstraint.ResourceSelectors[0].Evaluate = ""
		Expect(k8sClient.Update(context.TODO(), currentClassifier)).To(Succeed())

		Byf("Verifying AgentFailureMessage is cleared once evaluation succeeds again")
		verifyAgentFailureMessage(classifier.Name, false)
		verifyClassifierReport(classifier.Name, true)
	})
})

// verifyAgentFailureMessage waits for ClassifierReport.Status.AgentFailureMessage to be set (or
// cleared, if wantSet is false), on the management cluster's copy of the report.
func verifyAgentFailureMessage(classifierName string, wantSet bool) {
	clusterType := libsveltosv1beta1.ClusterTypeCapi
	if kindWorkloadCluster.GetKind() == libsveltosv1beta1.SveltosClusterKind {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	}
	classifierReportName := libsveltosv1beta1.GetClassifierReportName(classifierName, kindWorkloadCluster.GetName(), &clusterType)
	Byf("Verifying ClassifierReport %s AgentFailureMessage is set: %t", classifierReportName, wantSet)
	Eventually(func() bool {
		currentClassifierReport := &libsveltosv1beta1.ClassifierReport{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: classifierReportName},
			currentClassifierReport)
		if err != nil {
			return false
		}
		return (currentClassifierReport.Status.AgentFailureMessage != nil) == wantSet
	}, timeout, pollingInterval).Should(BeTrue())
}
