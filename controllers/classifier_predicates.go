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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// SveltosClusterPredicates predicates for sveltos Cluster. ClassifierReconciler watches sveltos Cluster events
// and react to those by reconciling itself based on following predicates
func SveltosClusterPredicates(logger logr.Logger) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newCluster := e.ObjectNew.(*libsveltosv1alpha1.SveltosCluster)
			oldCluster := e.ObjectOld.(*libsveltosv1alpha1.SveltosCluster)
			log := logger.WithValues("predicate", "updateEvent",
				"namespace", newCluster.Namespace,
				"cluster", newCluster.Name,
			)

			if oldCluster == nil {
				log.V(logs.LogVerbose).Info("Old Cluster is nil. Reconcile ClusterProfile")
				return true
			}

			// return true if Cluster.Spec.Paused has changed from true to false
			if oldCluster.Spec.Paused && !newCluster.Spec.Paused {
				log.V(logs.LogVerbose).Info(
					"Cluster was unpaused. Will attempt to reconcile associated ClusterProfiles.")
				return true
			}

			if !oldCluster.Status.Ready && newCluster.Status.Ready {
				log.V(logs.LogVerbose).Info(
					"Cluster was not ready. Will attempt to reconcile associated ClusterProfiles.")
				return true
			}

			if !reflect.DeepEqual(oldCluster.Labels, newCluster.Labels) {
				log.V(logs.LogVerbose).Info(
					"Cluster labels changed. Will attempt to reconcile associated ClusterProfiles.",
				)
				return true
			}

			if !reflect.DeepEqual(oldCluster.Labels, newCluster.Labels) {
				log.V(logs.LogVerbose).Info(
					"Cluster labels changed. Will attempt to reconcile associated Classifiers.",
				)
				return true
			}

			// otherwise, return false
			log.V(logs.LogVerbose).Info(
				"Cluster did not match expected conditions.  Will not attempt to reconcile associated ClusterProfiles.")
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			cluster := e.Object.(*libsveltosv1alpha1.SveltosCluster)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", cluster.Namespace,
				"cluster", cluster.Name,
			)

			// Only need to trigger a reconcile if the Cluster.Spec.Paused is false
			if !cluster.Spec.Paused {
				log.V(logs.LogVerbose).Info(
					"Cluster is not paused.  Will attempt to reconcile associated ClusterProfiles.",
				)
				return true
			}
			log.V(logs.LogVerbose).Info(
				"Cluster did not match expected conditions.  Will not attempt to reconcile associated ClusterProfiles.")
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log := logger.WithValues("predicate", "deleteEvent",
				"namespace", e.Object.GetNamespace(),
				"cluster", e.Object.GetName(),
			)
			log.V(logs.LogVerbose).Info(
				"Cluster deleted.  Will attempt to reconcile associated ClusterProfiles.")
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			log := logger.WithValues("predicate", "genericEvent",
				"namespace", e.Object.GetNamespace(),
				"cluster", e.Object.GetName(),
			)
			log.V(logs.LogVerbose).Info(
				"Cluster did not match expected conditions.  Will not attempt to reconcile associated ClusterProfiles.")
			return false
		},
	}
}

type ClusterPredicate struct {
	Logger logr.Logger
}

func (p ClusterPredicate) Create(obj event.TypedCreateEvent[*clusterv1.Cluster]) bool {
	cluster := obj.Object
	log := p.Logger.WithValues("predicate", "createEvent",
		"namespace", cluster.Namespace,
		"cluster", cluster.Name,
	)

	// Only need to trigger a reconcile if the Cluster.Spec.Paused is false
	if !cluster.Spec.Paused {
		log.V(logs.LogVerbose).Info(
			"Cluster is not paused.  Will attempt to reconcile associated Classifiers.",
		)
		return true
	}
	log.V(logs.LogVerbose).Info(
		"Cluster did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
	return false
}

func (p ClusterPredicate) Update(obj event.TypedUpdateEvent[*clusterv1.Cluster]) bool {
	newCluster := obj.ObjectNew
	oldCluster := obj.ObjectOld
	log := p.Logger.WithValues("predicate", "updateEvent",
		"namespace", newCluster.Namespace,
		"cluster", newCluster.Name,
	)

	if oldCluster == nil {
		log.V(logs.LogVerbose).Info("Old Cluster is nil. Reconcile Classifier")
		return true
	}

	// return true if Cluster.Spec.Paused has changed from true to false
	if oldCluster.Spec.Paused && !newCluster.Spec.Paused {
		log.V(logs.LogVerbose).Info(
			"Cluster was unpaused. Will attempt to reconcile associated Classifiers.")
		return true
	}

	if oldCluster.Status.ControlPlaneReady != newCluster.Status.ControlPlaneReady {
		log.V(logs.LogVerbose).Info(
			"Cluster ControlPlaneReady changed. Will attempt to reconcile associated Classifiers.",
		)
		return true
	}

	if oldCluster.Status.InfrastructureReady != newCluster.Status.InfrastructureReady {
		log.V(logs.LogVerbose).Info(
			"Cluster InfrastructureReady changed. Will attempt to reconcile associated Classifiers.",
		)
		return true
	}

	if !reflect.DeepEqual(oldCluster.Labels, newCluster.Labels) {
		log.V(logs.LogVerbose).Info(
			"Cluster labels changed. Will attempt to reconcile associated Classifiers.",
		)
		return true
	}

	// otherwise, return false
	log.V(logs.LogVerbose).Info(
		"Cluster did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
	return false
}

func (p ClusterPredicate) Delete(obj event.TypedDeleteEvent[*clusterv1.Cluster]) bool {
	log := p.Logger.WithValues("predicate", "deleteEvent",
		"namespace", obj.Object.GetNamespace(),
		"cluster", obj.Object.GetName(),
	)
	log.V(logs.LogVerbose).Info(
		"Cluster deleted.  Will attempt to reconcile associated Classifiers.")
	return true
}

func (p ClusterPredicate) Generic(obj event.TypedGenericEvent[*clusterv1.Cluster]) bool {
	log := p.Logger.WithValues("predicate", "genericEvent",
		"namespace", obj.Object.GetNamespace(),
		"cluster", obj.Object.GetName(),
	)
	log.V(logs.LogVerbose).Info(
		"Cluster did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
	return false
}

type MachinePredicate struct {
	Logger logr.Logger
}

func (p MachinePredicate) Create(obj event.TypedCreateEvent[*clusterv1.Machine]) bool {
	machine := obj.Object
	log := p.Logger.WithValues("predicate", "createEvent",
		"namespace", machine.Namespace,
		"machine", machine.Name,
	)

	// React only to ControlPlane machine
	if !isControlPlaneMachine(machine) {
		return false
	}

	// Only need to trigger a reconcile if the Machine.Status.Phase is Running
	if machine.Status.GetTypedPhase() == clusterv1.MachinePhaseRunning {
		return true
	}

	log.V(logs.LogVerbose).Info(
		"Machine did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
	return false
}

func (p MachinePredicate) Update(obj event.TypedUpdateEvent[*clusterv1.Machine]) bool {
	newMachine := obj.ObjectNew
	oldMachine := obj.ObjectOld
	log := p.Logger.WithValues("predicate", "updateEvent",
		"namespace", newMachine.Namespace,
		"machine", newMachine.Name,
	)

	// React only to ControlPlane machine
	if !isControlPlaneMachine(newMachine) {
		return false
	}

	if newMachine.Status.GetTypedPhase() != clusterv1.MachinePhaseRunning {
		return false
	}

	if oldMachine == nil {
		log.V(logs.LogVerbose).Info("Old Machine is nil. Reconcile Classifier")
		return true
	}

	// return true if Machine.Status.Phase has changed from not running to running
	if oldMachine.Status.GetTypedPhase() != newMachine.Status.GetTypedPhase() {
		log.V(logs.LogVerbose).Info(
			"Machine was not in Running Phase. Will attempt to reconcile associated Classifiers.")
		return true
	}

	// otherwise, return false
	log.V(logs.LogVerbose).Info(
		"Machine did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
	return false
}

func (p MachinePredicate) Delete(obj event.TypedDeleteEvent[*clusterv1.Machine]) bool {
	log := p.Logger.WithValues("predicate", "deleteEvent",
		"namespace", obj.Object.GetNamespace(),
		"machine", obj.Object.GetName(),
	)
	log.V(logs.LogVerbose).Info(
		"Machine did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
	return false
}

func (p MachinePredicate) Generic(obj event.TypedGenericEvent[*clusterv1.Machine]) bool {
	log := p.Logger.WithValues("predicate", "genericEvent",
		"namespace", obj.Object.GetNamespace(),
		"machine", obj.Object.GetName(),
	)
	log.V(logs.LogVerbose).Info(
		"Machine did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
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

			if _, ok := newSecret.Labels[libsveltosv1alpha1.AccessRequestNameLabel]; !ok {
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

			if _, ok := secret.Labels[libsveltosv1alpha1.AccessRequestNameLabel]; !ok {
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

// ClassifierReportPredicate predicates for ClassifierReport. ClassifierReconciler watches ClassifierReport events
// and react to those by reconciling itself based on following predicates
func ClassifierReportPredicate(logger logr.Logger) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newReport := e.ObjectNew.(*libsveltosv1alpha1.ClassifierReport)
			oldReport := e.ObjectOld.(*libsveltosv1alpha1.ClassifierReport)
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
			report := e.Object.(*libsveltosv1alpha1.ClassifierReport)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", report.Namespace,
				"name", report.Name,
			)

			log.V(logs.LogVerbose).Info(
				"ClassifierReport did match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			report := e.Object.(*libsveltosv1alpha1.ClassifierReport)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", report.Namespace,
				"name", report.Name,
			)

			log.V(logs.LogVerbose).Info(
				"ClassifierReport did match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			report := e.Object.(*libsveltosv1alpha1.ClassifierReport)
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

// ClassifierPredicate predicates for Classifier. ClassifierReconciler watches Classifier events
// and react to those by reconciling itself based on following predicates
func ClassifierPredicate(logger logr.Logger) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			newClassifer := e.ObjectNew.(*libsveltosv1alpha1.Classifier)
			oldClassifier := e.ObjectOld.(*libsveltosv1alpha1.Classifier)
			log := logger.WithValues("predicate", "updateEvent",
				"name", newClassifer.Name,
			)

			if oldClassifier == nil {
				log.V(logs.LogVerbose).Info("Old Classifier is nil. Reconcile Classifier")
				return true
			}

			// return true if Classifier.Status has changed
			if !reflect.DeepEqual(oldClassifier.Status.MachingClusterStatuses, newClassifer.Status.MachingClusterStatuses) {
				log.V(logs.LogVerbose).Info(
					"Classifier Status.MachingClusterStatuses changed. Will attempt to reconcile associated Classifiers.")
				return true
			}

			// otherwise, return false
			log.V(logs.LogVerbose).Info(
				"ClassifierReport did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			classifier := e.Object.(*libsveltosv1alpha1.Classifier)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", classifier.Namespace,
				"name", classifier.Name,
			)

			log.V(logs.LogVerbose).Info(
				"Classifier did not match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			classifier := e.Object.(*libsveltosv1alpha1.Classifier)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", classifier.Namespace,
				"name", classifier.Name,
			)

			log.V(logs.LogVerbose).Info(
				"Classifier did match expected conditions.  Will attempt to reconcile associated Classifiers.")
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			classifier := e.Object.(*libsveltosv1alpha1.Classifier)
			log := logger.WithValues("predicate", "createEvent",
				"namespace", classifier.Namespace,
				"name", classifier.Name,
			)

			log.V(logs.LogVerbose).Info(
				"Classifier did not match expected conditions.  Will not attempt to reconcile associated Classifiers.")
			return false
		},
	}
}

func isControlPlaneMachine(machine *clusterv1.Machine) bool {
	// React only to ControlPlane machine
	if machine.Labels == nil {
		return false
	}

	if _, ok := machine.Labels[clusterv1.MachineControlPlaneLabel]; !ok {
		return false
	}

	return true
}
