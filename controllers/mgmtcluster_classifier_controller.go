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
	"strings"
	"sync"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/projectsveltos/classifier/controllers/keymanager"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

// ManagementClusterClassifierReconciler reconciles ManagementClusterClassifier objects.
// Unlike the regular Classifier, it evaluates resources on the management cluster itself —
// no deployment to managed clusters is needed.
type ManagementClusterClassifierReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Mux guards GVKToClassifiers and other in-memory state.
	Mux sync.Mutex

	// GVKToClassifiers maps each GVK from spec.matchResources to the set of
	// ManagementClusterClassifier names (as corev1.ObjectReference) that reference it.
	// Used by requeueForResource to find which classifiers to requeue when a resource changes.
	GVKToClassifiers map[schema.GroupVersionKind]*libsveltosset.Set

	// controller and mgr are stored after SetupWithManager to allow dynamic watches.
	controller controller.Controller
	mgr        ctrl.Manager

	// watchedGVKs tracks which GVKs already have a watch registered.
	watchedGVKs    map[schema.GroupVersionKind]bool
	watchedGVKsMux sync.RWMutex

	Logger logr.Logger
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=managementclusterclassifiers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=managementclusterclassifiers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=managementclusterclassifiers/finalizers,verbs=update
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=managementclusterclassifierreports,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=managementclusterclassifierreports/status,verbs=get;update;patch

func (r *ManagementClusterClassifierReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogDebug).Info("Reconciling")

	mcc := &libsveltosv1beta1.ManagementClusterClassifier{}
	if err := r.Get(ctx, req.NamespacedName, mcc); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("failed to fetch ManagementClusterClassifier %s: %w", req.Name, err)
	}

	logger = logger.WithValues("managementclusterclassifier", mcc.Name)

	if !mcc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mcc, logger)
	}
	return r.reconcileNormal(ctx, mcc, logger)
}

func (r *ManagementClusterClassifierReconciler) reconcileNormal(
	ctx context.Context, mcc *libsveltosv1beta1.ManagementClusterClassifier, logger logr.Logger,
) (reconcile.Result, error) {

	logger.V(logs.LogDebug).Info("Reconciling ManagementClusterClassifier")

	if !controllerutil.ContainsFinalizer(mcc, libsveltosv1beta1.ManagementClusterClassifierFinalizer) {
		patch := client.MergeFrom(mcc.DeepCopy())
		controllerutil.AddFinalizer(mcc, libsveltosv1beta1.ManagementClusterClassifierFinalizer)
		if err := r.Patch(ctx, mcc, patch); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to add finalizer: %v", err))
			return reconcile.Result{}, err
		}
	}

	// Build the sets of previously managed and newly matched clusters.
	oldClusters, newClusters, luaFailures, err := r.buildClusterSets(ctx, mcc, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to build cluster sets")
		_ = setMgmtClassifierFailureMessage(ctx, r.Client, mcc, err.Error())
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}
	trackMgmtClusterMatchingClusters(mcc.Name, len(newClusters), logger)

	km, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get keymanager")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	fakeClassifier := mgmtClassifierAsClassifier(mcc)

	// Remove labels from clusters that are no longer matched and delete their reports.
	if err := r.removeStaleClusterLabels(ctx, fakeClassifier, mcc.Name, oldClusters, newClusters, logger); err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to remove stale cluster labels")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	conflictCount := 0

	// Apply labels to newly matched clusters.
	for key, ref := range newClusters {
		clusterType, err := clusterTypeFromKind(ref.Kind)
		if err != nil {
			continue
		}

		if err := ensureMgmtClassifierReport(ctx, r.Client, mcc.Name, ref.Namespace, ref.Name, clusterType); err != nil {
			logger.V(logs.LogInfo).Error(err, fmt.Sprintf("failed to ensure report for cluster %s", key))
			continue
		}

		km.RemoveStaleRegistrations(fakeClassifier, ref.Namespace, ref.Name, clusterType)
		km.RegisterClassifierForLabels(fakeClassifier, ref.Namespace, ref.Name, clusterType)

		managed, unmanaged := r.classifyMgmtLabels(fakeClassifier, ref.Namespace, ref.Name, clusterType,
			mcc.Spec.ClassifierLabels, km)

		if len(unmanaged) > 0 {
			conflictCount++
		}

		if err := updateMgmtClassifierReportStatus(ctx, r.Client, mcc.Name, ref.Namespace, ref.Name,
			clusterType, managed, unmanaged); err != nil {
			logger.V(logs.LogInfo).Error(err, fmt.Sprintf("failed to update report status for cluster %s", key))
		}

		labelsToApply := r.filterManagedLabels(mcc.Spec.ClassifierLabels, managed)
		if err := applyLabelsToCluster(ctx, r.Client, ref.Namespace, ref.Name, clusterType,
			labelsToApply, logger); err != nil {
			if !apierrors.IsNotFound(err) {
				logger.V(logs.LogInfo).Error(err, fmt.Sprintf("failed to apply labels to cluster %s", key))
			}
		}
	}
	trackMgmtClusterLabelConflicts(mcc.Name, conflictCount, logger)

	// Update failure message: Lua errors plus any recorded conflicts.
	failMsg := strings.Join(luaFailures, "; ")
	if err := setMgmtClassifierFailureMessage(ctx, r.Client, mcc, failMsg); err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to update failure message")
	}

	// Sync GVK map and register dynamic watches for spec.matchResources.
	r.syncGVKWatches(mcc, logger)

	if conflictCount > 0 {
		return reconcile.Result{Requeue: true, RequeueAfter: conflictRequeueAfter}, nil
	}

	logger.V(logs.LogDebug).Info("Reconcile success")
	return reconcile.Result{}, nil
}

func (r *ManagementClusterClassifierReconciler) reconcileDelete(
	ctx context.Context, mcc *libsveltosv1beta1.ManagementClusterClassifier, logger logr.Logger,
) (reconcile.Result, error) {

	logger.V(logs.LogDebug).Info("Reconciling ManagementClusterClassifier delete")

	existingReports, err := listMgmtClassifierReports(ctx, r.Client, mcc.Name)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to list reports")
		return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
	}

	km, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get keymanager")
		return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
	}

	fakeClassifier := mgmtClassifierAsClassifier(mcc)

	for i := range existingReports {
		report := &existingReports[i]
		clusterType := report.Spec.ClusterType

		// Use the in-memory keymanager rather than report.Status.ManagedLabels: the Status
		// is read from the cache and can be stale (e.g. after a report was re-created in the
		// same reconcile cycle). The keymanager is always authoritative.
		keysToRemove := make([]string, 0, len(mcc.Spec.ClassifierLabels))
		for _, cl := range mcc.Spec.ClassifierLabels {
			mgr, err := km.GetManagerForKey(report.Spec.ClusterNamespace, report.Spec.ClusterName, cl.Key, clusterType)
			if err == nil && mgr == fakeClassifier.Name {
				keysToRemove = append(keysToRemove, cl.Key)
			}
		}

		if len(keysToRemove) > 0 {
			if err := removeLabelsFromCluster(ctx, r.Client,
				report.Spec.ClusterNamespace, report.Spec.ClusterName, clusterType,
				keysToRemove, logger); err != nil {
				logger.V(logs.LogInfo).Error(err, fmt.Sprintf("failed to remove labels from cluster %s/%s",
					report.Spec.ClusterNamespace, report.Spec.ClusterName))
				return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
			}
		}

		km.RemoveAllRegistrations(fakeClassifier, report.Spec.ClusterNamespace, report.Spec.ClusterName, clusterType)

		if err := r.Delete(ctx, report); err != nil && !apierrors.IsNotFound(err) {
			logger.V(logs.LogInfo).Error(err, "failed to delete report")
			return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
		}
	}

	r.removeFromGVKMap(mcc)

	patch := client.MergeFrom(mcc.DeepCopy())
	controllerutil.RemoveFinalizer(mcc, libsveltosv1beta1.ManagementClusterClassifierFinalizer)
	if err := r.Patch(ctx, mcc, patch); err != nil {
		return reconcile.Result{}, err
	}

	logger.V(logs.LogDebug).Info("Reconcile delete success")
	return reconcile.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *ManagementClusterClassifierReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, logger logr.Logger) (controller.Controller, error) {

	r.mgr = mgr
	r.watchedGVKs = make(map[schema.GroupVersionKind]bool)

	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1beta1.ManagementClusterClassifier{},
			builder.WithPredicates(
				ManagementClusterClassifierPredicate{
					Logger: mgr.GetLogger().WithValues("predicate", "managementclusterclassifierpredicate"),
				},
			),
		).
		Build(r)
	if err != nil {
		return nil, fmt.Errorf("error creating ManagementClusterClassifier controller: %w", err)
	}

	r.controller = c
	return c, nil
}

// buildClusterSets returns the set of previously managed clusters (by report) and the set of
// newly matched clusters (from classificationLua), plus any Lua validation failures.
func (r *ManagementClusterClassifierReconciler) buildClusterSets(
	ctx context.Context, mcc *libsveltosv1beta1.ManagementClusterClassifier, logger logr.Logger,
) (oldClusters map[string]libsveltosv1beta1.ManagementClusterClassifierReport,
	newClusters map[string]clusterRef,
	luaFailures []string,
	err error) {

	allResources, err := r.collectResources(ctx, mcc, logger)
	if err != nil {
		return nil, nil, nil, err
	}

	var newClusterRefs []clusterRef
	newClusterRefs, luaFailures, err = r.evaluateLua(mcc, allResources, logger)
	if err != nil {
		return nil, nil, nil, err
	}

	existingReports, err := listMgmtClassifierReports(ctx, r.Client, mcc.Name)
	if err != nil {
		return nil, nil, nil, err
	}

	oldClusters = make(map[string]libsveltosv1beta1.ManagementClusterClassifierReport, len(existingReports))
	for i := range existingReports {
		rp := existingReports[i]
		key := mgmtClusterKey(rp.Spec.ClusterNamespace, rp.Spec.ClusterName, rp.Spec.ClusterType)
		oldClusters[key] = rp
	}

	newClusters = make(map[string]clusterRef, len(newClusterRefs))
	for _, ref := range newClusterRefs {
		ct, _ := clusterTypeFromKind(ref.Kind)
		key := mgmtClusterKey(ref.Namespace, ref.Name, ct)
		newClusters[key] = ref
	}

	return oldClusters, newClusters, luaFailures, nil
}

// collectResources fetches all resources matching spec.matchResources from the management cluster.
func (r *ManagementClusterClassifierReconciler) collectResources(
	ctx context.Context, mcc *libsveltosv1beta1.ManagementClusterClassifier, logger logr.Logger,
) ([]*unstructured.Unstructured, error) {

	var all []*unstructured.Unstructured
	for i := range mcc.Spec.MatchResources {
		resources, err := fetchResourcesForSelector(ctx, r.Client, &mcc.Spec.MatchResources[i], logger)
		if err != nil {
			return nil, fmt.Errorf("failed to list resources for selector %d: %w", i, err)
		}
		all = append(all, resources...)
	}
	return all, nil
}

// evaluateLua runs classificationLua and returns the valid cluster refs plus any per-entry failures.
func (r *ManagementClusterClassifierReconciler) evaluateLua(
	mcc *libsveltosv1beta1.ManagementClusterClassifier,
	resources []*unstructured.Unstructured, logger logr.Logger,
) ([]clusterRef, []string, error) {

	rawRefs, err := runClassificationLua(mcc.Spec.ClassificationLua, resources, logger)
	if err != nil {
		return nil, nil, err
	}

	valid := make([]clusterRef, 0, len(rawRefs))
	var failures []string
	for _, ref := range rawRefs {
		if _, err := clusterTypeFromKind(ref.Kind); err != nil {
			msg := fmt.Sprintf("classificationLua returned invalid kind for cluster %s/%s: %v",
				ref.Namespace, ref.Name, err)
			logger.V(logs.LogInfo).Info(msg)
			failures = append(failures, msg)
			continue
		}
		valid = append(valid, ref)
	}
	return valid, failures, nil
}

// removeStaleClusterLabels removes labels from clusters no longer matched, removes keymanager
// registrations, and deletes the stale reports.
func (r *ManagementClusterClassifierReconciler) removeStaleClusterLabels(
	ctx context.Context,
	fakeClassifier *libsveltosv1beta1.Classifier,
	classifierName string,
	oldClusters map[string]libsveltosv1beta1.ManagementClusterClassifierReport,
	newClusters map[string]clusterRef,
	logger logr.Logger,
) error {

	km, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		return err
	}

	for key := range oldClusters {
		if _, ok := newClusters[key]; ok {
			continue
		}

		report := oldClusters[key]
		ct := report.Spec.ClusterType

		if len(report.Status.ManagedLabels) > 0 {
			if err := removeLabelsFromCluster(ctx, r.Client,
				report.Spec.ClusterNamespace, report.Spec.ClusterName, ct,
				report.Status.ManagedLabels, logger); err != nil {
				logger.V(logs.LogInfo).Error(err, fmt.Sprintf("failed to remove labels from cluster %s/%s",
					report.Spec.ClusterNamespace, report.Spec.ClusterName))
				return err
			}
		}

		km.RemoveAllRegistrations(fakeClassifier, report.Spec.ClusterNamespace, report.Spec.ClusterName, ct)

		if err := deleteMgmtClassifierReport(ctx, r.Client, classifierName,
			report.Spec.ClusterNamespace, report.Spec.ClusterName, ct); err != nil {
			logger.V(logs.LogInfo).Error(err, "failed to delete stale report")
			return err
		}
	}
	return nil
}

// classifyMgmtLabels divides desired labels into managed and unmanaged via the keymanager.
func (r *ManagementClusterClassifierReconciler) classifyMgmtLabels(
	fakeClassifier *libsveltosv1beta1.Classifier,
	clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType,
	desired []libsveltosv1beta1.ClassifierLabel,
	km interface {
		CanManageLabel(*libsveltosv1beta1.Classifier, string, string, string, libsveltosv1beta1.ClusterType) bool
		GetManagerForKey(string, string, string, libsveltosv1beta1.ClusterType) (string, error)
	},
) (managed []string, unmanaged []libsveltosv1beta1.UnManagedLabel) {

	for i := range desired {
		label := &desired[i]
		if km.CanManageLabel(fakeClassifier, clusterNamespace, clusterName, label.Key, clusterType) {
			managed = append(managed, label.Key)
		} else {
			ul := libsveltosv1beta1.UnManagedLabel{Key: label.Key}
			if current, err := km.GetManagerForKey(clusterNamespace, clusterName, label.Key, clusterType); err == nil {
				msg := fmt.Sprintf("classifier %s currently manages this label", current)
				ul.FailureMessage = &msg
			}
			unmanaged = append(unmanaged, ul)
		}
	}
	return managed, unmanaged
}

// filterManagedLabels returns only those ClassifierLabels whose keys appear in managedKeys.
func (r *ManagementClusterClassifierReconciler) filterManagedLabels(
	all []libsveltosv1beta1.ClassifierLabel, managedKeys []string,
) []libsveltosv1beta1.ClassifierLabel {

	keySet := make(map[string]bool, len(managedKeys))
	for _, k := range managedKeys {
		keySet[k] = true
	}
	result := make([]libsveltosv1beta1.ClassifierLabel, 0, len(managedKeys))
	for i := range all {
		if keySet[all[i].Key] {
			result = append(result, all[i])
		}
	}
	return result
}

// syncGVKWatches updates the GVK→classifier map and ensures dynamic informer watches are
// registered for every GVK declared in mcc.spec.matchResources.
func (r *ManagementClusterClassifierReconciler) syncGVKWatches(
	mcc *libsveltosv1beta1.ManagementClusterClassifier, logger logr.Logger) {

	r.updateGVKMap(mcc)
	for i := range mcc.Spec.MatchResources {
		rs := &mcc.Spec.MatchResources[i]
		gvk := schema.GroupVersionKind{Group: rs.Group, Version: rs.Version, Kind: rs.Kind}
		if err := r.registerWatchForGVK(gvk); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to register watch for %s: %v", gvk, err))
		}
	}
}

// updateGVKMap rebuilds the GVKToClassifiers entry for mcc under the reconciler lock.
func (r *ManagementClusterClassifierReconciler) updateGVKMap(mcc *libsveltosv1beta1.ManagementClusterClassifier) {
	ref := mccObjectRef(mcc)

	r.Mux.Lock()
	defer r.Mux.Unlock()

	// Remove this classifier from all GVK sets (stale entries from previous spec).
	for gvk, set := range r.GVKToClassifiers {
		set.Erase(ref)
		if set.Len() == 0 {
			delete(r.GVKToClassifiers, gvk)
		}
	}

	// Re-add for the current set of GVKs.
	for i := range mcc.Spec.MatchResources {
		rs := &mcc.Spec.MatchResources[i]
		gvk := schema.GroupVersionKind{Group: rs.Group, Version: rs.Version, Kind: rs.Kind}
		if r.GVKToClassifiers[gvk] == nil {
			r.GVKToClassifiers[gvk] = &libsveltosset.Set{}
		}
		r.GVKToClassifiers[gvk].Insert(ref)
	}
}

// removeFromGVKMap removes mcc from all GVKToClassifiers entries.
func (r *ManagementClusterClassifierReconciler) removeFromGVKMap(mcc *libsveltosv1beta1.ManagementClusterClassifier) {
	ref := mccObjectRef(mcc)

	r.Mux.Lock()
	defer r.Mux.Unlock()

	for gvk, set := range r.GVKToClassifiers {
		set.Erase(ref)
		if set.Len() == 0 {
			delete(r.GVKToClassifiers, gvk)
		}
	}
}

// registerWatchForGVK adds an informer watch for gvk if not already registered.
func (r *ManagementClusterClassifierReconciler) registerWatchForGVK(gvk schema.GroupVersionKind) error {
	r.watchedGVKsMux.Lock()
	defer r.watchedGVKsMux.Unlock()

	if r.watchedGVKs[gvk] {
		return nil
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)

	src := source.Kind[*unstructured.Unstructured](
		r.mgr.GetCache(),
		u,
		handler.TypedEnqueueRequestsFromMapFunc(r.requeueForResource),
	)

	if err := r.controller.Watch(src); err != nil {
		return fmt.Errorf("failed to watch GVK %s: %w", gvk, err)
	}

	r.watchedGVKs[gvk] = true
	return nil
}

// mccObjectRef builds the corev1.ObjectReference used as the key in GVKToClassifiers.
func mccObjectRef(mcc *libsveltosv1beta1.ManagementClusterClassifier) *corev1.ObjectReference {
	return &corev1.ObjectReference{
		APIVersion: libsveltosv1beta1.GroupVersion.String(),
		Kind:       libsveltosv1beta1.ManagementClusterClassifierKind,
		Name:       mcc.Name,
	}
}

// mgmtClusterKey returns a string key for a (namespace, name, clusterType) triple.
func mgmtClusterKey(namespace, name string, ct libsveltosv1beta1.ClusterType) string {
	return fmt.Sprintf("%s/%s/%s", namespace, name, string(ct))
}
