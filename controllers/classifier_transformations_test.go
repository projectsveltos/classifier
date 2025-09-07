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
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var _ = Describe("ClassifierTransformations map functions", func() {
	It("requeueClassifierForCluster returns all existing Classifiers", func() {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
		}

		Expect(addTypeInformationToObject(scheme, cluster)).To(Succeed())

		classifier0 := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1beta1.ClassifierSpec{
				KubernetesVersionConstraints: []libsveltosv1beta1.KubernetesVersionConstraint{
					{
						Version:    "1.24.0",
						Comparison: string(libsveltosv1beta1.ComparisonEqual),
					},
				},
				ClassifierLabels: []libsveltosv1beta1.ClassifierLabel{
					{Key: randomString(), Value: randomString()},
				},
			},
		}

		classifier1 := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: libsveltosv1beta1.ClassifierSpec{
				KubernetesVersionConstraints: []libsveltosv1beta1.KubernetesVersionConstraint{
					{
						Version:    "1.24.0",
						Comparison: string(libsveltosv1beta1.ComparisonEqual),
					},
				},
				ClassifierLabels: []libsveltosv1beta1.ClassifierLabel{
					{Key: randomString(), Value: randomString()},
				},
			},
		}

		initObjects := []client.Object{
			cluster,
			classifier0,
			classifier1,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
		}

		set := libsveltosset.Set{}
		key := corev1.ObjectReference{
			Kind: cluster.Kind, Namespace: cluster.Namespace, Name: cluster.Name, APIVersion: cluster.APIVersion}

		set.Insert(&corev1.ObjectReference{
			Kind: libsveltosv1beta1.ClassifierKind, Name: classifier0.Name,
			APIVersion: libsveltosv1beta1.GroupVersion.String(),
		})
		reconciler.ClusterMap[key] = &set
		reconciler.AllClassifierSet = libsveltosset.Set{}
		reconciler.AllClassifierSet.Insert(
			&corev1.ObjectReference{Kind: libsveltosv1beta1.ClassifierKind, Name: classifier0.Name},
		)
		reconciler.AllClassifierSet.Insert(
			&corev1.ObjectReference{Kind: libsveltosv1beta1.ClassifierKind, Name: classifier1.Name},
		)

		requests := controllers.RequeueClassifierForCluster(reconciler, context.TODO(), cluster)
		Expect(requests).To(HaveLen(2))
	})
})

var _ = Describe("ClassifierTransformations map functions", func() {
	It("requeueClassifierForClassifierReport returns Classifier report is for", func() {
		classifierName := randomString()
		report := &libsveltosv1beta1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: randomString(),
			},
			Spec: libsveltosv1beta1.ClassifierReportSpec{
				ClusterNamespace: randomString(),
				ClusterName:      randomString(),
				ClassifierName:   classifierName,
			},
		}

		Expect(addTypeInformationToObject(scheme, report)).To(Succeed())

		initObjects := []client.Object{
			report,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
		}

		requests := controllers.RequeueClassifierForClassifierReport(reconciler, context.TODO(), report)
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
			ClusterMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
		}

		classifierName1 := randomString()
		classifierInfo1 := corev1.ObjectReference{
			Kind: libsveltosv1beta1.ClassifierKind, Name: classifierName1, APIVersion: libsveltosv1beta1.GroupVersion.String()}
		reconciler.ClassifierSet.Insert(&classifierInfo1)
		classifierName2 := randomString()
		classifierInfo2 := corev1.ObjectReference{
			Kind: libsveltosv1beta1.ClassifierKind, Name: classifierName2, APIVersion: libsveltosv1beta1.GroupVersion.String()}
		reconciler.ClassifierSet.Insert(&classifierInfo2)

		classifier := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
		}

		requests := controllers.RequeueClassifierForClassifier(reconciler, context.TODO(), classifier)
		Expect(requests).To(HaveLen(2))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifierName2}}))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifierName1}}))
	})
})
