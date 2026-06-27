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

package main

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

const testClassifierName = "default-classifier"

func TestMigration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Migration Suite")
}

var _ = Describe("migrateOneClassifier", func() {
	var (
		ctx    context.Context
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	buildScheme := func() *runtime.Scheme {
		s := runtime.NewScheme()
		Expect(libsveltosv1beta1.AddToScheme(s)).To(Succeed())
		Expect(clusterv1.AddToScheme(s)).To(Succeed())
		Expect(clientgoscheme.AddToScheme(s)).To(Succeed())
		Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
		return s
	}

	It("creates ClassifierReport and clears deprecated fields when namespace exists", func() {
		clusterNamespace := "existing-ns"
		clusterName := "my-cluster"
		classifierName := testClassifierName
		status := libsveltosv1beta1.SveltosStatusProvisioned
		msg := "all good"

		cluster := corev1.ObjectReference{
			APIVersion: libsveltosv1beta1.GroupVersion.String(),
			Kind:       libsveltosv1beta1.SveltosClusterKind,
			Namespace:  clusterNamespace,
			Name:       clusterName,
		}

		classifier := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{Name: classifierName},
			Status: libsveltosv1beta1.ClassifierStatus{
				ClusterInfo: []libsveltosv1beta1.ClusterInfo{
					{
						Cluster:        cluster,
						Status:         status,
						FailureMessage: &msg,
					},
				},
			},
		}

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNamespace}}

		scheme := buildScheme()
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(classifier, &libsveltosv1beta1.ClassifierReport{}).
			WithObjects(classifier, ns).
			Build()

		Expect(migrateOneClassifier(ctx, c, classifier, logger)).To(Succeed())

		// ClassifierReport must exist in the cluster namespace
		clusterType := libsveltosv1beta1.ClusterTypeSveltos
		reportName := libsveltosv1beta1.GetClassifierReportName(classifierName, clusterName, &clusterType)
		report := &libsveltosv1beta1.ClassifierReport{}
		Expect(c.Get(ctx, types.NamespacedName{Namespace: clusterNamespace, Name: reportName}, report)).To(Succeed())
		Expect(report.Status.DeploymentStatus).NotTo(BeNil())
		Expect(*report.Status.DeploymentStatus).To(Equal(status))
		Expect(report.Status.FailureMessage).NotTo(BeNil())
		Expect(*report.Status.FailureMessage).To(Equal(msg))

		// Deprecated fields must be cleared
		updated := &libsveltosv1beta1.Classifier{}
		Expect(c.Get(ctx, types.NamespacedName{Name: classifierName}, updated)).To(Succeed())
		Expect(updated.Status.ClusterInfo).To(BeNil())            //nolint:staticcheck // deprecated, migration only
		Expect(updated.Status.MachingClusterStatuses).To(BeNil()) //nolint:staticcheck // deprecated, migration only
	})

	It("skips stale entry and still clears deprecated fields when namespace is gone", func() {
		staleNamespace := "deleted-ns"
		staleClusterName := "customer-b-shoot-1"
		classifierName := testClassifierName
		status := libsveltosv1beta1.SveltosStatusProvisioning
		msg := `SveltosCluster.lib.projectsveltos.io "customer-b-shoot-1" not found`

		cluster := corev1.ObjectReference{
			APIVersion: libsveltosv1beta1.GroupVersion.String(),
			Kind:       libsveltosv1beta1.SveltosClusterKind,
			Namespace:  staleNamespace,
			Name:       staleClusterName,
		}

		classifier := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{Name: classifierName},
			Status: libsveltosv1beta1.ClassifierStatus{
				ClusterInfo: []libsveltosv1beta1.ClusterInfo{
					{
						Cluster:        cluster,
						Status:         status,
						FailureMessage: &msg,
					},
				},
			},
		}

		// Namespace is intentionally NOT created — it was deleted with the cluster.
		scheme := buildScheme()
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(classifier, &libsveltosv1beta1.ClassifierReport{}).
			WithObjects(classifier).
			Build()

		Expect(migrateOneClassifier(ctx, c, classifier, logger)).To(Succeed())

		// No ClassifierReport must have been created in the gone namespace
		clusterType := libsveltosv1beta1.ClusterTypeSveltos
		reportName := libsveltosv1beta1.GetClassifierReportName(classifierName, staleClusterName, &clusterType)
		report := &libsveltosv1beta1.ClassifierReport{}
		err := c.Get(ctx, types.NamespacedName{Namespace: staleNamespace, Name: reportName}, report)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("not found"))

		// Deprecated fields must still be cleared
		updated := &libsveltosv1beta1.Classifier{}
		Expect(c.Get(ctx, types.NamespacedName{Name: classifierName}, updated)).To(Succeed())
		Expect(updated.Status.ClusterInfo).To(BeNil())            //nolint:staticcheck // deprecated, migration only
		Expect(updated.Status.MachingClusterStatuses).To(BeNil()) //nolint:staticcheck // deprecated, migration only
	})
})
