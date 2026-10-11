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
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/eventreasons"
)

const eventBufferSize = 10

var _ = Describe("Classifier events", func() {
	var classifier *libsveltosv1beta1.Classifier
	var cluster *corev1.ObjectReference
	var recorder *events.FakeRecorder
	var reconciler *controllers.ClassifierReconciler

	BeforeEach(func() {
		classifier = getClassifierInstance(randomString())
		cluster = &corev1.ObjectReference{
			Namespace:  randomString(),
			Name:       randomString(),
			APIVersion: libsveltosv1beta1.GroupVersion.String(),
			Kind:       libsveltosv1beta1.SveltosClusterKind,
		}
		recorder = events.NewFakeRecorder(eventBufferSize)
		reconciler = &controllers.ClassifierReconciler{EventRecorder: recorder}
	})

	Context("recordWarningf", func() {
		It("does nothing when no recorder is set", func() {
			Expect(func() {
				controllers.RecordWarningf(nil, classifier, eventreasons.ClassifierFailed,
					eventreasons.ActionReconciling, "failed: %v", errors.New("boom"))
			}).ToNot(Panic())
		})

		It("records a Warning event with the reason and the note", func() {
			controllers.RecordWarningf(recorder, classifier, eventreasons.ClassifierFailed,
				eventreasons.ActionReconciling, "failed to do %s", "something")

			Expect(recorder.Events).To(Receive(Equal("Warning ClassifierFailed failed to do something")))
		})

		It("does not interpret a percent sign in the message", func() {
			controllers.RecordWarningf(recorder, classifier, eventreasons.ClassifierFailed,
				eventreasons.ActionReconciling, "%s", "disk usage 100%d")

			Expect(recorder.Events).To(Receive(Equal("Warning ClassifierFailed disk usage 100%d")))
		})

		It("caps a note longer than the API server accepts", func() {
			longMessage := strings.Repeat("a", eventreasons.NoteMaxLength*2)
			controllers.RecordWarningf(recorder, classifier, eventreasons.ClassifierFailed,
				eventreasons.ActionReconciling, "%s", longMessage)

			var recorded string
			Expect(recorder.Events).To(Receive(&recorded))
			note := strings.TrimPrefix(recorded, "Warning ClassifierFailed ")
			Expect(len(note)).To(Equal(eventreasons.NoteMaxLength))
			Expect(note).To(HaveSuffix("...(truncated)"))
		})
	})

	Context("unmanagedLabelKeys", func() {
		It("returns the keys separated by a comma", func() {
			unmanaged := []libsveltosv1beta1.UnManagedLabel{{Key: "env"}, {Key: "region"}}
			Expect(controllers.UnmanagedLabelKeys(unmanaged)).To(Equal("env, region"))
		})
	})

	Context("recordReconcileFailure", func() {
		It("records a Warning ClassifierFailed event naming the step and the error", func() {
			controllers.RecordReconcileFailure(reconciler, classifier, "apply labels to matching clusters", errors.New("boom"))

			Expect(recorder.Events).To(Receive(Equal(
				"Warning ClassifierFailed failed to apply labels to matching clusters: boom")))
		})
	})

	Context("recordDeploymentFailure", func() {
		clusterInfo := func(status libsveltosv1beta1.SveltosFeatureStatus, failureMessage *string,
		) *libsveltosv1beta1.ClusterInfo {

			return &libsveltosv1beta1.ClusterInfo{Status: status, FailureMessage: failureMessage}
		}

		message := func(s string) *string { return &s }

		It("records ClassifierFailed when the deployment failed", func() {
			controllers.RecordDeploymentFailure(reconciler, classifier, cluster,
				clusterInfo(libsveltosv1beta1.SveltosStatusFailed, message("failed to deploy CRDs")))

			Expect(recorder.Events).To(Receive(Equal(
				"Warning ClassifierFailed cluster " + cluster.Namespace + "/" + cluster.Name + ": failed to deploy CRDs")))
		})

		It("records nothing when the agent of the cluster is not healthy: that is reported once per cluster", func() {
			controllers.RecordDeploymentFailure(reconciler, classifier, cluster,
				clusterInfo(libsveltosv1beta1.SveltosStatusFailedNonRetriable,
					message("agent in managed cluster is not healthy.")))

			Expect(recorder.Events).To(BeEmpty())
		})

		It("records nothing while the deployment is in progress, even with a message", func() {
			controllers.RecordDeploymentFailure(reconciler, classifier, cluster,
				clusterInfo(libsveltosv1beta1.SveltosStatusProvisioning, message("cleanup is in progress")))

			Expect(recorder.Events).To(BeEmpty())
		})

		It("records nothing when there is no failure message", func() {
			controllers.RecordDeploymentFailure(reconciler, classifier, cluster,
				clusterInfo(libsveltosv1beta1.SveltosStatusFailed, message("")))
			controllers.RecordDeploymentFailure(reconciler, classifier, cluster,
				clusterInfo(libsveltosv1beta1.SveltosStatusFailed, nil))

			Expect(recorder.Events).To(BeEmpty())
		})
	})
})

var _ = Describe("ManagementClusterClassifier events", func() {
	It("records a Warning ClassifierFailed event when the classification script cannot run", func() {
		mcc := &libsveltosv1beta1.ManagementClusterClassifier{
			ObjectMeta: metav1.ObjectMeta{Name: randomString()},
			Spec: libsveltosv1beta1.ManagementClusterClassifierSpec{
				ClassificationLua: "not valid lua !@#",
				ClassifierLabels: []libsveltosv1beta1.ClassifierLabel{
					{Key: testLabelEnv, Value: testValueProd},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(mgmtTestScheme()).
			WithObjects(mcc).
			Build()

		recorder := events.NewFakeRecorder(eventBufferSize)
		reconciler := &controllers.ManagementClusterClassifierReconciler{
			Client:        fakeClient,
			EventRecorder: recorder,
		}

		_, err := reconciler.Reconcile(context.TODO(),
			ctrl.Request{NamespacedName: types.NamespacedName{Name: mcc.Name}})
		Expect(err).ToNot(HaveOccurred())

		var recorded string
		Expect(recorder.Events).To(Receive(&recorded))
		Expect(recorded).To(HavePrefix("Warning ClassifierFailed failed to build cluster sets: "))
	})
})
