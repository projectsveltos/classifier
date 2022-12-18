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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2/klogr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

func (r *ClassifierReconciler) requeueClassifierForCluster(
	o client.Object,
) []reconcile.Request {

	cluster := o.(*clusterv1.Cluster)
	logger := klogr.New().WithValues(
		"objectMapper",
		"requeueClassifierForCluster",
		"namespace",
		cluster.Namespace,
		"cluster",
		cluster.Name,
	)

	logger.V(logs.LogDebug).Info("reacting to CAPI Cluster change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	clusterInfo := corev1.ObjectReference{
		Kind:       cluster.Kind,
		Namespace:  cluster.Namespace,
		Name:       cluster.Name,
		APIVersion: cluster.APIVersion,
	}

	// Get all Classifiers previously matching this cluster and reconcile those
	requests := make([]ctrl.Request, r.getClusterMapForEntry(&clusterInfo).Len())
	consumers := r.getClusterMapForEntry(&clusterInfo).Items()

	for i := range consumers {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("requeuing classifier %s", consumers[i].Name))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: consumers[i].Name,
			},
		}
	}

	return requests
}

func (r *ClassifierReconciler) requeueClassifierForMachine(
	o client.Object,
) []reconcile.Request {

	machine := o.(*clusterv1.Machine)
	logger := klogr.New().WithValues(
		"objectMapper",
		"requeueClassifierForMachine",
		"namespace",
		machine.Namespace,
		"cluster",
		machine.Name,
	)

	clusterLabelName, ok := machine.Labels[clusterv1.ClusterLabelName]
	if !ok {
		logger.V(logs.LogDebug).Info("Machine has not ClusterLabelName")
		return nil
	}

	r.Mux.Lock()
	defer r.Mux.Unlock()

	clusterInfo := corev1.ObjectReference{
		Kind:       "Cluster",
		Namespace:  machine.Namespace,
		Name:       clusterLabelName,
		APIVersion: clusterv1.GroupVersion.String(),
	}

	// Get all Classifiers previously matching this cluster and reconcile those
	requests := make([]ctrl.Request, r.getClusterMapForEntry(&clusterInfo).Len())
	consumers := r.getClusterMapForEntry(&clusterInfo).Items()

	for i := range consumers {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("requeuing classifier %s", consumers[i].Name))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: consumers[i].Name,
			},
		}
	}

	return requests
}

func (r *ClassifierReconciler) requeueClassifierForSecret(
	o client.Object,
) []reconcile.Request {

	secret := o.(*corev1.Secret)
	logger := klogr.New().WithValues(
		"objectMapper",
		"requeueClassifierForSecret",
		"namespace",
		secret.Namespace,
		"secret",
		secret.Name,
	)

	logger.V(logs.LogDebug).Info("reacting to Secret change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	if secret.Labels == nil {
		return nil
	}
	if _, ok := secret.Labels[libsveltosv1alpha1.AccessRequestLabelName]; !ok {
		return nil
	}

	requests := make([]ctrl.Request, r.AllClassifierSet.Len())
	classifiers := r.AllClassifierSet.Items()
	for i := range classifiers {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("queuing classifier %s", classifiers[i].Name))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: classifiers[i].Name,
			},
		}
	}

	return requests
}

func (r *ClassifierReconciler) requeueClassifierForClassifierReport(
	o client.Object,
) []reconcile.Request {

	report := o.(*libsveltosv1alpha1.ClassifierReport)
	logger := klogr.New().WithValues(
		"objectMapper",
		"requeueClassifierForClassifierReport",
		"namespace",
		report.Namespace,
		"cluster",
		report.Name,
	)

	logger.V(logs.LogDebug).Info("reacting to ClassifierReport change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	requests := make([]ctrl.Request, 1)

	requests[0] = ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name: report.Spec.ClassifierName,
		},
	}

	return requests
}

func (r *ClassifierReconciler) requeueClassifierForClassifier(
	o client.Object,
) []reconcile.Request {

	classifier := o.(*libsveltosv1alpha1.Classifier)
	logger := klogr.New().WithValues(
		"objectMapper",
		"requeueClassifierForClassifier",
		"classifier",
		classifier.Name,
	)

	logger.V(logs.LogDebug).Info("reacting to Classifier change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	// Get all Classifier with at least one conflict
	requests := make([]ctrl.Request, r.ClassifierSet.Len())

	classifierWithConflicts := r.ClassifierSet.Items()

	for i := range classifierWithConflicts {
		cName := classifierWithConflicts[i].Name

		if cName == classifier.Name {
			continue
		}

		logger.V(logs.LogDebug).Info(fmt.Sprintf("queing %s for reconciliation", cName))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: cName,
			},
		}
	}

	return requests
}
