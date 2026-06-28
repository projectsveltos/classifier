/*
Copyright 2024. projectsveltos.io. All rights reserved.

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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// requeueForResource is the TypedEnqueueRequestsFromMapFunc handler for dynamically watched
// management-cluster resources. It maps a changed resource to the set of
// ManagementClusterClassifiers that listed its GVK in spec.matchResources.
func (r *ManagementClusterClassifierReconciler) requeueForResource(
	ctx context.Context, o *unstructured.Unstructured,
) []reconcile.Request {

	gvk := schema.GroupVersionKind{
		Group:   o.GroupVersionKind().Group,
		Version: o.GroupVersionKind().Version,
		Kind:    o.GroupVersionKind().Kind,
	}

	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))).WithValues(
		"objectMapper", "requeueForResource",
		"gvk", gvk.String(),
		"namespace", o.GetNamespace(),
		"name", o.GetName(),
	)

	logger.V(logs.LogDebug).Info("reacting to management-cluster resource change")

	r.Mux.Lock()
	defer r.Mux.Unlock()

	classifierSet, ok := r.GVKToClassifiers[gvk]
	if !ok || classifierSet == nil || classifierSet.Len() == 0 {
		return nil
	}

	items := classifierSet.Items()
	requests := make([]ctrl.Request, 0, len(items))
	for i := range items {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("requeuing ManagementClusterClassifier %s", items[i].Name))
		requests = append(requests, ctrl.Request{
			NamespacedName: client.ObjectKey{Name: items[i].Name},
		})
	}
	return requests
}
