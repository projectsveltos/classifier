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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
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
	// deleteRequeueAfter is how long to wait before checking again to see if the classifier
	// can be deleted
	deleteRequeueAfter = 10 * time.Second

	// normalRequeueAfter is how long to wait before reconciling the classifier instance
	normalRequeueAfter = 10 * time.Second

	// conflictRequeueAfter is how long to wait before retrying when a label
	// management conflict is detected with another classifier instance.
	conflictRequeueAfter = time.Minute

	controlplaneendpoint = "controlplaneendpoint-key"
	configurationHash    = "configurationHash"
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

	Logger logr.Logger
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifiers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifiers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifiers/finalizers,verbs=update
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifierreports,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=classifierreports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=accessrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationgroups,verbs=get;list;watch;create;delete;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationgroups/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationbundles,verbs=get;list;watch;create;delete;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=configurationbundles/status,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;watch;list;update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get;watch;list
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
	logger.V(logs.LogDebug).Info("Reconciling")

	// Fecth the Classifier instance
	classifier := &libsveltosv1beta1.Classifier{}
	if err := r.Get(ctx, req.NamespacedName, classifier); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "failed to fetch Classifier")
		return reconcile.Result{}, fmt.Errorf(
			"failed to fetch Classifier %s: %w",
			req.NamespacedName, err,
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
		logger.Error(err, "failed to create classifierScope")
		return reconcile.Result{}, fmt.Errorf(
			"unable to create classifier scope for %s: %w",
			req.NamespacedName, err,
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
	logger.V(logs.LogDebug).Info("Reconciling Classifier delete")

	err := r.removeLabelsFromClusters(ctx, classifierScope, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to remove managed labels from clusters")
		return reconcile.Result{}, err
	}

	err = r.removeAllRegistrations(ctx, classifierScope, logger)
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

	logger.V(logs.LogDebug).Info("Reconcile delete success")
	return reconcile.Result{}, nil
}

func (r *ClassifierReconciler) reconcileNormal(
	ctx context.Context,
	classifierScope *scope.ClassifierScope,
) (reconcile.Result, error) {

	logger := classifierScope.Logger
	logger.V(logs.LogDebug).Info("Reconciling Classifier")

	if !controllerutil.ContainsFinalizer(classifierScope.Classifier, libsveltosv1beta1.ClassifierFinalizer) {
		if err := r.addFinalizer(ctx, classifierScope); err != nil {
			logger.V(logs.LogDebug).Info("failed to update finalizer")
			return reconcile.Result{}, err
		}
	}

	// get list of clusters currently matching this Classifier instance
	matchingClusters, err := r.syncAndGetMatchingClusters(ctx, classifierScope, logger)
	if err != nil {
		logger.V(logs.LogDebug).Error(err, "failed to get matching clusters")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	// For clusters that no longer match, remove managed labels and clear internal registrations
	err = r.cleanUpManagedResources(ctx, classifierScope, matchingClusters, logger)
	if err != nil {
		// Use Error level because this indicates a failure to clean up resources
		logger.V(logs.LogDebug).Error(err, "failed to clean up labels/registrations for non-matching clusters")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	// For every currently matching cluster, update label ownership registrations
	matchingStatuses, err := r.updateMatchingClustersAndRegistrations(ctx, classifierScope, matchingClusters, logger)
	if err != nil {
		logger.V(logs.LogDebug).Error(err, "failed to update status/registrations for matching clusters")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	// Apply the labels to the clusters
	err = r.updateLabelsOnMatchingClusters(ctx, classifierScope, matchingStatuses, logger)
	if err != nil {
		logger.V(logs.LogDebug).Error(err, "failed to apply labels to matching clusters")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	err = r.ensureClassifierReports(ctx, classifierScope)
	if err != nil {
		logger.V(logs.LogDebug).Error(err, "failed to ensure ClassifierReports")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	if err := r.updateMaps(ctx, classifierScope); err != nil {
		logger.V(logs.LogDebug).Error(err, "failed to update internal maps")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	f := getHandlersForFeature(libsveltosv1beta1.FeatureClassifier)

	if err := r.deployClassifier(ctx, classifierScope, f, logger); err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to deploy")
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	if hasUnManagedLabels(matchingStatuses) {
		return reconcile.Result{Requeue: true, RequeueAfter: conflictRequeueAfter}, nil
	}

	logger.V(logs.LogDebug).Info("Reconcile success")
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClassifierReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager, logger logr.Logger) (controller.Controller, error) {

	// When Classifier changes, according to ClassifierPredicates,
	// all Classifier with at least one conflict needs to be reconciled

	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1beta1.Classifier{}, builder.WithPredicates(
			ClassifierPredicate{Logger: mgr.GetLogger().WithValues("classifierPredicate")})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConcurrentReconciles,
		}).
		Watches(&libsveltosv1beta1.ClassifierReport{},
			handler.EnqueueRequestsFromMapFunc(r.requeueClassifierForClassifierReport),
			builder.WithPredicates(
				ClassifierReportPredicate(mgr.GetLogger().WithValues("predicate", "classifierreportpredicate")),
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
		return nil, fmt.Errorf("error creating controller: %w", err)
	}

	// At this point we don't know yet whether CAPI is present in the cluster.
	// Later on, in main, we detect that and if CAPI is present WatchForCAPI will be invoked.

	if r.ClassifierReportMode == CollectFromManagementCluster {
		go collectClassifierReports(ctx, mgr.GetClient(), r.ShardKey, r.CapiOnboardAnnotation, getVersion(), mgr.GetLogger())
	}

	if getAgentInMgmtCluster() {
		go removeStaleClassifierResources(ctx, logger)
	}

	go removeStaleClassifierReports(ctx, logger)

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
		classifierScope.Error(err, "failed to add finalizer")
		return fmt.Errorf(
			"failed to add finalizer for %s: %w",
			classifierScope.Name(), err,
		)
	}
	return nil
}

// ensureClassifierReports creates a ClassifierReport in the management cluster for every known
// cluster that does not already have one. The report is initialized with Match=false; the agent
// updates Spec.Match on its next reconcile cycle.
func (r *ClassifierReconciler) ensureClassifierReports(ctx context.Context, classifierScope *scope.ClassifierScope) error {
	allClusters, err := clusterproxy.GetListOfClusters(ctx, r.Client, "", r.CapiOnboardAnnotation,
		classifierScope.Logger)
	if err != nil {
		return err
	}

	for i := range allClusters {
		if err := ensureClassifierReportForCluster(ctx, r.Client, classifierScope.Classifier.Name,
			&allClusters[i]); err != nil {
			return err
		}
	}
	return nil
}

// getMatchingClusters returns the set of clusters currently matching the Classifier
// based on the existing ClassifierReport resources. If a ClassifierReport is found but
// the cluster no longer exist, removes the ClassifierReport
func (r *ClassifierReconciler) syncAndGetMatchingClusters(ctx context.Context,
	classifierScope *scope.ClassifierScope, logger logr.Logger) (map[corev1.ObjectReference]bool, error) {

	// Get existing clusters first
	existingClusters, err := clusterproxy.GetListOfClusters(ctx, r.Client, "", "", logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get existing clusters")
		return nil, err
	}

	existingClustersMap := make(map[corev1.ObjectReference]struct{}, len(existingClusters))
	for i := range existingClusters {
		existingClustersMap[existingClusters[i]] = struct{}{}
	}

	listOptions := []client.ListOption{
		client.MatchingLabels{
			libsveltosv1beta1.ClassifierlNameLabel: classifierScope.Classifier.Name,
		},
	}

	classifierReportList := &libsveltosv1beta1.ClassifierReportList{}
	if err := r.List(ctx, classifierReportList, listOptions...); err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to list ClassifierReports")
		return nil, err
	}

	currentMatchingClusters := make(map[corev1.ObjectReference]bool)
	for i := range classifierReportList.Items {
		report := &classifierReportList.Items[i]

		// Only consider reports with ClusterNamespace set
		if report.Spec.ClusterNamespace == "" {
			continue
		}

		if report.Spec.Match {
			cluster := getClusterRefFromClassifierReport(report)
			// Only add if it actually exists in the cluster list
			if _, exists := existingClustersMap[*cluster]; exists {
				currentMatchingClusters[*cluster] = true
			} else {
				_ = removeClusterClassifierReports(ctx, r.Client, cluster.Namespace, cluster.Name,
					clusterproxy.GetClusterType(cluster), logger)
			}
		}
	}

	return currentMatchingClusters, nil
}

// updateMatchingClustersAndRegistrations synchronizes the KeyManager registrations,
// writes managed/unmanaged label data into each matching cluster's ClassifierReport.Status,
// and returns the in-memory slice for use by the caller within the same reconcile cycle.
func (r *ClassifierReconciler) updateMatchingClustersAndRegistrations(ctx context.Context,
	classifierScope *scope.ClassifierScope, matchingClusters map[corev1.ObjectReference]bool,
	logger logr.Logger) ([]libsveltosv1beta1.MachingClusterStatus, error) {

	// Sync registrations first so classifyLabels knows what we are allowed to manage
	if err := r.registerMatchingClusters(ctx, classifierScope.Classifier, matchingClusters, logger); err != nil {
		return nil, fmt.Errorf("failed to register labels with keymanager: %w", err)
	}

	matchingClusterStatus := make([]libsveltosv1beta1.MachingClusterStatus, 0, len(matchingClusters))
	var hasUnmanaged bool

	for c := range matchingClusters {
		managed, unmanaged, err := r.classifyLabels(ctx, classifierScope.Classifier, &c, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to classify labels for cluster %s/%s: %w", c.Namespace, c.Name, err)
		}

		if len(unmanaged) > 0 {
			hasUnmanaged = true
		}

		if err := updateClassifierReportLabelStatus(ctx, r.Client, classifierScope.Classifier.Name,
			&c, managed, unmanaged); err != nil {
			return nil, fmt.Errorf("failed to update ClassifierReport label status for cluster %s/%s: %w",
				c.Namespace, c.Name, err)
		}

		matchingClusterStatus = append(matchingClusterStatus, libsveltosv1beta1.MachingClusterStatus{
			ClusterRef:      c,
			ManagedLabels:   managed,
			UnManagedLabels: unmanaged,
		})
	}

	r.updateClassifierSet(classifierScope, hasUnmanaged)
	return matchingClusterStatus, nil
}

func (r *ClassifierReconciler) cleanUpManagedResources(ctx context.Context,
	classifierScope *scope.ClassifierScope, matchingClusters map[corev1.ObjectReference]bool,
	logger logr.Logger) error {

	// Build the set of clusters we previously applied labels to by reading ClassifierReport.Status.
	// Any cluster with non-empty ManagedLabels or UnManagedLabels was processed in a prior reconcile.
	reports, err := listClassifierReportsForClassifier(ctx, r.Client, classifierScope.Classifier.Name)
	if err != nil {
		return fmt.Errorf("failed to list ClassifierReports: %w", err)
	}

	oldMatchingClusters := make(map[corev1.ObjectReference]bool)
	for i := range reports {
		report := &reports[i]
		if report.Spec.ClusterNamespace == "" {
			continue
		}
		if len(report.Status.ManagedLabels) > 0 || len(report.Status.UnManagedLabels) > 0 {
			cluster := getClusterRefFromClassifierReport(report)
			oldMatchingClusters[*cluster] = true
		}
	}

	err = r.cleanUpNonMatchingClusters(ctx, classifierScope.Classifier,
		matchingClusters, oldMatchingClusters, logger)
	if err != nil {
		return fmt.Errorf("failed to clean up non-matching clusters: %w", err)
	}

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

// updateLabelsOnMatchingClusters applies the desired labels to all clusters
// currently matching the Classifier, skipping clusters that are not found.
func (r *ClassifierReconciler) updateLabelsOnMatchingClusters(ctx context.Context,
	classifierScope *scope.ClassifierScope,
	statuses []libsveltosv1beta1.MachingClusterStatus, logger logr.Logger) error {

	var errs []error

	for i := range statuses {
		ref := &statuses[i].ClusterRef

		cluster, err := clusterproxy.GetCluster(ctx, r.Client, ref.Namespace, ref.Name, clusterproxy.GetClusterType(ref))
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue // Cluster was likely deleted; cleanUpManagedResources will handle it next time
			}
			logger.Error(err, "failed to fetch cluster for label update", "cluster", ref.Name)
			errs = append(errs, err)
			continue
		}

		clusterLogger := logger.WithValues("cluster", fmt.Sprintf("%s/%s", cluster.GetNamespace(), cluster.GetName()))
		// Attempt to update labels on the physical cluster
		if err := r.updateLabelsOnCluster(ctx, classifierScope, cluster, clusterproxy.GetClusterType(ref), clusterLogger); err != nil {
			if !apierrors.IsNotFound(err) {
				clusterLogger.Error(err, "failed to apply labels to cluster")
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// removeLabelsFromCluster removes all labels from the provided cluster that were
// specifically managed by this Classifier instance. It uses the KeyManager
// to ensure it only deletes labels it has permission to manage.
func (r *ClassifierReconciler) removeLabelsFromCluster(ctx context.Context,
	classifier *libsveltosv1beta1.Classifier, cluster client.Object, clusterType libsveltosv1beta1.ClusterType,
	logger logr.Logger) error {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	labels := cluster.GetLabels()
	if labels == nil {
		return nil // Nothing to remove
	}

	labelsChanged := false
	for i := range classifier.Spec.ClassifierLabels {
		label := classifier.Spec.ClassifierLabels[i]

		// Only remove the label if this specific Classifier is authorized to manage it
		if manager.CanManageLabel(classifier, cluster.GetNamespace(), cluster.GetName(), label.Key,
			clusterType) {

			if _, exists := labels[label.Key]; exists {
				delete(labels, label.Key)
				labelsChanged = true
			}
		}
	}

	// Only trigger a cluster update if labels were actually modified
	if labelsChanged {
		cluster.SetLabels(labels)
		return r.Update(ctx, cluster)
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

func (r *ClassifierReconciler) updateMaps(ctx context.Context, classifierScope *scope.ClassifierScope) error {
	reports, err := listClassifierReportsForClassifier(ctx, r.Client, classifierScope.Classifier.Name)
	if err != nil {
		return fmt.Errorf("failed to list ClassifierReports for map update: %w", err)
	}

	currentClusters := &libsveltosset.Set{}
	for i := range reports {
		report := &reports[i]
		if report.Spec.ClusterNamespace == "" {
			continue
		}
		if report.Status.DeploymentStatus != nil &&
			*report.Status.DeploymentStatus == libsveltosv1beta1.SveltosStatusRemoved {

			continue
		}
		cluster := getClusterRefFromClassifierReport(report)
		currentClusters.Insert(cluster)
	}

	r.Mux.Lock()
	defer r.Mux.Unlock()

	classifierInfo := getKeyFromObject(r.Scheme, classifierScope.Classifier)

	var toBeRemoved []corev1.ObjectReference
	if v, ok := r.ClassifierMap[*classifierInfo]; ok {
		toBeRemoved = v.Difference(currentClusters)
	}

	for i := range reports {
		report := &reports[i]
		if report.Spec.ClusterNamespace == "" {
			continue
		}
		if report.Status.DeploymentStatus != nil &&
			*report.Status.DeploymentStatus == libsveltosv1beta1.SveltosStatusRemoved {

			continue
		}
		cluster := getClusterRefFromClassifierReport(report)
		r.getClusterMapForEntry(cluster).Insert(classifierInfo)
	}

	for i := range toBeRemoved {
		clusterName := toBeRemoved[i]
		r.getClusterMapForEntry(&clusterName).Erase(classifierInfo)
	}

	r.ClassifierMap[*classifierInfo] = currentClusters
	return nil
}

// removeLabelsFromClusters removes labels managed by this Classifier from every cluster
// that has ManagedLabels tracked in its ClassifierReport.Status.
func (r *ClassifierReconciler) removeLabelsFromClusters(ctx context.Context,
	classifierScope *scope.ClassifierScope, logger logr.Logger) error {

	reports, err := listClassifierReportsForClassifier(ctx, r.Client, classifierScope.Classifier.Name)
	if err != nil {
		return fmt.Errorf("failed to list ClassifierReports: %w", err)
	}

	var errs []error
	for i := range reports {
		report := &reports[i]
		if report.Spec.ClusterNamespace == "" || len(report.Status.ManagedLabels) == 0 {
			continue
		}

		ref := getClusterRefFromClassifierReport(report)
		cluster, err := clusterproxy.GetCluster(ctx, r.Client, ref.Namespace, ref.Name,
			clusterproxy.GetClusterType(ref))
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			logger.V(logs.LogInfo).Error(err, "failed to get cluster",
				"namespace", ref.Namespace, "name", ref.Name)
			errs = append(errs, err)
			continue
		}

		clusterLogger := logger.WithValues(
			"cluster", fmt.Sprintf("%s/%s", cluster.GetNamespace(), cluster.GetName()),
			"clusterType", clusterproxy.GetClusterType(ref),
		)
		clusterLogger.V(logs.LogDebug).Info("removing managed labels from cluster")

		if err := r.removeLabelsFromCluster(ctx, classifierScope.Classifier, cluster,
			clusterproxy.GetClusterType(ref), clusterLogger); err != nil {
			clusterLogger.Error(err, "failed to remove labels")
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// removeAllRegistrations unregisters Classifier for all cluster labels it was managing.
func (r *ClassifierReconciler) removeAllRegistrations(ctx context.Context,
	classifierScope *scope.ClassifierScope, logger logr.Logger,
) error {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	reports, err := listClassifierReportsForClassifier(ctx, r.Client, classifierScope.Classifier.Name)
	if err != nil {
		return fmt.Errorf("failed to list ClassifierReports: %w", err)
	}

	for i := range reports {
		report := &reports[i]
		if report.Spec.ClusterNamespace == "" {
			continue
		}
		c := getClusterRefFromClassifierReport(report)
		manager.RemoveAllRegistrations(classifierScope.Classifier, c.Namespace, c.Name, clusterproxy.GetClusterType(c))
	}

	return nil
}

// registerMatchingClusters updates the label ownership registrations for all clusters
// currently matching the Classifier. It also ensures stale registrations for these
// specific clusters are cleared.
func (r *ClassifierReconciler) registerMatchingClusters(ctx context.Context,
	classifier *libsveltosv1beta1.Classifier,
	currentMatchingClusters map[corev1.ObjectReference]bool,
	logger logr.Logger) error {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	for c := range currentMatchingClusters {
		clusterType := clusterproxy.GetClusterType(&c)
		// Ensure the manager is up-to-date for this specific cluster
		manager.RemoveStaleRegistrations(classifier, c.Namespace, c.Name, clusterType)
		manager.RegisterClassifierForLabels(classifier, c.Namespace, c.Name, clusterType)
	}

	return nil
}

// cleanUpNonMatchingClusters identifies clusters that no longer match the Classifier.
// It removes managed labels from the clusters first; only upon successful label removal
// does it clear the internal registrations in the KeyManager.
func (r *ClassifierReconciler) cleanUpNonMatchingClusters(ctx context.Context,
	classifier *libsveltosv1beta1.Classifier,
	currentMatchingClusters, oldMatchingClusters map[corev1.ObjectReference]bool,
	logger logr.Logger) error {

	manager, err := keymanager.GetKeyManagerInstance(ctx, r.Client)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get label key manager")
		return err
	}

	var errs []error
	for c := range oldMatchingClusters {
		// Only process clusters that were in the old set but are NOT in the current set
		if _, ok := currentMatchingClusters[c]; ok {
			continue
		}

		clusterType := clusterproxy.GetClusterType(&c)
		clusterLogger := logger.WithValues("cluster", fmt.Sprintf("%s/%s", c.Namespace, c.Name))

		// 1. Attempt to fetch the cluster
		cluster, err := clusterproxy.GetCluster(ctx, r.Client, c.Namespace, c.Name, clusterType)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Cluster is gone, so labels are effectively removed. Safe to clear registrations.
				manager.RemoveAllRegistrations(classifier, c.Namespace, c.Name, clusterType)
				continue
			}

			wrappedErr := fmt.Errorf("failed to get cluster %s/%s for cleanup: %w", c.Namespace, c.Name, err)
			clusterLogger.Error(wrappedErr, "lookup failed, skipping registration removal")
			errs = append(errs, wrappedErr)
			continue
		}

		// 2. Attempt to remove the managed labels
		// Passing the classifier object as the owner to removeLabelsFromCluster
		if err := r.removeLabelsFromCluster(ctx, classifier, cluster, clusterType, clusterLogger); err != nil {
			wrappedErr := fmt.Errorf("failed to remove labels from cluster %s/%s: %w", c.Namespace, c.Name, err)
			clusterLogger.Error(wrappedErr, "label removal failed, skipping registration removal")
			errs = append(errs, wrappedErr)
			continue
		}

		// 3. Only remove registrations if label removal was successful
		manager.RemoveAllRegistrations(classifier, c.Namespace, c.Name, clusterType)

		// 4. Clear label tracking from ClassifierReport.Status now that labels are removed.
		if err := updateClassifierReportLabelStatus(ctx, r.Client, classifier.Name, &c, nil, nil); err != nil {
			clusterLogger.Error(err, "failed to clear ClassifierReport label status")
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
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

// hasUnManagedLabels returns true if any cluster in statuses has label conflicts.
func hasUnManagedLabels(statuses []libsveltosv1beta1.MachingClusterStatus) bool {
	for i := range statuses {
		if len(statuses[i].UnManagedLabels) > 0 {
			return true
		}
	}
	return false
}
