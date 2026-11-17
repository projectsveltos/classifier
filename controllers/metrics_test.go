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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Metrics", func() {
	It("cleanClusterStaleResources removes only the deleted cluster's series", func() {
		logger := textlogger.NewLogger(textlogger.NewConfig())
		namespace := randomString()
		deleted := randomString()
		kept := randomString()

		for _, name := range []string{deleted, kept} {
			cluster := &corev1.ObjectReference{
				Kind: clusterv1.ClusterKind, APIVersion: clusterv1.GroupVersion.String(),
				Namespace: namespace, Name: name,
			}
			controllers.ProgramDuration(time.Second, namespace, name,
				string(libsveltosv1beta1.FeatureClassifier), libsveltosv1beta1.ClusterTypeCapi, logger)
			controllers.TrackClassifierOutcome(randomString(), cluster,
				&libsveltosv1beta1.ClusterInfo{Status: libsveltosv1beta1.SveltosStatusProvisioned}, logger)
		}

		// histogram + outcome counter + last-success gauge
		Expect(controllers.CountClusterMetricSeries(namespace, deleted)).To(Equal(3))
		Expect(controllers.CountClusterMetricSeries(namespace, kept)).To(Equal(3))

		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		_, err := controllers.CleanClusterStaleResources(context.TODO(), c, namespace, deleted,
			libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())

		Expect(controllers.CountClusterMetricSeries(namespace, deleted)).To(Equal(0))
		Expect(controllers.CountClusterMetricSeries(namespace, kept)).To(Equal(3))
	})
})
