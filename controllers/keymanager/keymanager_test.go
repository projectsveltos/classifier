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

package keymanager_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers/keymanager"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

const (
	upstreamClusterNamePrefix = "chart-manager"
)

var _ = Describe("Chart manager", func() {
	var classifier *libsveltosv1alpha1.Classifier
	var cluster *clusterv1.Cluster
	var sveltosCluster *libsveltosv1alpha1.SveltosCluster
	var c client.Client
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = setupScheme()

		classifier = &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1alpha1.ClassifierSpec{
				ClassifierLabels: []libsveltosv1alpha1.ClassifierLabel{
					{Key: randomString(), Value: randomString()},
					{Key: randomString(), Value: randomString()},
				},
			},
		}

		cluster = &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      upstreamClusterNamePrefix + randomString(),
			},
		}

		sveltosCluster = &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      upstreamClusterNamePrefix + randomString(),
			},
		}

		initObjects := []client.Object{
			classifier,
		}

		c = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

	})

	AfterEach(func() {
		removeSubscriptions(c, classifier, cluster.Namespace, cluster.Name, libsveltosv1alpha1.ClusterTypeCapi)
		removeSubscriptions(c, classifier, sveltosCluster.Namespace, sveltosCluster.Name, libsveltosv1alpha1.ClusterTypeSveltos)
	})

	It("registerClusterSummaryForCharts registers classifier for all referenced helm charts",
		func() {
			manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
			Expect(err).To(BeNil())

			manager.RegisterClassifierForLabels(classifier, cluster.Namespace, cluster.Name, libsveltosv1alpha1.ClusterTypeCapi)

			for i := range classifier.Spec.ClassifierLabels {
				labelKey := &classifier.Spec.ClassifierLabels[i].Key
				By(fmt.Sprintf("Verifying Classifier %s manages label (key) %s",
					classifier.Name, *labelKey))
				Expect(manager.CanManageLabel(classifier, cluster.Namespace, cluster.Name,
					*labelKey, libsveltosv1alpha1.ClusterTypeCapi)).To(BeTrue())
			}
		})

	It("CanManageLabel return true only for the first registered Classifier", func() {
		manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
		Expect(err).To(BeNil())

		clusterType := libsveltosv1alpha1.ClusterTypeCapi
		manager.RegisterClassifierForLabels(classifier, cluster.Namespace, cluster.Name, clusterType)

		tmpClassifier := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: classifier.Name + randomString(),
			},
			Spec: classifier.Spec,
		}

		manager.RegisterClassifierForLabels(tmpClassifier, cluster.Namespace, cluster.Name, clusterType)
		defer removeSubscriptions(c, tmpClassifier, cluster.Namespace, cluster.Name, libsveltosv1alpha1.ClusterTypeCapi)

		for i := range tmpClassifier.Spec.ClassifierLabels {
			labelKey := &tmpClassifier.Spec.ClassifierLabels[i].Key
			By(fmt.Sprintf("Verifying Classifier %s does not manage label (key) %s",
				tmpClassifier.Name, *labelKey))
			Expect(manager.CanManageLabel(tmpClassifier, cluster.Namespace, cluster.Name, *labelKey, clusterType)).To(BeFalse())
		}
	})

	It("removeStaleRegistrations removes registration for labels not referenced anymore", func() {
		manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
		Expect(err).To(BeNil())

		clusterType := libsveltosv1alpha1.ClusterTypeSveltos
		manager.RegisterClassifierForLabels(classifier, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)

		oldLabels := make([]libsveltosv1alpha1.ClassifierLabel, 0)
		for i := range classifier.Spec.ClassifierLabels {
			labelKey := &classifier.Spec.ClassifierLabels[i].Key
			By(fmt.Sprintf("Verifying Classifier %s manages label (key) %s",
				classifier.Name, *labelKey))
			Expect(manager.CanManageLabel(classifier, sveltosCluster.Namespace, sveltosCluster.Name, *labelKey,
				clusterType)).To(BeTrue())
			oldLabels = append(oldLabels, classifier.Spec.ClassifierLabels[i])
		}

		classifier.Spec.ClassifierLabels = nil
		manager.RemoveStaleRegistrations(classifier, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)

		for i := range oldLabels {
			labelKey := &oldLabels[i].Key
			By(fmt.Sprintf("Verifying Classifier %s manages label (key) %s",
				classifier.Name, *labelKey))
			Expect(manager.CanManageLabel(classifier, sveltosCluster.Namespace, sveltosCluster.Name, *labelKey,
				clusterType)).To(BeFalse())
		}
	})

	It("isClassifierAlreadyRegistered returns true if a classifier key is already present", func() {
		key := randomString()
		keys := []string{randomString(), randomString(), key, randomString()}
		Expect(keymanager.IsClassifierAlreadyRegistered(keys, key+randomString())).To(BeFalse())
		Expect(keymanager.IsClassifierAlreadyRegistered(keys, key)).To(BeTrue())
	})

	It("GetManagerForKey returns the name of the Classifier managing a label (key)", func() {
		manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
		Expect(err).To(BeNil())

		clusterType := libsveltosv1alpha1.ClusterTypeSveltos
		manager.RegisterClassifierForLabels(classifier, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)

		tmpClassifier := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: classifier.Name + randomString(),
			},
			Spec: classifier.Spec,
		}

		newLabel := libsveltosv1alpha1.ClassifierLabel{Key: randomString(), Value: randomString()}
		tmpClassifier.Spec.ClassifierLabels = append(tmpClassifier.Spec.ClassifierLabels, newLabel)

		manager.RegisterClassifierForLabels(tmpClassifier, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)
		defer removeSubscriptions(c, tmpClassifier, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)

		csName, err := manager.GetManagerForKey(sveltosCluster.Namespace, sveltosCluster.Name, newLabel.Key, clusterType)
		Expect(err).To(BeNil())
		Expect(csName).To(Equal(tmpClassifier.Name))

		for i := range classifier.Spec.ClassifierLabels {
			labelKey := &classifier.Spec.ClassifierLabels[i].Key
			By(fmt.Sprintf("Verifying Classifier %s does not manage label (key) %s",
				tmpClassifier.Name, *labelKey))
			Expect(manager.CanManageLabel(tmpClassifier, sveltosCluster.Namespace, sveltosCluster.Name, *labelKey,
				clusterType)).To(BeFalse())
			Expect(manager.CanManageLabel(classifier, sveltosCluster.Namespace, sveltosCluster.Name, *labelKey,
				clusterType)).To(BeTrue())
		}
	})

	It("GetRegisteredClassifiers returns currently registered Classifiers filtering by ∂∂Cluster",
		func() {
			manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
			Expect(err).To(BeNil())

			clusterType := libsveltosv1alpha1.ClusterTypeSveltos
			manager.RegisterClassifierForLabels(classifier, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)

			tmpClassifier1 := &libsveltosv1alpha1.Classifier{
				ObjectMeta: metav1.ObjectMeta{
					Name: classifier.Name + randomString(),
				},
				Spec: classifier.Spec,
			}
			manager.RegisterClassifierForLabels(tmpClassifier1, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)
			defer removeSubscriptions(c, tmpClassifier1, sveltosCluster.Namespace, sveltosCluster.Name, clusterType)

			tmpClassifier2 := &libsveltosv1alpha1.Classifier{
				ObjectMeta: metav1.ObjectMeta{
					Name: classifier.Name + randomString(),
				},
				Spec: classifier.Spec,
			}
			tmpNamespace := sveltosCluster.Namespace + randomString()
			manager.RegisterClassifierForLabels(tmpClassifier2, tmpNamespace, sveltosCluster.Name, clusterType)
			defer removeSubscriptions(c, tmpClassifier2, tmpNamespace, sveltosCluster.Name, clusterType)

			registered := manager.GetRegisteredClassifiers(sveltosCluster.Namespace, sveltosCluster.Name, clusterType)
			Expect(len(registered)).To(Equal(2))
			Expect(registered).To(ContainElement(classifier.Name))
			Expect(registered).To(ContainElement(tmpClassifier1.Name))
		})

	It("rebuildRegistrations rebuilds label (keys) registrations", func() {
		Expect(len(classifier.Spec.ClassifierLabels)).Should(BeNumerically(">=", 2))

		// Mark classifier as manager for one release
		classifier.Status = libsveltosv1alpha1.ClassifierStatus{
			MachingClusterStatuses: []libsveltosv1alpha1.MachingClusterStatus{
				{
					ClusterRef: corev1.ObjectReference{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name,
						APIVersion: libsveltosv1alpha1.GroupVersion.String(), Kind: libsveltosv1alpha1.SveltosClusterKind},
					ManagedLabels:   []string{classifier.Spec.ClassifierLabels[0].Key},
					UnManagedLabels: []libsveltosv1alpha1.UnManagedLabel{{Key: classifier.Spec.ClassifierLabels[1].Key}},
				},
			},
		}
		Expect(c.Status().Update(context.TODO(), classifier)).To(Succeed())

		// Mark tmpClassifier as manager for classifier.Spec.ClassifierLabels[1]
		tmpClassifier := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: classifier.Name + randomString(),
			},
			Spec: classifier.Spec,
			Status: libsveltosv1alpha1.ClassifierStatus{
				MachingClusterStatuses: []libsveltosv1alpha1.MachingClusterStatus{
					{
						ClusterRef: corev1.ObjectReference{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name,
							APIVersion: libsveltosv1alpha1.GroupVersion.String(), Kind: libsveltosv1alpha1.SveltosClusterKind},
						ManagedLabels:   []string{classifier.Spec.ClassifierLabels[1].Key},
						UnManagedLabels: []libsveltosv1alpha1.UnManagedLabel{{Key: classifier.Spec.ClassifierLabels[0].Key}},
					},
				},
			},
		}
		Expect(c.Create(context.TODO(), tmpClassifier)).To(Succeed())
		defer removeSubscriptions(c, tmpClassifier, sveltosCluster.Namespace, sveltosCluster.Name, libsveltosv1alpha1.ClusterTypeSveltos)

		manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
		Expect(err).To(BeNil())

		err = keymanager.RebuildRegistrations(manager, context.TODO(), c)
		Expect(err).To(BeNil())

		Expect(manager.CanManageLabel(classifier, sveltosCluster.Namespace, sveltosCluster.Name,
			classifier.Spec.ClassifierLabels[0].Key, libsveltosv1alpha1.ClusterTypeSveltos)).To(BeTrue())
		Expect(manager.CanManageLabel(tmpClassifier, sveltosCluster.Namespace, sveltosCluster.Name,
			classifier.Spec.ClassifierLabels[0].Key, libsveltosv1alpha1.ClusterTypeSveltos)).To(BeFalse())

		Expect(manager.CanManageLabel(classifier, sveltosCluster.Namespace, sveltosCluster.Name,
			classifier.Spec.ClassifierLabels[1].Key, libsveltosv1alpha1.ClusterTypeSveltos)).To(BeFalse())
		Expect(manager.CanManageLabel(tmpClassifier, sveltosCluster.Namespace, sveltosCluster.Name,
			classifier.Spec.ClassifierLabels[1].Key, libsveltosv1alpha1.ClusterTypeSveltos)).To(BeTrue())
	})
})

func removeSubscriptions(c client.Client, classifier *libsveltosv1alpha1.Classifier,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) {

	manager, err := keymanager.GetKeyManagerInstance(context.TODO(), c)
	Expect(err).To(BeNil())

	classifier.Spec.ClassifierLabels = nil
	manager.RemoveStaleRegistrations(classifier, clusterNamespace, clusterName, clusterType)
}

func setupScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(libsveltosv1alpha1.AddToScheme(s)).To(Succeed())
	Expect(clusterv1.AddToScheme(s)).To(Succeed())
	Expect(clientgoscheme.AddToScheme(s)).To(Succeed())
	return s
}
