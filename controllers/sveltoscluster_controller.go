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

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// SveltosClusterReconciler reconciles a SveltosCluster object
type SveltosClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters/status,verbs=get;list;watch

func (r *SveltosClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling SveltosCluster")

	// Fecth the SveltosCluster instance
	sveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
	if err := r.Get(ctx, req.NamespacedName, sveltosCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return removeClassifierReportForDeletedCluster(ctx, r.Client, req.Namespace, req.Name,
				libsveltosv1alpha1.ClusterTypeSveltos, logger)
		}
		logger.Error(err, "Failed to fetch SveltosCluster")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch SveltosCluster %s",
			req.NamespacedName,
		)
	}

	// Handle deleted SveltosCluster
	if !sveltosCluster.DeletionTimestamp.IsZero() {
		return removeClassifierReportForDeletedCluster(ctx, r.Client, req.Namespace, req.Name,
			libsveltosv1alpha1.ClusterTypeSveltos, logger)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1alpha1.SveltosCluster{}).
		Complete(r)
}

func removeClassifierReportForDeletedCluster(ctx context.Context, c client.Client,
	clusterNamespace, cluserName string, clusterType libsveltosv1alpha1.ClusterType,
	logger logr.Logger) (ctrl.Result, error) {

	err := removeClusterClassifierReports(ctx, c, clusterNamespace, cluserName, clusterType, logger)
	if err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
