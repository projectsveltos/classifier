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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/textlogger"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

const (
	namespacePrefix = "predicates"
)

var _ = Describe("Classifier Predicates: SecretPredicates", func() {
	var logger logr.Logger
	var secret *corev1.Secret

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: namespacePrefix + randomString(),
			},
		}
	})

	It("Create reprocesses when label is present", func() {
		secretPredicate := controllers.SecretPredicates(logger)

		secret.Labels = map[string]string{
			libsveltosv1beta1.AccessRequestNameLabel: randomString(),
		}

		e := event.CreateEvent{
			Object: secret,
		}

		result := secretPredicate.Create(e)
		Expect(result).To(BeTrue())
	})

	It("Create does not reprocess when label is not present", func() {
		secretPredicate := controllers.SecretPredicates(logger)

		e := event.CreateEvent{
			Object: secret,
		}

		result := secretPredicate.Create(e)
		Expect(result).To(BeFalse())
	})

	It("Delete does not reprocess ", func() {
		secretPredicate := controllers.SecretPredicates(logger)

		e := event.DeleteEvent{
			Object: secret,
		}

		result := secretPredicate.Delete(e)
		Expect(result).To(BeFalse())
	})

	It("Update reprocesses when Secret Data has changed", func() {
		secretPredicate := controllers.SecretPredicates(logger)

		secret.Data = map[string][]byte{
			randomString(): []byte(randomString()),
		}
		secret.Labels = map[string]string{
			libsveltosv1beta1.AccessRequestNameLabel: randomString(),
		}

		oldSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secret.Name,
				Namespace: secret.Namespace,
				Labels:    secret.Labels,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: secret,
			ObjectOld: oldSecret,
		}

		result := secretPredicate.Update(e)
		Expect(result).To(BeTrue())
	})

	It("Update does not reprocess when Secret Data has not changed", func() {
		secretPredicate := controllers.SecretPredicates(logger)

		secret.Data = map[string][]byte{
			randomString(): []byte(randomString()),
		}
		secret.Labels = map[string]string{
			libsveltosv1beta1.AccessRequestNameLabel: randomString(),
		}

		oldSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secret.Name,
				Namespace: secret.Namespace,
				Labels:    secret.Labels,
			},
			Data: secret.Data,
		}

		e := event.UpdateEvent{
			ObjectNew: secret,
			ObjectOld: oldSecret,
		}

		result := secretPredicate.Update(e)
		Expect(result).To(BeFalse())
	})

	It("Update does not reprocess when Secret has no label", func() {
		secretPredicate := controllers.SecretPredicates(logger)

		secret.Data = map[string][]byte{
			randomString(): []byte(randomString()),
		}

		oldSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secret.Name,
				Namespace: secret.Namespace,
				Labels:    secret.Labels,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: secret,
			ObjectOld: oldSecret,
		}

		result := secretPredicate.Update(e)
		Expect(result).To(BeFalse())
	})
})

var _ = Describe("Classifier Predicates: ConfigMapPredicates", func() {
	var logger logr.Logger
	var configMap *corev1.ConfigMap

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))

		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "projectsveltos",
				Name:      randomString(),
			},
		}
	})

	AfterEach(func() {
		controllers.SetSveltosAgentConfigMap("")
	})

	It("Create reprocesses when ConfigMap is the one with SveltosAgent configuration", func() {
		name := randomString()
		controllers.SetSveltosAgentConfigMap(name)

		configMapPredicate := controllers.ConfigMapPredicates(logger)

		e := event.CreateEvent{
			Object: configMap,
		}

		result := configMapPredicate.Create(e)
		Expect(result).To(BeFalse())

		configMap.Name = name

		result = configMapPredicate.Create(e)
		Expect(result).To(BeTrue())
	})

	It("Delete reprocesses when ConfigMap is the one with SveltosAgent configuration", func() {
		name := randomString()
		controllers.SetSveltosAgentConfigMap(name)

		configMapPredicate := controllers.ConfigMapPredicates(logger)

		e := event.DeleteEvent{
			Object: configMap,
		}

		result := configMapPredicate.Delete(e)
		Expect(result).To(BeFalse())

		configMap.Name = name

		result = configMapPredicate.Delete(e)
		Expect(result).To(BeTrue())
	})

	It("Update reprocesses when ConfigMap Data has changed", func() {
		name := randomString()
		controllers.SetSveltosAgentConfigMap(name)
		configMap.Name = name

		configMapPredicate := controllers.ConfigMapPredicates(logger)

		configMap.Data = map[string]string{
			randomString(): randomString(),
		}

		oldConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: configMap,
			ObjectOld: oldConfigMap,
		}

		result := configMapPredicate.Update(e)
		Expect(result).To(BeTrue())
	})

	It("Update does not reprocess when ConfigMap Data has not changed", func() {
		name := randomString()
		controllers.SetSveltosAgentConfigMap(name)
		configMap.Name = name

		configMapPredicate := controllers.ConfigMapPredicates(logger)

		configMap.Data = map[string]string{
			randomString(): randomString(),
		}

		oldConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMap.Name,
				Namespace: configMap.Namespace,
			},
			Data: configMap.Data,
		}

		e := event.UpdateEvent{
			ObjectNew: configMap,
			ObjectOld: oldConfigMap,
		}

		result := configMapPredicate.Update(e)
		Expect(result).To(BeFalse())
	})
})

var _ = Describe("Classifier Predicates: ClassifierReportPredicate", func() {
	var logger logr.Logger
	var report *libsveltosv1beta1.ClassifierReport

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
		report = &libsveltosv1beta1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      upstreamClusterNamePrefix + randomString(),
				Namespace: namespacePrefix + randomString(),
			},
		}
	})

	It("Create reprocesses always", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		e := event.CreateEvent{
			Object: report,
		}

		result := reportPredicate.Create(e)
		Expect(result).To(BeTrue())
	})
	It("Delete does reprocess ", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		e := event.DeleteEvent{
			Object: report,
		}

		result := reportPredicate.Delete(e)
		Expect(result).To(BeTrue())
	})
	It("Update reprocesses when Spec.Match changes", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		report.Spec.Match = true

		oldReport := &libsveltosv1beta1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      report.Name,
				Namespace: report.Namespace,
			},
		}
		oldReport.Spec.Match = false

		e := event.UpdateEvent{
			ObjectNew: report,
			ObjectOld: oldReport,
		}

		result := reportPredicate.Update(e)
		Expect(result).To(BeTrue())
	})
	It("Update does not reprocess when Spec.Match does not change", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		report.Spec.Match = true
		report.Labels = map[string]string{randomString(): randomString()}

		oldReport := &libsveltosv1beta1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      report.Name,
				Namespace: report.Namespace,
			},
		}
		oldReport.Spec.Match = true

		e := event.UpdateEvent{
			ObjectNew: report,
			ObjectOld: oldReport,
		}

		result := reportPredicate.Update(e)
		Expect(result).To(BeFalse())
	})
})
