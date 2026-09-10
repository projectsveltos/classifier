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
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clustercache"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

// buildFakeKubeconfig returns a minimal, syntactically valid kubeconfig pointing at server.
// clientcmd only needs to parse this, never dial it, so no real cert/token data is needed.
func buildFakeKubeconfig(server string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: fake-token
`, server))
}

const (
	testKubeVersion124 = "1.24.0"
	value              = "value"
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
						Version:    testKubeVersion124,
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
						Version:    testKubeVersion124,
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

		logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Logger:     logger,
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

		logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
		reconciler := &controllers.ClassifierReconciler{
			Client:     c,
			Scheme:     scheme,
			Mux:        sync.Mutex{},
			ClusterMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Logger:     logger,
		}

		requests := controllers.RequeueClassifierForClassifierReport(reconciler, context.TODO(), report)
		Expect(requests).To(HaveLen(1))
		Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: classifierName}}))
	})
})

var _ = Describe("requeueClassifierForSecret", func() {
	It("evicts clustercache when a cluster's kubeconfig Secret changes, regardless of AccessRequest labels", func() {
		// A kubeconfig Secret's content can change (endpoint, credentials) with no auth error
		// and no cluster deletion - clustercache's other eviction paths never fire for that.
		// requeueClassifierForSecret otherwise only reacts to AccessRequest-labeled Secrets, so
		// this must not be gated on that label. See #1954.
		clusterNamespace := randomString()
		clusterName := randomString()
		secretName := clusterName + "-kubeconfig"
		secretKey := types.NamespacedName{Namespace: clusterNamespace, Name: secretName}

		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: clusterNamespace, Name: clusterName},
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: clusterNamespace, Name: secretName},
			Data: map[string][]byte{
				value: buildFakeKubeconfig("https://10.0.0.1:6443"),
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, secret).Build()
		logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))

		cacheMgr := clustercache.GetManager()
		config, err := cacheMgr.GetKubernetesRestConfig(context.TODO(), c, clusterNamespace, clusterName,
			"", "", libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())
		Expect(config.Host).To(Equal("https://10.0.0.1:6443"))

		// Point the Secret at a different endpoint - no AccessRequest label, no deletion, no
		// auth error.
		Expect(c.Get(context.TODO(), secretKey, secret)).To(Succeed())
		secret.Data[value] = buildFakeKubeconfig("https://10.0.0.2:6443")
		Expect(c.Update(context.TODO(), secret)).To(Succeed())

		reconciler := &controllers.ClassifierReconciler{
			Client: c,
			Scheme: scheme,
			Mux:    sync.Mutex{},
			Logger: logger,
		}
		controllers.RequeueClassifierForSecret(reconciler, context.TODO(), secret)

		config, err = cacheMgr.GetKubernetesRestConfig(context.TODO(), c, clusterNamespace, clusterName,
			"", "", libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())
		Expect(config.Host).To(Equal("https://10.0.0.2:6443"))
	})
})
