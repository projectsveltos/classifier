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
	"reflect"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/event"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// ManagementClusterClassifierPredicate filters ManagementClusterClassifier events.
type ManagementClusterClassifierPredicate struct {
	Logger logr.Logger
}

func (p ManagementClusterClassifierPredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p ManagementClusterClassifierPredicate) Update(e event.UpdateEvent) bool {
	newMCC := e.ObjectNew.(*libsveltosv1beta1.ManagementClusterClassifier)
	oldMCC := e.ObjectOld.(*libsveltosv1beta1.ManagementClusterClassifier)
	log := p.Logger.WithValues("predicate", "updateManagementClusterClassifier",
		"managementclusterclassifier", newMCC.Name,
	)

	if oldMCC == nil {
		log.V(logs.LogVerbose).Info("Old ManagementClusterClassifier is nil. Reconcile.")
		return true
	}

	if !reflect.DeepEqual(oldMCC.DeletionTimestamp, newMCC.DeletionTimestamp) {
		return true
	}

	if !reflect.DeepEqual(oldMCC.Spec, newMCC.Spec) {
		log.V(logs.LogVerbose).Info("ManagementClusterClassifier Spec changed. Reconciling.")
		return true
	}

	return false
}

func (p ManagementClusterClassifierPredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p ManagementClusterClassifierPredicate) Generic(e event.GenericEvent) bool {
	return false
}
