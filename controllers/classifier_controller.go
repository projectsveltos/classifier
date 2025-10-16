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
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/projectsveltos/classifier/controllers/keymanager"
	"github.com/projectsveltos/classifier/pkg/scope"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	"github.com/projectsveltos/libsveltos/lib/deployer"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	predicates "github.com/projectsveltos/libsveltos/lib/predicates"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

type ReportMode int

const (
	// Default mode. In this mode, Classifier running
	// in the management cluster periodically collect
	// ClassifierReport from Sveltos/CAPI clusters
	CollectFromManagementCluster ReportMode = iota

	// In this mode, classifier agent sends ClassifierReport
	// to management cluster.
	// SveltosAgent is provided with Kubeconfig to access
	// management cluster and can only update ClassifierReport/
	// HealthCheckReport/EventReport
	AgentSendReportsNoGateway
)

const (
	// deleteRequeueAfter is how long to wait before checking again to see if the cluster still has
	// children during deletion.
	deleteRequeueAfter = 10 * time.Second

	// normalRequeueAfter is how long to wait before checking again to see if the cluster can be moved
	// to ready after or workload features (for instance ingress or reporter) have failed
	normalRequeueAfter = 10 * time.Second

	controlplaneendpoint = "controlplaneendpoint-key"
	configurationHash    = "configurationHash"

	projectsveltos = "projectsveltos"
)

// ClassifierReconciler reconciles a Classifier object
type ClassifierReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Deployer             deployer.DeployerInterface
	ConcurrentReconciles int
	ClassifierReportMode ReportMode
	AgentInMgmtCluster   bool // if true, indicates sveltos-agent needs to be started in the management cluster
	// Management cluster controlplane endpoint. This is needed when mode is AgentSendReportsNoGateway.
	// It will be used by classifier-agent to send classifierreports back to management cluster.
	ControlPlaneEndpoint  string
	ShardKey              string // when set, only clusters matching the ShardKey will be reconciled
	CapiOnboardAnnotation string // when set, only capi clusters with this annotation are considered
	// use a Mutex to update in-memory structure as MaxConcurrentReconciles is higher than one
	Mux sync.Mutex
	// key: Sveltos/CAPI Cluster namespace/name; value: set of all Classifiers deployed int the Cluster
	// When a Cluster changes, we need to reconcile one or more Classifier (as of now all Classifier
	// are deployed in all Clusters). In order to do so, Classifier reconciler watches for Sveltos/CAPI Cluster
	// changes. Inside a MapFunc there should be no I/O (if that fails, there is no way to recover).
	// So keeps track of Classifier sets deployed in each Sveltos/CAPI Cluster, so that when Sveltos/CAPI Cluster changes
	// list of Classifiers that need reconciliation is in memory.
	// Even though currently each Classifier is deployed in each Sveltos/CAPI Cluster, do not simply keep an in-memory
	// list of all existing Classifier. Rather keep a map per Sveltos/CAPI cluster. If in future, not all Classifiers
	// are deployed in all clusters, map will come in handy.
	// key: Sveltos/CAPI Cluster namespace/name; value: set of all ClusterProfiles matching the Cluster
	ClusterMap map[corev1.ObjectReference]*libsveltosset.Set

	// key: Classifier; value: set of Sveltos/CAPI Clusters matched
	ClassifierMap map[corev1.ObjectReference]*libsveltosset.Set

	// Contains list of all Classifier with at least one conflict
	ClassifierSet libsveltosset.Set

	// List of current existing Classifiers
	AllClassifierSet libsveltosset.Set
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifiers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifiers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifiers/finalizers,verbs=update
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifierreports,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=accessrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationgroups,verbs=get;list;watch;create;delete;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationgroups/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationbundles,verbs=get;list;watch;create;delete;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationbundles/status,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;watch;list;update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get;watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;watch;list
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines/status,verbs=get;watch;list
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;list;watch

func (r *ClassifierReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	if r.ShardKey != "" {
		// Only the default classifier deployment will reconcile classifiers.
		// The other classifier deployments will fetch classifierReports from the clusters
		// matching their shard
		return reconcile.Result{}, nil
	}

	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling")

	// Fecth the Classifier instance
	classifier := &libsveltosv1beta1.Classifier{}
	if err := r.Get(ctx, req.NamespacedName, classifier); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to fetch Classifier")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch Classifier %s",
			req.NamespacedName,
		)
	}

	logger = logger.WithValues("classifier", classifier.Name)

	classifierScope, err := scope.NewClassifierScope(scope.ClassifierScopeParams{
		Client:         r.Client,
		Logger:         logger,
		Classifier:     classifier,
		ControllerName: "classifier",
	})
	if err != nil {
		logger.Error(err, "Failed to create classifierScope")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"unable to create classifier scope for %s",
			req.NamespacedName,
		)
	}

	// Always close the scope when exiting this function so we can persist any Classifier
	// changes.
	defer func() {
		if err := classifierScope.Close(ctx); err != nil {
			reterr = err
		}
	}()

	// Handle deleted classifier
	if !classifier.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, classifierScope)
	}

	// Handle non-deleted classifier
	return r.reconcileNormal(ctx, classifierScope)
}

func (r *ClassifierReconciler) reconcileDelete(
	ctx context.Context,
	classifierScope *scope.ClassifierScope,
) (reconcile.Result, error) {

	logger := classifierScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling Classifier delete")

	err := r.removeAllRegistrations(ctx, classifierScope, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to clear Classifier label registrations")
		return reconcile.Result{}, err
	}

	if !r.AgentInMgmtCluster {
		// In agentless mode, Classifier instances are not copied to managed clusters.
		// So there is nothing to remove from managed cluster.
		f := getHandlersForFeature(libsveltosv1beta1.FeatureClassifier)
		err = r.undeployClassifier(ctx, classifierScope, f, logger)
		if err != nil {
			logger.V(logs.LogInfo).Error(err, "failed to undeploy")
			return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
		}
	}

	err = removeClassifierReports(ctx, r.Client, classifierScope.Classifier, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to remove classifierReports")
		return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
	}

	if r.ClassifierReportMode == CollectFromManagementCluster {
		err = removeAccessRequest(ctx, r.Client, logger)
		if err != nil {
			logger.V(logs.LogInfo).Error(err, "failed to remove accessRequest")
			return reconcile.Result{Requeue: true, RequeueAfter: deleteRequeueAfter}, nil
		}
	}

	r.Mux.Lock()
	defer r.Mux.Unlock()

	classifierInfo := getKeyFromObject(r.Scheme, classifierScope.Classifier)
	r.ClassifierSet.Erase(classifierInfo)
	r.AllClassifierSet.Erase(classifierInfo)

	// Get list of Clusters not matched anymore by Classifier
	if v, ok := r.ClassifierMap[*classifierInfo]; ok {
		clusters := v.Items()
		for i := range clusters {
			r.getClusterMapForEntry(&clusters[i]).Erase(classifierInfo)
		}
	}
	delete(r.ClassifierMap, *classifierInfo)

	if controllerutil.ContainsFinalizer(classifierScope.Classifier, libsveltosv1beta1.ClassifierFinalizer) {
		controllerutil.RemoveFinalizer(classifierScope.Classifier, libsveltosv1beta1.ClassifierFinalizer)
	}

	logger.V(logs.LogInfo).Info("Reconcile delete success")
	return reconcile.Result{}, nil
}

func (r *ClassifierReconciler) reconcileNormal(
	ctx context.Context,
	classifierScope *scope.ClassifierScope,
) (reconcile.Result, error) {

	logger := classifierScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling Classifier")

	if !controllerutil.ContainsFinalizer(classifierScope.Classifier, libsveltosv1beta1.ClassifierFinalizer) {
		if err := r.addFinalizer(ctx, classifierScope); err != nil {
			logger.V(logs.LogDebug).Info("failed to update finalizer")
			return reconcile.Result{}, err
		}
	}

	err := r.updateMatchingClustersAndRegistrations(ctx, classifierScope, logger)
	if err != nil {
		logger.V(logs.LogDebug).Info("failed to update matchingClusterRefs")
		return reconcile.Result{}, err
	}

	err = r.updateLabelsOnMatchingClusters(ctx, classifierScope, logger)
	if err != nil {
		logger.V(logs.LogDebug).Info("failed to update cluster labels")
		return reconcile.Result{}, err
	}

	err = r.updateClusterInfo(ctx, classifierScope)
	if err != nil {
		logger.V(logs.LogDebug).Info("failed to update clusterInfo")
		return reconcile.Result{}, err
	}

	r.updateMaps(classifierScope)

	f := getHandlersForFeature(libsveltosv1beta1.FeatureClassifier)

	if err := r.deployClassifier(ctx, classifierScope, f, logger); err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to deploy")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	logger.V(logs.LogInfo).Info("Reconcile success")
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClassifierReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, logger logr.Logger) (controller.Controller, error) {

	// When Classifier changes, according to ClassifierPredicates,
	// all Classifier with at least one conflict needs to be reconciled

	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1beta1.Classifier{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConcurrentReconciles,
		}).
		Watches(&libsveltosv1beta1.ClassifierReport{},
			handler.EnqueueRequestsFromMapFunc(r.requeueClassifierForClassifierReport),
			builder.WithPredicates(
				ClassifierReportPredicate(mgr.GetLogger().WithValues("predicate", "classifierreportpredicate")),
			),
		).
		Watches(&libsveltosv1beta1.Classifier{},
			handler.EnqueueRequestsFromMapFunc(r.requeueClassifierForClassifier),
			builder.WithPredicates(
				ClassifierPredicate(mgr.GetLogger().WithValues("predicate", "classifiepredicate")),
			),
		).
		Watches(&libsveltosv1beta1.SveltosCluster{},
			handler.EnqueueRequestsFromMapFunc(r.requeueClassifierForSveltosCluster),
			builder.WithPredicates(
				predicates.SveltosClusterPredicates(mgr.GetLogger().WithValues("predicate", "sveltosclusterpredicate")),
			),
		).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.requeueClassifierForSecret),
			builder.WithPredicates(
				SecretPredicates(mgr.GetLogger().WithValues("predicate", "secretpredicate")),
			),
		).
		Watches(&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.requeueClassifierForConfigMap),
			builder.WithPredicates(
				ConfigMapPredicates(mgr.GetLogger().WithValues("predicate", "configmappredicate")),
			),
		).
		Build(r)
	if err != nil {
		return nil, errors.Wrap(err, "error creating controller")
	}

	// At this point we don't know yet whether CAPI is present in the cluster.
	// Later on, in main, we detect that and if CAPI is present WatchForCAPI will be invoked.

	if r.ClassifierReportMode == CollectFromManagementCluster {
		go collectClassifierReports(mgr.GetClient(), r.ShardKey, r.CapiOnboardAnnotation, getVersion(), mgr.GetLogger())
	}

	if getAgentInMgmtCluster() {
		go removeStaleClassifierResources(ctx, logger)
	}

	return c, nil
}

func (r *ClassifierReconciler) WatchForCAPI(mgr ctrl.Manager, c controller.Controller) error {
	// When cluster-api cluster changes, according to ClusterPredicates,
	// one or more Classifiers need to be reconciled.
	sourceCluster := source.Kind[*clusterv1.Cluster](
		mgr.GetCache(),
		&clusterv1.Cluster{},
		handler.TypedEnqueueRequestsFromMapFunc(r.requeueClassifierForCluster),
		predicates.ClusterPredicate{Logger: mgr.GetLogger().WithValues("predicate", "clusterpredicate")},
	)
	if err := c.Watch(sourceCluster); err != nil {
		return err
	}

	// When cluster-api machine changes, according to ClusterPredicates,
	// one or more ClusterProfiles need to be reconciled.
	machineCluster := source.Kind[*clusterv1.Machine](
		mgr.GetCache(),
		&clusterv1.Machine{},
		handler.TypedEnqueueRequestsFromMapFunc(r.requeueClassifierForMachine),
		predicates.MachinePredicate{Logger: mgr.GetLogger().WithValues("predicate", "clusterpredicate")},
	)
	if err := c.Watch(machineCluster); err != nil {
		return err
	}

	return nil
}

func (r *ClassifierReconciler) getClusterMapForEntry(entry *corev1.ObjectReference) *libsveltosset.Set {
	s := r.ClusterMap[*entry]
	if s == nil {
		s = &libsveltosset.Set{}
		r.ClusterMap[*entry] = s
	}
	return s
}

func (r *ClassifierReconciler) addFinalizer(ctx context.Context, classifierScope *scope.ClassifierScope) error {
	// If the Classifier doesn't have our finalizer, add it.
	controllerutil.AddFinalizer(classifierScope.Classifier, libsveltosv1beta1.ClassifierFinalizer)
	// Register the finalizer immediately to avoid orphaning clusterprofile resources on delete
	if err := classifierScope.PatchObject(ctx); err != nil {
		classifierScope.Error(err, "Failed to add finalizer")
		return errors.Wrapf(
			err,
			"Failed to add finalizer for %s",
			classifierScope.Name(),
		)
	}
	return nil
}

// updateClusterInfo updates Classifier Status ClusterInfo by adding an entry for any
// new cluster where Classifier needs to be deployed
func (r *ClassifierReconciler) updateClusterInfo(ctx context.Context, classifierScope *scope.ClassifierScope) error {
	classifier := classifierScope.Classifier

	getClusterID := func(cluster corev1.ObjectReference) string {
		return fmt.Sprintf("%s:%s/%s", clusterproxy.GetClusterType(&cluster), cluster.Namespace, cluster.Name)
	}

	matchingCluster, err := clusterproxy.GetListOfClusters(ctx, r.Client, "", r.CapiOnboardAnnotation,
		classifierScope.Logger)
	if err != nil {
		return err
	}

	// Build Map for all Clusters with an entry in Classifier.Status.ClusterInfo
	clusterMap := make(map[string]bool)
	for i := range classifier.Status.ClusterInfo {
		c := &classifier.Status.ClusterInfo[i]
		clusterMap[getClusterID(c.Cluster)] = true
	}

	newClusterInfo := make([]libsveltosv1beta1.ClusterInfo, 0)
	for i := range matchingCluster {
		c := matchingCluster[i]
		if _, ok := clusterMap[getClusterID(c)]; !ok {
			newClusterInfo = append(newClusterInfo, libsveltosv1beta1.ClusterInfo{
				Cluster: c,
			})
		}
	}

	finalClusterInfo := classifier.Status.ClusterInfo
	finalClusterInfo = append(finalClusterInfo, newClusterInfo...)
	classifierScope.SetClusterInfo(finalClusterInfo)
	return nil
}

// updateMatchingClustersAndRegistrations does two things:
// - updates Classifier Status.MachingClusterStatuses
// - update label key registration with keymanager instance
func (r *ClassifierReconciler) updateMatchingClustersAndRegistrations(ctx context.Context,
	classifierScope *scope.ClassifierScope, logger logr.Logger) error {

	listOptions := []client.ListOption{
		client.MatchingLabels{
			libsveltosv1beta1.ClassifierlNameLabel: classifierScope.Classifier.Name,
		},
	}

	classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
	err := r.List(ctx, classifierReportList, listOptions...)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to list ClassifierReports. Err: %v", err))
		return err
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("found %d ClassifierReports for this Classifier instance",
		len(classifierReportList.Items)))

	// create map of current matching clusters
	currentMatchingClusters := make(map[corev1.ObjectReference]bool)
	for i := range classifierReportList.Items {
		report := &classifierReportList.Items[i]
		// If Sveltos is managing the management cluster as well,
		// there will be two types of ClassifierReports:
		// 1. created by sveltos-agent running in the management cluster.
		// Those will have Spec.ClusterNamespace not set
		// 2. pulled by classifier or pushed by sveltos-agent running
		// in the managed cluster. Those will have Spec.ClusterNamespace set
		// Consider only type #2
		if report.Spec.ClusterNamespace == "" {
			continue
		}
		if report.Spec.Match {
			cluster := getClusterRefFromClassifierReport(report)
			l := logger.WithValues("cluster", fmt.Sprintf("type: %s cluster %s/%s", report.Spec.ClusterType, cluster.Namespace, cluster.Name))
			l.V(logs.LogDebug).Info("is a match")
			currentMatchingClusters[*cluster] = true
		}
	}

	// create map of old matching clusters
	oldMatchingClusters := make(map[corev1.ObjectReference]bool)
	for i := range classifierScope.Classifier.Status.MachingClusterStatuses {
		ref := classifierScope.Classifier.Status.MachingClusterStatuses[i]
		oldMatchingClusters[ref.ClusterRef] = true
	}

	err = r.handleLabelRegistrations(ctx, classifierScope.Classifier, currentMatchingClusters,
		oldMatchingClusters, logger)
	if err != nil {
		return err
	}

	matchingClusterStatus := make([]libsveltosv1beta1.MachingClusterStatus, len(currentMatchingClusters))
	i := 0
	unManaged := 0
	for c := range currentMatchingClusters {
		tmpManaged, tmpUnmanaged, err := r.classifyLabels(ctx, classifierScope.Classifier, &c, logger)
		if err != nil {
			return err
		}
		unManaged += len(tmpUnmanaged)
		matchingClusterStatus[i] =
			libsveltosv1beta1.MachingClusterStatus{
				ClusterRef:      c,
				ManagedLabels:   tmpManaged,
				UnManagedLabels: tmpUnmanaged,
			}
		i++
	}

	r.updateClassifierSet(classifierScope, unManaged != 0)

	classifierScope.SetMachingClusterStatuses(matchingClusterStatus)

	return nil
}

func (r *ClassifierReconciler) updateClassifierSet(classifierScope *scope.ClassifierScope, hasUnManaged bool) {
	r.Mux.Lock()
	defer r.Mux.Unlock()

	classifierInfo := getKeyFromObject(r.Scheme, classifierScope.Classifier)
	if hasUnManaged {
		r.ClassifierSet.Insert(classifierInfo)
	} else {
		r.ClassifierSet.Erase(classifierInfo)
	}

	r.AllClassifierSet.Insert(classifierInfo)
}

// updateLabelsOnMatchingClusters set labels on all matching clusters (only for clusters
// for which permission is granted by keymanager)
func (r *ClassifierReconciler) updateLabelsOnMatchingClusters(ctx context.Context,
	classifierScope *scope.ClassifierScope, logger logr.Logger) error {

	// Register Classifier instance as wanting to manage any labels in ClassifierLabels
	// for all the clusters currently matching
	for i := range classifierScope.Classifier.Status.MachingClusterStatuses {
		ref := &classifierScope.Classifier.Status.MachingClusterStatuses[i].ClusterRef
		cluster, err := clusterproxy.GetCluster(ctx, r.Client, ref.Namespace, ref.Name, clusterproxy.GetClusterType(ref))
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			logger.V(logs.LogInfo).Error(err, fmt.Sprintf("failed to get cluster %s/%s", ref.Namespace, ref.Name))
			return err
		}

		l := logger.WithValues("cluster", fmt.Sprintf("%s/%s", cluster.GetNamespace(), cluster.GetName()))
		l.V(logs.LogDebug).Info("update labels on cluster")
		err = r.updateLabelsOnCluster(ctx, classifierScope, cluster, clusterproxy.GetClusterType(ref), l)
		if err != nil {
			// If cluster was removed before classifier had a chance to react to it, ignore the error
			if apierrors.IsNotFound(err) {
				return nil
			}
			l.V(logs.LogDebug).Error(err, "failed to update labels on cluster")
			return err
		}
	}

	return nil
}

func (r *ClassifierReconciler) updateLabelsOnCluster(ctx context.Context,
	classifierScope *scope.ClassifierScope, cluster client.Object, clusterType libsveltosv1beta1.ClusterType,
	logger logr.Logger) error {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	for i := range classifierScope.Classifier.Spec.ClassifierLabels {
		label := classifierScope.Classifier.Spec.ClassifierLabels[i]
		if manager.CanManageLabel(classifierScope.Classifier, cluster.GetNamespace(), cluster.GetName(), label.Key, clusterType) {
			labels := cluster.GetLabels()
			if labels == nil {
				labels = make(map[string]string)
			}
			labels[label.Key] = label.Value
			cluster.SetLabels(labels)
		} else {
			l := logger.WithValues("label", label.Key)
			l.V(logs.LogInfo).Info("cannot manage label")
			// Issues is already reported
		}
	}

	return r.Update(ctx, cluster)
}

func (r *ClassifierReconciler) updateMaps(classifierScope *scope.ClassifierScope) {
	currentClusters := &libsveltosset.Set{}
	for i := range classifierScope.Classifier.Status.ClusterInfo {
		currentClusters.Insert(&classifierScope.Classifier.Status.ClusterInfo[i].Cluster)
	}

	r.Mux.Lock()
	defer r.Mux.Unlock()

	classifierInfo := getKeyFromObject(r.Scheme, classifierScope.Classifier)

	// Get list of Clusters not matched anymore by Classifier
	var toBeRemoved []corev1.ObjectReference
	if v, ok := r.ClassifierMap[*classifierInfo]; ok {
		toBeRemoved = v.Difference(currentClusters)
	}

	// For each currently matching Cluster, add Classifier as consumer
	for i := range classifierScope.Classifier.Status.ClusterInfo {
		clusterInfo := &classifierScope.Classifier.Status.ClusterInfo[i].Cluster
		r.getClusterMapForEntry(clusterInfo).Insert(classifierInfo)
	}

	// For each Cluster not matched anymore, remove Classifier as consumer
	for i := range toBeRemoved {
		clusterName := toBeRemoved[i]
		r.getClusterMapForEntry(&clusterName).Erase(classifierInfo)
	}

	// Update list of Cluster currently a match for a Classifier
	r.ClassifierMap[*classifierInfo] = currentClusters
}

// removeAllRegistrations unregisters Classifier for all cluster labels
// it used to manage (in any matching cluster)
func (r *ClassifierReconciler) removeAllRegistrations(ctx context.Context,
	classifierScope *scope.ClassifierScope, logger logr.Logger,
) error {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	for i := range classifierScope.Classifier.Status.MachingClusterStatuses {
		c := &classifierScope.Classifier.Status.MachingClusterStatuses[i].ClusterRef
		manager.RemoveAllRegistrations(classifierScope.Classifier, c.Namespace, c.Name, clusterproxy.GetClusterType(c))
	}

	return nil
}

// handleLabelRegistrations registers Classifier for all labels, considering all clusters
// currently matching Classifier
// Clear old registrations
func (r *ClassifierReconciler) handleLabelRegistrations(ctx context.Context,
	classifier *libsveltosv1beta1.Classifier,
	currentMatchingClusters, oldMatchingClusters map[corev1.ObjectReference]bool,
	logger logr.Logger) error {

	// Register Classifier instance as wanting to manage any labels in ClassifierLabels
	// for all the clusters currently matching
	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	matchingClusterRefs := make([]corev1.ObjectReference, len(currentMatchingClusters))
	i := 0
	for c := range currentMatchingClusters {
		clusterType := clusterproxy.GetClusterType(&c)
		manager.RemoveStaleRegistrations(classifier, c.Namespace, c.Name, clusterType)
		manager.RegisterClassifierForLabels(classifier, c.Namespace, c.Name, clusterType)
		matchingClusterRefs[i] = c
		i++
	}

	// For every cluster which is not a match anymore, remove registations
	for c := range oldMatchingClusters {
		if _, ok := currentMatchingClusters[c]; !ok {
			clusterType := clusterproxy.GetClusterType(&c)
			manager.RemoveAllRegistrations(classifier, c.Namespace, c.Name, clusterType)
		}
	}

	return nil
}

// classifyLabels divides labels in Managed and UnManaged
func (r *ClassifierReconciler) classifyLabels(ctx context.Context, classifier *libsveltosv1beta1.Classifier,
	cluster *corev1.ObjectReference, logger logr.Logger) ([]string, []libsveltosv1beta1.UnManagedLabel, error) {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return nil, nil, err
	}

	clusterType := clusterproxy.GetClusterType(&corev1.ObjectReference{
		Namespace: cluster.Namespace, Name: cluster.Name,
		APIVersion: cluster.APIVersion, Kind: cluster.Kind,
	})

	managed := make([]string, 0)
	unManaged := make([]libsveltosv1beta1.UnManagedLabel, 0)
	for i := range classifier.Spec.ClassifierLabels {
		label := &classifier.Spec.ClassifierLabels[i]
		if manager.CanManageLabel(classifier, cluster.Namespace, cluster.Name, label.Key, clusterType) {
			logger.V(logs.LogDebug).Info(fmt.Sprintf("classifier can manage label %s", label.Key))
			managed = append(managed, label.Key)
		} else {
			logger.V(logs.LogDebug).Info(fmt.Sprintf("classifier cannot manage label %s", label.Key))
			tmpUnManaged := libsveltosv1beta1.UnManagedLabel{Key: label.Key}
			currentManager, err := manager.GetManagerForKey(cluster.Namespace, cluster.Name, label.Key, clusterType)
			if err == nil {
				failureMessage := fmt.Sprintf("classifier %s currently manage this", currentManager)
				tmpUnManaged.FailureMessage = &failureMessage
			}
			unManaged = append(unManaged, tmpUnManaged)
		}
	}

	return managed, unManaged, nil
}

const (
	sveltosAgentInMgtmCluster = "sveltosAgentInMgtmCluster"
)

func startSveltosAgentInMgmtCluster(o deployer.Options) bool {
	if o.HandlerOptions == nil {
		return false
	}

	runInMgtmCluster := false
	if _, ok := o.HandlerOptions[sveltosAgentInMgtmCluster]; ok {
		runInMgtmCluster = true
	}

	return runInMgtmCluster
}
