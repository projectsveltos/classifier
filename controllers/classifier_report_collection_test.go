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

package controllers_test

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // SA1019: We are unable to update the dependency at this time.
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Classifier Deployer", func() {
	var classifier *libsveltosv1beta1.Classifier
	var logger logr.Logger

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
		classifier = getClassifierInstance(randomString())
	})

	It("removeClassifierReports deletes all ClassifierReport for a given Classifier instance", func() {
		classifierReport1 := getClassifierReport(classifier.Name, randomString(), randomString())
		classifierReport2 := getClassifierReport(classifier.Name, randomString(), randomString())
		initObjects := []client.Object{
			classifierReport1,
			classifierReport2,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		Expect(controllers.RemoveClassifierReports(context.TODO(), c, classifier, logger)).To(Succeed())

		classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
		Expect(c.List(context.TODO(), classifierReportList)).To(Succeed())
		Expect(len(classifierReportList.Items)).To(BeZero())
	})

	It("removeClusterClassifierReports deletes all ClassifierReport for a given cluster instance", func() {
		clusterType := libsveltosv1beta1.ClusterTypeCapi
		clusterNamespace := randomString()
		clusterName := randomString()

		// Create a classifierReport from clusterNamespace/clusterName for a random Classifier (classifierName)
		classifierName := randomString()
		classifierReport1 := getClassifierReport(classifierName, clusterNamespace, clusterName)
		classifierReport1.Labels = libsveltosv1beta1.GetClassifierReportLabels(
			classifier.Name, clusterName, &clusterType)

		// Create a classifierReport from clusterNamespace/clusterName for a random Classifier (classifierName)
		classifierName = randomString()
		classifierReport2 := getClassifierReport(classifierName, clusterNamespace, clusterName)
		classifierReport2.Labels = libsveltosv1beta1.GetClassifierReportLabels(
			classifier.Name, clusterName, &clusterType)

		initObjects := []client.Object{
			classifierReport1,
			classifierReport2,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		Expect(controllers.RemoveClusterClassifierReports(context.TODO(), c, clusterNamespace, clusterName,
			clusterType, logger)).To(Succeed())

		classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
		Expect(c.List(context.TODO(), classifierReportList)).To(Succeed())
		Expect(len(classifierReportList.Items)).To(BeZero())
	})

	It("collectClassifierReports collects ClassifierReports from clusters", func() {
		cluster := prepareCluster()

		// In managed cluster this is the namespace where ClassifierReports
		// are created
		const classifierReportNamespace = "projectsveltos"
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: classifierReportNamespace,
			},
		}
		err := testEnv.Create(context.TODO(), ns)
		if err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
		}
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

		classifierName := randomString()
		classifier := getClassifierInstance(classifierName)
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		classifierReport := getClassifierReport(classifierName, "", "")
		classifierReport.Namespace = classifierReportNamespace
		Expect(testEnv.Create(context.TODO(), classifierReport)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifierReport)).To(Succeed())

		Expect(controllers.CollectClassifierReportsFromCluster(context.TODO(), testEnv.Client,
			getClusterRef(cluster), version, logger)).To(Succeed())

		clusterType := libsveltosv1beta1.ClusterTypeCapi

		validateClassifierReports(classifierName, cluster, &clusterType)

		// Update ClassifierReports and validate again
		Expect(controllers.CollectClassifierReportsFromCluster(context.TODO(), testEnv.Client,
			getClusterRef(cluster), version, logger)).To(Succeed())

		validateClassifierReports(classifierName, cluster, &clusterType)
	})

})

func validateClassifierReports(classifierName string, cluster *clusterv1.Cluster, clusterType *libsveltosv1beta1.ClusterType) {
	// Verify ClassifierReport is created
	// Eventual loop so testEnv Cache is synced
	Eventually(func() bool {
		classifierReportName := libsveltosv1beta1.GetClassifierReportName(classifierName, cluster.Name, clusterType)
		currentClassifierReport := &libsveltosv1beta1.ClassifierReport{}
		err := testEnv.Get(context.TODO(),
			types.NamespacedName{Namespace: cluster.Namespace, Name: classifierReportName}, currentClassifierReport)
		if err != nil {
			By("Not found")
			return false
		}
		if currentClassifierReport.Labels == nil {
			By("Missing labels")
			return false
		}
		if currentClassifierReport.Spec.ClusterNamespace != cluster.Namespace ||
			currentClassifierReport.Spec.ClusterName != cluster.Name {

			By("Spec ClusterNamespace and ClusterName not set")
			return false
		}
		v, ok := currentClassifierReport.Labels[libsveltosv1beta1.ClassifierlNameLabel]
		return ok && v == classifierName
	}, timeout, pollingInterval).Should(BeTrue())
}
