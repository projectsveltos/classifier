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

package scope_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	classifyv1alpha1 "github.com/projectsveltos/classifier/api/v1alpha1"
	"github.com/projectsveltos/classifier/pkg/scope"
)

const (
	classifierNamePrefix  = "scope-"
	failedToDeploy        = "failed to deploy"
	apiserverNotReachable = "apiserver not reachable"
)

var _ = Describe("ClassifierScope", func() {
	var classifier *classifyv1alpha1.Classifier
	var c client.Client

	BeforeEach(func() {
		classifier = &classifyv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: classifierNamePrefix + randomString(),
			},
		}

		scheme := setupScheme()
		initObjects := []client.Object{classifier}
		c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
	})

	It("Return nil,error if Classifier is not specified", func() {
		params := scope.ClassifierScopeParams{
			Client: c,
			Logger: klogr.New(),
		}

		scope, err := scope.NewClassifierScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Return nil,error if client is not specified", func() {
		params := scope.ClassifierScopeParams{
			Classifier: classifier,
			Logger:     klogr.New(),
		}

		scope, err := scope.NewClassifierScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Name returns Classifier Name", func() {
		params := scope.ClassifierScopeParams{
			Client:     c,
			Classifier: classifier,
			Logger:     klogr.New(),
		}

		scope, err := scope.NewClassifierScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		Expect(scope.Name()).To(Equal(classifier.Name))
	})

	It("SetClusterInfo updates Classifier Status ClusterInfo", func() {
		params := scope.ClassifierScopeParams{
			Client:     c,
			Classifier: classifier,
			Logger:     klogr.New(),
		}

		scope, err := scope.NewClassifierScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		clusterNamespace := randomString()
		clusterName := randomString()
		hash := []byte(randomString())
		clusterInfo := classifyv1alpha1.ClusterInfo{
			Cluster: corev1.ObjectReference{Namespace: clusterNamespace, Name: clusterName},
			Status:  classifyv1alpha1.ClassifierStatusProvisioned,
			Hash:    hash,
		}
		scope.SetClusterInfo([]classifyv1alpha1.ClusterInfo{clusterInfo})
		Expect(classifier.Status.ClusterInfo).ToNot(BeNil())
		Expect(len(classifier.Status.ClusterInfo)).To(Equal(1))
		Expect(classifier.Status.ClusterInfo[0].Cluster.Namespace).To(Equal(clusterNamespace))
		Expect(classifier.Status.ClusterInfo[0].Cluster.Name).To(Equal(clusterName))
		Expect(classifier.Status.ClusterInfo[0].Hash).To(Equal(hash))
		Expect(classifier.Status.ClusterInfo[0].Status).To(Equal(classifyv1alpha1.ClassifierStatusProvisioned))
	})

	It("SetMatchingClusters sets Classifier.Status.MatchingCluster", func() {
		params := scope.ClassifierScopeParams{
			Client:     c,
			Classifier: classifier,
			Logger:     klogr.New(),
		}

		scope, err := scope.NewClassifierScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		failureMessage := randomString()
		machingClusterStatuses := []classifyv1alpha1.MachingClusterStatus{
			{
				ClusterRef: corev1.ObjectReference{
					Namespace: "t-" + randomString(),
					Name:      "c-" + randomString(),
				},
				ManagedLabels: []string{randomString(), randomString()},
				UnManagedLabels: []classifyv1alpha1.UnManagedLabel{
					{Key: randomString(), FailureMessage: &failureMessage},
				},
			},
		}
		scope.SetMachingClusterStatuses(machingClusterStatuses)
		Expect(reflect.DeepEqual(classifier.Status.MachingClusterStatuses, machingClusterStatuses)).To(BeTrue())
	})

})
