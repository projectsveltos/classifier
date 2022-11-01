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
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var _ = Describe("ClassifierTransformations map functions", func() {
	It("requeueClassifierForCluster returns matching ClusterSummary", func() {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
		}

		Expect(addTypeInformationToObject(scheme, cluster)).To(Succeed())

		classifier0 := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1alpha1.ClassifierSpec{
				KubernetesVersion: libsveltosv1alpha1.KubernetesVersion{
					Version:    "1.24.0",
					Comparison: string(libsveltosv1alpha1.ComparisonEqual),
				},
				ClassifierLabels: []libsveltosv1alpha1.ClassifierLabel{
					{Key: randomString(), Value: randomString()},
				},
			},
		}

		classifier1 := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1alpha1.ClassifierSpec{
				KubernetesVersion: libsveltosv1alpha1.KubernetesVersion{
					Version:    "1.24.0",
					Comparison: string(libsveltosv1alpha1.ComparisonEqual),
				},
				ClassifierLabels: []libsveltosv1alpha1.ClassifierLabel{
					{Key: randomString(), Value: randomString()},
				},
			},
		}

		initObjects := []client.Object{
			cluster,
			classifier0,
			classifier1,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[libsveltosv1alpha1.PolicyRef]*libsveltosset.Set),
		}

		set := libsveltosset.Set{}
		key := libsveltosv1alpha1.PolicyRef{Kind: cluster.Kind, Namespace: cluster.Namespace, Name: cluster.Name}

		set.Insert(&libsveltosv1alpha1.PolicyRef{Kind: libsveltosv1alpha1.ClassifierKind, Name: classifier0.Name})
		reconciler.ClusterMap[key] = &set

		requests := controllers.RequeueClassifierForCluster(reconciler, cluster)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal(classifier0.Name))

		set.Insert(&libsveltosv1alpha1.PolicyRef{Kind: libsveltosv1alpha1.ClassifierKind, Name: classifier1.Name})
		reconciler.ClusterMap[key] = &set

		requests = controllers.RequeueClassifierForCluster(reconciler, cluster)
		Expect(requests).To(HaveLen(2))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifier0.Name}}))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifier1.Name}}))
	})
})

var _ = Describe("ClassifierTransformations map functions", func() {
	It("requeueClassifierForClassifierReport returns Classifier report is for", func() {
		classifierName := randomString()
		report := &libsveltosv1alpha1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
			Spec: libsveltosv1alpha1.ClassifierReportSpec{
				ClusterNamespace: randomString(),
				ClusterName:      randomString(),
				ClassifierName:   classifierName,
			},
		}

		Expect(addTypeInformationToObject(scheme, report)).To(Succeed())

		initObjects := []client.Object{
			report,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[libsveltosv1alpha1.PolicyRef]*libsveltosset.Set),
		}

		requests := controllers.RequeueClassifierForClassifierReport(reconciler, report)
		Expect(requests).To(HaveLen(1))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifierName}}))
	})
})

var _ = Describe("ClassifierTransformations map functions", func() {
	It("requeueClassifierForClassifier returns Classifiers with at least one conflict", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[libsveltosv1alpha1.PolicyRef]*libsveltosset.Set),
		}

		classifierName1 := randomString()
		classifierInfo1 := libsveltosv1alpha1.PolicyRef{Kind: libsveltosv1alpha1.ClassifierKind, Name: classifierName1}
		reconciler.ClassifierSet.Insert(&classifierInfo1)
		classifierName2 := randomString()
		classifierInfo2 := libsveltosv1alpha1.PolicyRef{Kind: libsveltosv1alpha1.ClassifierKind, Name: classifierName2}
		reconciler.ClassifierSet.Insert(&classifierInfo2)

		classifier := &libsveltosv1alpha1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
		}

		requests := controllers.RequeueClassifierForClassifier(reconciler, classifier)
		Expect(requests).To(HaveLen(2))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifierName2}}))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifierName1}}))
	})
})
