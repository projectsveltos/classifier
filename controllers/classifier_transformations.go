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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // SA1019: We are unable to update the dependency at this time.
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

func (r *ClassifierReconciler) requeueClassifierForSveltosCluster(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	return r.requeueClassifierForACluster(o)
}

func (r *ClassifierReconciler) requeueClassifierForCluster(
	ctx context.Context, cluster *clusterv1.Cluster,
) []reconcile.Request {

	return r.requeueClassifierForACluster(cluster)
}

func (r *ClassifierReconciler) requeueClassifierForACluster(o client.Object,
) []reconcile.Request {

	cluster := o
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"objectMapper",
		"requeueClassifierForCluster",
		"namespace",
		cluster.GetNamespace(),
		"cluster",
		cluster.GetName(),
	)

	logger.V(logs.LogDebug).Info("reacting to Cluster change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	// Get all existing classifiers
	classifiers := r.AllClassifierSet.Items()
	requests := make([]ctrl.Request, r.AllClassifierSet.Len())

	for i := range classifiers {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("requeuing classifier %s", classifiers[i].Name))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: classifiers[i].Name,
			},
		}
	}

	return requests
}

func (r *ClassifierReconciler) requeueClassifierForMachine(
	ctx context.Context, machine *clusterv1.Machine,
) []reconcile.Request {

	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"objectMapper",
		"requeueClassifierForMachine",
		"namespace",
		machine.Namespace,
		"cluster",
		machine.Name,
	)

	r.Mux.Lock()
	defer r.Mux.Unlock()

	// Get all existing classifiers
	classifiers := r.AllClassifierSet.Items()
	requests := make([]ctrl.Request, r.AllClassifierSet.Len())

	for i := range classifiers {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("requeuing classifier %s", classifiers[i].Name))
		requests[i] = ctrl.Request{
			NamespacedName: client.ObjectKey{
				Name: classifiers[i].Name,
			},
		}
	}

	return requests
}

func (r *ClassifierReconciler) requeueClassifierForSecret(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	secret := o.(*corev1.Secret)
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
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
	if _, ok := secret.Labels[libsveltosv1beta1.AccessRequestNameLabel]; !ok {
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

func (r *ClassifierReconciler) requeueClassifierForConfigMap(
	ctx context.Context, o client.Object,
) []reconcile.Request {

	configMap := o.(*corev1.ConfigMap)
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"objectMapper",
		"requeueClassifierForConfigMap",
		"namespace",
		configMap.Namespace,
		"configMap",
		configMap.Name,
	)

	logger.V(logs.LogDebug).Info("reacting to configMap change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	if configMap.Namespace != projectsveltos || configMap.Name != getSveltosAgentConfigMap() {
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
	ctx context.Context, o client.Object,
) []reconcile.Request {

	report := o.(*libsveltosv1beta1.ClassifierReport)
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
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
	ctx context.Context, o client.Object,
) []reconcile.Request {

	classifier := o.(*libsveltosv1beta1.Classifier)
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
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
