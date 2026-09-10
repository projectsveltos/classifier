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

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clustercache"
)

var _ = Describe("cleanClusterStaleResources", func() {
	It("evicts clustercache when the cluster is deleted", func() {
		// A cluster that is deleted and immediately replaced (same namespace/name, new
		// kubeconfig) must not leave the old rest.Config cached. Deletion is the one case
		// InvalidateOnAuthError never sees. cleanClusterStaleResources is shared by both the
		// CAPI Cluster and SveltosCluster deletion paths.
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
		logger := textlogger.NewLogger(textlogger.NewConfig())

		cacheMgr := clustercache.GetManager()
		config, err := cacheMgr.GetKubernetesRestConfig(context.TODO(), c, clusterNamespace, clusterName,
			"", "", libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())
		Expect(config.Host).To(Equal("https://10.0.0.1:6443"))

		// Same namespace/name comes back as a brand new cluster with a different endpoint -
		// exactly what cleanClusterStaleResources must not leave stale.
		Expect(c.Get(context.TODO(), secretKey, secret)).To(Succeed())
		secret.Data[value] = buildFakeKubeconfig("https://10.0.0.2:6443")
		Expect(c.Update(context.TODO(), secret)).To(Succeed())

		_, err = controllers.CleanClusterStaleResources(context.TODO(), c, clusterNamespace, clusterName,
			libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())

		config, err = cacheMgr.GetKubernetesRestConfig(context.TODO(), c, clusterNamespace, clusterName,
			"", "", libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())
		Expect(config.Host).To(Equal("https://10.0.0.2:6443"))
	})
})
