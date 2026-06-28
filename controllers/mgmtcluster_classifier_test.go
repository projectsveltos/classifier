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

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"

	"github.com/projectsveltos/classifier/controllers"
)

const (
	testLabelEnv  = "env"
	testLabelApp  = "app"
	testValueProd = "prod"
)

var _ = Describe("ManagementClusterClassifier utils", func() {

	Context("doesMatchLabelFilters", func() {
		It("returns true when all Equal filters match", func() {
			u := buildUnstructuredConfigMap("cm", map[string]string{
				testLabelEnv: testValueProd,
				testLabelApp: "nginx",
			})
			filters := []libsveltosv1beta1.LabelFilter{
				{Key: testLabelEnv, Operation: libsveltosv1beta1.OperationEqual, Value: testValueProd},
				{Key: testLabelApp, Operation: libsveltosv1beta1.OperationHas},
			}
			Expect(controllers.DoesMatchLabelFilters(u, filters)).To(BeTrue())
		})

		It("returns false when an Equal filter value does not match", func() {
			u := buildUnstructuredConfigMap("cm", map[string]string{testLabelEnv: "dev"})
			filters := []libsveltosv1beta1.LabelFilter{
				{Key: testLabelEnv, Operation: libsveltosv1beta1.OperationEqual, Value: testValueProd},
			}
			Expect(controllers.DoesMatchLabelFilters(u, filters)).To(BeFalse())
		})

		It("returns false when a Has filter key is missing", func() {
			u := buildUnstructuredConfigMap("cm", map[string]string{"other": "x"})
			filters := []libsveltosv1beta1.LabelFilter{
				{Key: testLabelEnv, Operation: libsveltosv1beta1.OperationHas},
			}
			Expect(controllers.DoesMatchLabelFilters(u, filters)).To(BeFalse())
		})

		It("returns false when DoesNotHave filter key is present", func() {
			u := buildUnstructuredConfigMap("cm", map[string]string{testLabelEnv: "x"})
			filters := []libsveltosv1beta1.LabelFilter{
				{Key: testLabelEnv, Operation: libsveltosv1beta1.OperationDoesNotHave},
			}
			Expect(controllers.DoesMatchLabelFilters(u, filters)).To(BeFalse())
		})

		It("returns false when Different filter value equals", func() {
			u := buildUnstructuredConfigMap("cm", map[string]string{testLabelEnv: testValueProd})
			filters := []libsveltosv1beta1.LabelFilter{
				{Key: testLabelEnv, Operation: libsveltosv1beta1.OperationDifferent, Value: testValueProd},
			}
			Expect(controllers.DoesMatchLabelFilters(u, filters)).To(BeFalse())
		})

		It("returns true with no filters", func() {
			u := buildUnstructuredConfigMap("cm", nil)
			Expect(controllers.DoesMatchLabelFilters(u, nil)).To(BeTrue())
		})
	})

	Context("runClassificationLua", func() {
		It("returns no clusters for empty resource list", func() {
			script := `
function evaluate(resources)
  local result = {}
  if #resources > 0 then
    table.insert(result, {namespace="default", name="cluster1", kind="Cluster"})
  end
  return result
end
`
			refs, err := controllers.RunClassificationLua(script, nil, testEnv.GetLogger())
			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(BeEmpty())
		})

		It("returns cluster refs when resources are present", func() {
			script := `
function evaluate(resources)
  local result = {}
  table.insert(result, {namespace="ns1", name="cluster1", kind="Cluster"})
  table.insert(result, {namespace="ns2", name="sc1", kind="SveltosCluster"})
  return result
end
`
			resources := []*unstructured.Unstructured{
				buildUnstructuredConfigMap("cm1", nil),
			}
			refs, err := controllers.RunClassificationLua(script, resources, testEnv.GetLogger())
			Expect(err).ToNot(HaveOccurred())
			Expect(refs).To(HaveLen(2))
		})

		It("returns an error when the script fails to compile", func() {
			_, err := controllers.RunClassificationLua("not valid lua !@#", nil, testEnv.GetLogger())
			Expect(err).To(HaveOccurred())
		})

		It("returns an error when evaluate function is missing", func() {
			// Valid Lua but no evaluate function
			_, err := controllers.RunClassificationLua(`local x = 1`, nil, testEnv.GetLogger())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("clusterTypeFromKind", func() {
		It("returns ClusterTypeCapi for Cluster (case insensitive)", func() {
			ct, err := controllers.ClusterTypeFromKind("Cluster")
			Expect(err).ToNot(HaveOccurred())
			Expect(ct).To(Equal(libsveltosv1beta1.ClusterTypeCapi))
		})

		It("returns ClusterTypeSveltos for SveltosCluster", func() {
			ct, err := controllers.ClusterTypeFromKind("SveltosCluster")
			Expect(err).ToNot(HaveOccurred())
			Expect(ct).To(Equal(libsveltosv1beta1.ClusterTypeSveltos))
		})

		It("returns an error for unknown kind", func() {
			_, err := controllers.ClusterTypeFromKind("Unknown")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("mgmtClassifierAsClassifier", func() {
		It("prefixes the name with mgmt:", func() {
			mcc := &libsveltosv1beta1.ManagementClusterClassifier{
				ObjectMeta: metav1.ObjectMeta{Name: "my-classifier"},
				Spec: libsveltosv1beta1.ManagementClusterClassifierSpec{
					ClassifierLabels: []libsveltosv1beta1.ClassifierLabel{
						{Key: testLabelEnv, Value: testValueProd},
					},
				},
			}
			c := controllers.MgmtClassifierAsClassifier(mcc)
			Expect(c.Name).To(Equal("mgmt:my-classifier"))
			Expect(c.Spec.ClassifierLabels).To(HaveLen(1))
			Expect(c.Spec.ClassifierLabels[0].Key).To(Equal(testLabelEnv))
		})
	})

	Context("classifierLabelKeys", func() {
		It("returns all keys from ClassifierLabel slice", func() {
			labels := []libsveltosv1beta1.ClassifierLabel{
				{Key: "k1", Value: "v1"},
				{Key: "k2", Value: "v2"},
			}
			keys := controllers.ClassifierLabelKeys(labels)
			Expect(keys).To(ConsistOf("k1", "k2"))
		})

		It("returns empty slice for nil input", func() {
			Expect(controllers.ClassifierLabelKeys(nil)).To(HaveLen(0))
		})
	})

	Context("ensureMgmtClassifierReport", func() {
		It("creates a report when it does not exist", func() {
			ns := randomString()
			clusterName := randomString()
			classifierName := randomString()
			clusterType := libsveltosv1beta1.ClusterTypeCapi

			s := mgmtTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(s).
				WithStatusSubresource(&libsveltosv1beta1.ManagementClusterClassifierReport{}).
				Build()

			err := controllers.EnsureMgmtClassifierReport(context.TODO(), fakeClient,
				classifierName, ns, clusterName, clusterType)
			Expect(err).ToNot(HaveOccurred())

			reportName := libsveltosv1beta1.GetManagementClusterClassifierReportName(
				classifierName, clusterName, &clusterType)
			report := &libsveltosv1beta1.ManagementClusterClassifierReport{}
			Expect(fakeClient.Get(context.TODO(),
				types.NamespacedName{Namespace: ns, Name: reportName}, report)).To(Succeed())
			Expect(report.Spec.ClassifierName).To(Equal(classifierName))
			Expect(report.Spec.ClusterName).To(Equal(clusterName))
			Expect(report.Spec.ClusterType).To(Equal(clusterType))
		})

		It("is idempotent when the report already exists", func() {
			ns := randomString()
			clusterName := randomString()
			classifierName := randomString()
			clusterType := libsveltosv1beta1.ClusterTypeCapi

			reportName := libsveltosv1beta1.GetManagementClusterClassifierReportName(
				classifierName, clusterName, &clusterType)
			existing := &libsveltosv1beta1.ManagementClusterClassifierReport{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: reportName},
				Spec: libsveltosv1beta1.ManagementClusterClassifierReportSpec{
					ClassifierName: classifierName,
					ClusterName:    clusterName,
					ClusterType:    clusterType,
				},
			}

			s := mgmtTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(s).
				WithObjects(existing).
				WithStatusSubresource(&libsveltosv1beta1.ManagementClusterClassifierReport{}).
				Build()

			err := controllers.EnsureMgmtClassifierReport(context.TODO(), fakeClient,
				classifierName, ns, clusterName, clusterType)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("listMgmtClassifierReports", func() {
		It("returns all reports for the given classifier", func() {
			classifierName := randomString()
			clusterType := libsveltosv1beta1.ClusterTypeCapi

			r1Name := libsveltosv1beta1.GetManagementClusterClassifierReportName(
				classifierName, "cluster1", &clusterType)
			r2Name := libsveltosv1beta1.GetManagementClusterClassifierReportName(
				classifierName, "cluster2", &clusterType)
			otherName := libsveltosv1beta1.GetManagementClusterClassifierReportName(
				"other-classifier", "cluster1", &clusterType)

			r1 := reportWithLabel(classifierName, "ns1", r1Name, "cluster1", clusterType)
			r2 := reportWithLabel(classifierName, "ns2", r2Name, "cluster2", clusterType)
			other := reportWithLabel("other-classifier", "ns1", otherName, "cluster1", clusterType)

			s := mgmtTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(s).
				WithObjects(r1, r2, other).
				Build()

			reports, err := controllers.ListMgmtClassifierReports(context.TODO(), fakeClient, classifierName)
			Expect(err).ToNot(HaveOccurred())
			Expect(reports).To(HaveLen(2))
		})
	})
})

// buildUnstructuredConfigMap creates an *unstructured.Unstructured ConfigMap in the default namespace.
func buildUnstructuredConfigMap(name string, lbls map[string]string) *unstructured.Unstructured {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
			Labels:    lbls,
		},
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
	Expect(err).ToNot(HaveOccurred())
	u := &unstructured.Unstructured{Object: obj}
	u.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
	return u
}

// mgmtTestScheme builds a minimal scheme for fake-client tests of the new controller.
func mgmtTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(libsveltosv1beta1.AddToScheme(s)).To(Succeed())
	Expect(corev1.AddToScheme(s)).To(Succeed())
	return s
}

// reportWithLabel builds a ManagementClusterClassifierReport with the classifier name label set.
func reportWithLabel(classifierName, ns, reportName, clusterName string,
	clusterType libsveltosv1beta1.ClusterType) *libsveltosv1beta1.ManagementClusterClassifierReport {

	return &libsveltosv1beta1.ManagementClusterClassifierReport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      reportName,
			Labels: libsveltosv1beta1.GetManagementClusterClassifierReportLabels(
				classifierName, clusterName, &clusterType),
		},
		Spec: libsveltosv1beta1.ManagementClusterClassifierReportSpec{
			ClassifierName: classifierName,
			ClusterName:    clusterName,
			ClusterType:    clusterType,
		},
	}
}
