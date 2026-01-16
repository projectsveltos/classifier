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

package controllers

import (
	"reflect"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// ClassifierPredicate is a custom predicate that filters ClusterSummary s events
type ClassifierPredicate struct {
	Logger logr.Logger
}

func (p ClassifierPredicate) Create(e event.CreateEvent) bool {
	// Always reconcile on creation
	return true
}

func (p ClassifierPredicate) Update(e event.UpdateEvent) bool {
	newClassifier := e.ObjectNew.(*libsveltosv1beta1.Classifier)
	oldClassifier := e.ObjectOld.(*libsveltosv1beta1.Classifier)
	log := p.Logger.WithValues("predicate", "updateClassifier",
		"classifier", newClassifier.Name,
	)

	if oldClassifier == nil {
		log.V(logs.LogVerbose).Info("Old Classifier is nil. Reconcile Classifier.")
		return true
	}

	if !reflect.DeepEqual(oldClassifier.DeletionTimestamp, newClassifier.DeletionTimestamp) {
		return true
	}

	if !reflect.DeepEqual(oldClassifier.Spec, newClassifier.Spec) {
		log.V(logs.LogVerbose).Info(
			"Classifier Spec changed. Will attempt to reconcile ClusterSummary.",
		)
		return true
	}

	return false
}

func (p ClassifierPredicate) Delete(e event.DeleteEvent) bool {
	// Always reconcile on deletion
	return true
}

func (p ClassifierPredicate) Generic(e event.GenericEvent) bool {
	// Ignore generic
	return false
}

// SecretPredicates predicates for Secret. ClassifierReconciler watches Secret events
// and react to those by reconciling itself based on following predicates
func SecretPredicates(logger logr.Logger) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newSecret := e.ObjectNew.(*corev1.Secret)
			oldSecret := e.ObjectOld.(*corev1.Secret)
			log := logger.WithValues("predicate", "updateEvent",
				"namespace", newSecret.Namespace,
				"secret", newSecret.Name,
			)

			if newSecret.Labels == nil {
				log.V(logs.LogVerbose).Info("Secret with no label.  Will not attempt to reconcile associated Classifiers.")
				return false
			}

			if _, ok := newSecret.Labels[libsveltosv1beta1.AccessRequestNameLabel]; !ok {
				log.V(logs.LogVerbose).Info("Secret with no AccessRequestLabelName.  Will not attempt to reconcile associated Classifiers.")
				return false
			}

			if oldSecret == nil {
				log.V(logs.LogVerbose).Info("Old Secret is nil. Reconcile Classifier")
				return true
			}

			// return true if Data has changed
			if !reflect.DeepEqual(oldSecret.Data, newSecret.Data) {
				log.V(logs.LogVerbose).Info(
					"Secret Data changed. Will attempt to reconcile associated Classifiers.")
				return true
			}

			// otherwise, return false
			log.V(logs.LogVerbose).Info(
				"Secret did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			secret := e.Object.(*corev1.Secret)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", secret.Namespace,
				"cluster", secret.Name,
			)

			if secret.Labels == nil {
				log.V(logs.LogVerbose).Info("Secret with no label.  Will not attempt to reconcile associated Classifiers.")
				return false
			}

			if _, ok := secret.Labels[libsveltosv1beta1.AccessRequestNameLabel]; !ok {
				log.V(logs.LogVerbose).Info("Secret with no AccessRequestLabelName.  Will not attempt to reconcile associated Classifiers.")
				return false
			}

			log.V(logs.LogVerbose).Info(
				"Secret did match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log := logger.WithValues("predicate", "deleteEvent",
				"namespace", e.Object.GetNamespace(),
				"secret", e.Object.GetName(),
			)
			log.V(logs.LogVerbose).Info(
				"Secret did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			log := logger.WithValues("predicate", "genericEvent",
				"namespace", e.Object.GetNamespace(),
				"secret", e.Object.GetName(),
			)
			log.V(logs.LogVerbose).Info(
				"Secret did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
	}
}

// ConfigMapPredicates predicates for ConfigMap. ClassifierReconciler watches ConfigMap events
// and react to those by reconciling itself based on following predicates
func ConfigMapPredicates(logger logr.Logger) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newConfigMap := e.ObjectNew.(*corev1.ConfigMap)
			oldConfigMap := e.ObjectOld.(*corev1.ConfigMap)
			log := logger.WithValues("predicate", "updateEvent",
				"namespace", newConfigMap.Namespace,
				"configMap", newConfigMap.Name,
			)

			if newConfigMap.Namespace != projectsveltos || newConfigMap.Name != getSveltosAgentConfigMap() {
				return false
			}

			if oldConfigMap == nil {
				log.V(logs.LogVerbose).Info("Old ConfigMap is nil. Reconcile Classifier")
				return true
			}

			// return true if Data has changed
			if !reflect.DeepEqual(oldConfigMap.Data, newConfigMap.Data) {
				log.V(logs.LogVerbose).Info(
					"ConfigMap Data changed. Will attempt to reconcile associated Classifiers.")
				return true
			}

			// otherwise, return false
			log.V(logs.LogVerbose).Info(
				"ConfigMap did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			configMap := e.Object.(*corev1.ConfigMap)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", configMap.Namespace,
				"configMap", configMap.Name,
			)

			if configMap.Namespace == projectsveltos && configMap.Name == getSveltosAgentConfigMap() {
				log.V(logs.LogVerbose).Info("ConfigMap created. Will attempt to reconcile associated Classifiers.")
				return true
			}

			log.V(logs.LogVerbose).Info(
				"ConfigMap did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			configMap := e.Object.(*corev1.ConfigMap)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", configMap.Namespace,
				"configMap", configMap.Name,
			)

			if configMap.Namespace == projectsveltos && configMap.Name == getSveltosAgentConfigMap() {
				log.V(logs.LogVerbose).Info("ConfigMap deleted. Will attempt to reconcile associated Classifiers.")
				return true
			}

			log.V(logs.LogVerbose).Info(
				"ConfigMap did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			log := logger.WithValues("predicate", "genericEvent",
				"namespace", e.Object.GetNamespace(),
				"configMap", e.Object.GetName(),
			)
			log.V(logs.LogVerbose).Info(
				"ConfigMap did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
	}
}

// ClassifierReportPredicate predicates for ClassifierReport. ClassifierReconciler watches ClassifierReport events
// and react to those by reconciling itself based on following predicates
func ClassifierReportPredicate(logger logr.Logger) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newReport := e.ObjectNew.(*libsveltosv1beta1.ClassifierReport)
			oldReport := e.ObjectOld.(*libsveltosv1beta1.ClassifierReport)
			log := logger.WithValues("predicate", "updateEvent",
				"namespace", newReport.Namespace,
				"name", newReport.Name,
			)

			if oldReport == nil {
				log.V(logs.LogVerbose).Info("Old ClassifierReport is nil. Reconcile Classifier")
				return true
			}

			// return true if ClassifierReport.Spec.Match has changed
			if oldReport.Spec.Match != newReport.Spec.Match {
				log.V(logs.LogVerbose).Info(
					"Cluster was unpaused. Will attempt to reconcile associated Classifiers.")
				return true
			}

			// otherwise, return false
			log.V(logs.LogVerbose).Info(
				"ClassifierReport did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			report := e.Object.(*libsveltosv1beta1.ClassifierReport)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", report.Namespace,
				"name", report.Name,
			)

			log.V(logs.LogVerbose).Info(
				"ClassifierReport did match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			report := e.Object.(*libsveltosv1beta1.ClassifierReport)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", report.Namespace,
				"name", report.Name,
			)

			log.V(logs.LogVerbose).Info(
				"ClassifierReport did match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			report := e.Object.(*libsveltosv1beta1.ClassifierReport)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", report.Namespace,
				"name", report.Name,
			)

			log.V(logs.LogVerbose).Info(
				"ClassifierReport did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
	}
}
