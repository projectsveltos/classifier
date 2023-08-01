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
	"crypto/sha256"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/gdexlab/go-render/render"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/classifier/pkg/agent"
	"github.com/projectsveltos/classifier/pkg/scope"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	"github.com/projectsveltos/libsveltos/lib/crd"
	"github.com/projectsveltos/libsveltos/lib/deployer"
	"github.com/projectsveltos/libsveltos/lib/logsettings"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/libsveltos/lib/utils"
)

type getCurrentHash func(classifier *libsveltosv1alpha1.Classifier) []byte

type feature struct {
	id          string
	currentHash getCurrentHash
	deploy      deployer.RequestHandler
	undeploy    deployer.RequestHandler
}

func (r *ClassifierReconciler) deployClassifier(ctx context.Context, classifierScope *scope.ClassifierScope,
	f feature, logger logr.Logger) error {

	classifier := classifierScope.Classifier

	logger = logger.WithValues("classifier", classifier.Name)
	logger.V(logs.LogDebug).Info("request to deploy")

	var errorSeen error
	allDeployed := true
	clusterInfo := make([]libsveltosv1alpha1.ClusterInfo, 0)
	for i := range classifier.Status.ClusterInfo {
		c := classifier.Status.ClusterInfo[i]
		cInfo, err := r.processClassifier(ctx, classifierScope, r.ControlPlaneEndpoint, &c.Cluster, f, logger)
		if err != nil {
			errorSeen = err
		}
		if cInfo != nil {
			clusterInfo = append(clusterInfo, *cInfo)
			if cInfo.Status != libsveltosv1alpha1.SveltosStatusProvisioned {
				allDeployed = false
			}
		}
	}

	// Update Classifier Status
	classifierScope.SetClusterInfo(clusterInfo)

	if errorSeen != nil {
		return errorSeen
	}

	if !allDeployed {
		return fmt.Errorf("request to deploy Classifier is still queued in one ore more clusters")
	}

	return nil
}

func (r *ClassifierReconciler) undeployClassifier(ctx context.Context, classifierScope *scope.ClassifierScope,
	f feature, logger logr.Logger) error {

	classifier := classifierScope.Classifier

	logger.V(logs.LogDebug).Info("request to undeploy")

	clusters := make([]*corev1.ObjectReference, 0)
	// Get list of clusters where Classifier needs to be removed
	for i := range classifier.Status.ClusterInfo {
		c := &classifier.Status.ClusterInfo[i].Cluster

		// Remove any queued entry to deploy/evaluate
		r.Deployer.CleanupEntries(c.Namespace, c.Name, classifier.Name, f.id, clusterproxy.GetClusterType(c), false)

		// If deploying feature is in progress, wait for it to complete.
		// Otherwise, if we cleanup feature while same feature is still being provisioned, if two workers process those request in
		// parallel some resources might be left over.
		if r.Deployer.IsInProgress(c.Namespace, c.Name, classifier.Name, f.id, clusterproxy.GetClusterType(c), false) {
			logger.V(logs.LogDebug).Info("provisioning is in progress")
			return fmt.Errorf("deploying %s still in progress. Wait before cleanup", f.id)
		}

		_, err := clusterproxy.GetCluster(ctx, r.Client, c.Namespace, c.Name, clusterproxy.GetClusterType(c))
		if err != nil {
			if apierrors.IsNotFound(err) {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("cluster %s/%s does not exist", c.Namespace, c.Name))
				continue
			}
			logger.V(logs.LogInfo).Info("failed to get cluster")
			return err
		}
		clusters = append(clusters, c)
	}

	clusterInfo := make([]libsveltosv1alpha1.ClusterInfo, 0)
	for i := range clusters {
		c := clusters[i]
		err := r.removeClassifier(ctx, classifierScope, c, f, logger)
		if err != nil {
			failureMessage := err.Error()
			clusterInfo = append(clusterInfo, libsveltosv1alpha1.ClusterInfo{
				Cluster:        *c,
				Status:         libsveltosv1alpha1.SveltosStatusRemoving,
				FailureMessage: &failureMessage,
			})
		}
	}

	if len(clusterInfo) != 0 {
		matchingClusterStatuses := make([]libsveltosv1alpha1.MachingClusterStatus, len(clusterInfo))
		for i := range clusterInfo {
			matchingClusterStatuses[i] = libsveltosv1alpha1.MachingClusterStatus{
				ClusterRef: clusterInfo[i].Cluster,
			}
		}
		classifierScope.SetMachingClusterStatuses(matchingClusterStatuses)
		return fmt.Errorf("still in the process of removing Classifier from %d clusters",
			len(clusterInfo))
	}

	classifierScope.SetClusterInfo(clusterInfo)

	return nil
}

// classifierHash returns the Classifier hash
func classifierHash(classifier *libsveltosv1alpha1.Classifier) []byte {
	h := sha256.New()
	var config string
	config += render.AsCode(classifier.Spec)
	h.Write([]byte(config))
	return h.Sum(nil)
}

// getClassifierAndClusterClient gets Classifier and the client to access the associated
// Sveltos/CAPI Cluster.
// Returns an err if Classifier or associated Sveltos/CAPI Cluster are marked for deletion, or if an
// error occurs while getting resources.
func getClassifierAndClusterClient(ctx context.Context, clusterNamespace, clusterName, classifierName string,
	clusterType libsveltosv1alpha1.ClusterType, c client.Client, logger logr.Logger,
) (*libsveltosv1alpha1.Classifier, client.Client, error) {

	// Get Classifier that requested this
	classifier := &libsveltosv1alpha1.Classifier{}
	if err := c.Get(ctx,
		types.NamespacedName{Name: classifierName}, classifier); err != nil {
		return nil, nil, err
	}

	if !classifier.DeletionTimestamp.IsZero() {
		logger.V(logs.LogInfo).Info("Classifier is marked for deletion. Nothing to do.")
		// if classifier is marked for deletion, there is nothing to deploy
		return nil, nil, fmt.Errorf("classifier is marked for deletion")
	}

	// Get Cluster
	cluster, err := clusterproxy.GetCluster(ctx, c, clusterNamespace, clusterName, clusterType)
	if err != nil {
		return nil, nil, err
	}

	if !cluster.GetDeletionTimestamp().IsZero() {
		logger.V(logs.LogInfo).Info("cluster is marked for deletion. Nothing to do.")
		// if cluster is marked for deletion, there is nothing to deploy
		return nil, nil, fmt.Errorf("cluster is marked for deletion")
	}

	clusterClient, err := clusterproxy.GetKubernetesClient(ctx, c, clusterNamespace, clusterName,
		"", "", clusterType, logger)
	if err != nil {
		return nil, nil, err
	}

	return classifier, clusterClient, nil
}

func getAccessRequestName(clusterName string, clusterType libsveltosv1alpha1.ClusterType) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(string(clusterType)), clusterName)
}

func createAccessRequest(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType,
	options deployer.Options) error {

	// Currently there is one AccessRequest per cluster for all classifier-agents
	// deployed in such cluster

	missingControlPlaneMessage := "controlplane endpoint is missing"
	if options.HandlerOptions == nil {
		return fmt.Errorf("%s", missingControlPlaneMessage)
	}
	cpEndpoint, ok := options.HandlerOptions[controlplaneendpoint]
	if !ok {
		return fmt.Errorf("%s", missingControlPlaneMessage)
	}

	// the format of this is already validated in main.
	// Format is ^https://[0-9a-zA-Z][0-9a-zA-Z-.]+[0-9a-zA-Z]:\d+$`
	info := strings.Split(cpEndpoint, ":")
	port, _ := strconv.ParseInt(info[2], 10, 32)

	accessRequest := &libsveltosv1alpha1.AccessRequest{}
	err := c.Get(ctx,
		types.NamespacedName{Namespace: clusterNamespace, Name: getAccessRequestName(clusterName, clusterType)}, accessRequest)
	if err != nil {
		if apierrors.IsNotFound(err) {
			accessRequest.Namespace = clusterNamespace
			accessRequest.Name = getAccessRequestName(clusterName, clusterType)
			accessRequest.Labels = map[string]string{
				accessRequestClassifierLabel: "ok",
			}
			accessRequest.Spec = libsveltosv1alpha1.AccessRequestSpec{
				Namespace: clusterNamespace,
				Name:      clusterName,
				Type:      libsveltosv1alpha1.SveltosAgentRequest,
				ControlPlaneEndpoint: clusterv1.APIEndpoint{
					Host: fmt.Sprintf("%s:%s", info[0], info[1]),
					Port: int32(port),
				},
			}
			return c.Create(ctx, accessRequest)
		}
		return err
	}

	return nil
}

// getKubeconfigFromAccessRequest gets the Kubeconfig from AccessRequest
func getKubeconfigFromAccessRequest(ctx context.Context, c client.Client, clusterNamespace, clusterName string,
	clusterType libsveltosv1alpha1.ClusterType, logger logr.Logger) ([]byte, error) {

	accessRequest := &libsveltosv1alpha1.AccessRequest{}
	err := c.Get(ctx,
		types.NamespacedName{Namespace: clusterNamespace, Name: getAccessRequestName(clusterName, clusterType)}, accessRequest)
	if err != nil {
		return nil, err
	}

	if accessRequest.Status.SecretRef == nil {
		logger.V(logs.LogDebug).Info("accessRequest.Status.SecretRef still not set")
		return nil, nil
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: accessRequest.Status.SecretRef.Namespace,
		Name:      accessRequest.Status.SecretRef.Name,
	}

	if err := c.Get(ctx, key, secret); err != nil {
		return nil, err
	}

	for _, contents := range secret.Data {
		return contents, nil
	}

	logger.V(logs.LogDebug).Info("secret does not contain kubeconfig yet")
	return nil, nil
}

func createSecretNamespace(ctx context.Context, c client.Client) error {
	ns := &corev1.Namespace{}
	err := c.Get(ctx, types.NamespacedName{Name: libsveltosv1alpha1.ClassifierSecretNamespace}, ns)
	if err != nil {
		if apierrors.IsNotFound(err) {
			ns.Name = libsveltosv1alpha1.ClassifierSecretNamespace
			return c.Create(ctx, ns)
		}
		return err
	}
	return nil
}

// updateSecretWithAccessManagementKubeconfig creates (or updates) secret in the managed cluster
// containing the Kubeconfig for classifier-agent to access management cluster
func updateSecretWithAccessManagementKubeconfig(ctx context.Context, c client.Client,
	clusterNamespace, clusterName, applicant string, clusterType libsveltosv1alpha1.ClusterType,
	kubeconfig []byte, logger logr.Logger) error {

	// Get Classifier that requested this
	_, remoteClient, err := getClassifierAndClusterClient(ctx, clusterNamespace, clusterName, applicant,
		clusterType, c, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get classifier and cluster client")
		return err
	}

	err = createSecretNamespace(ctx, remoteClient)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: libsveltosv1alpha1.ClassifierSecretNamespace,
		Name:      libsveltosv1alpha1.ClassifierSecretName,
	}

	dataKey := "kubeconfig"
	err = remoteClient.Get(ctx, key, secret)
	if err != nil {
		secret.Namespace = libsveltosv1alpha1.ClassifierSecretNamespace
		secret.Name = libsveltosv1alpha1.ClassifierSecretName
		secret.Data = map[string][]byte{
			dataKey: kubeconfig,
		}
		return remoteClient.Create(ctx, secret)
	}

	currentKubeconfig, ok := secret.Data[dataKey]
	if ok && reflect.DeepEqual(currentKubeconfig, kubeconfig) {
		return nil
	}

	secret.Data[dataKey] = kubeconfig
	return remoteClient.Update(ctx, secret)
}

// deployCRDs deploys all Sveltos CRDs needed by sveltos-agent
func deployCRDs(ctx context.Context, c client.Client, clusterNamespace, clusterName string,
	clusterType libsveltosv1alpha1.ClusterType, logger logr.Logger) error {

	remoteRestConfig, err := clusterproxy.GetKubernetesRestConfig(ctx, c, clusterNamespace, clusterName,
		"", "", clusterType, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get cluster rest config")
		return err
	}

	logger.V(logs.LogDebug).Info("deploy classifier CRD")
	err = deployClassifierCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy classifierReport CRD")
	err = deployClassifierReportCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy healthCheck CRD")
	err = deployHealthCheckCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy healthCheckReport CRD")
	err = deployHealthCheckReportCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy eventsource CRD")
	err = deployEventSourceCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy eventReport CRD")
	err = deployEventReportCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy debuggingConfiguration CRD")
	err = deployDebuggingConfigurationCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy reloader CRD")
	err = deployReloaderCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("deploy reloaderReport CRD")
	err = deployReloaderReportCRD(ctx, remoteRestConfig, logger)
	if err != nil {
		return err
	}

	return nil
}

// deployClassifierWithKubeconfigInCluster does following things in order:
// - create (if one does not exist already) AccessRequest
// - get Kubeconfig to access management cluster from AccessRequest
// - create/update secret in the managed cluster with kubeconfig to access management cluster
// - deploy classifier-agent
func deployClassifierWithKubeconfigInCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName, applicant, featureID string,
	clusterType libsveltosv1alpha1.ClusterType, options deployer.Options, logger logr.Logger) error {

	logger = logger.WithValues("classifier", applicant)
	logger.V(logs.LogDebug).Info("deploy classifier: send reports mode")

	if err := createAccessRequest(ctx, c, clusterNamespace, clusterName, clusterType, options); err != nil {
		return err
	}

	kubeconfig, err := getKubeconfigFromAccessRequest(ctx, c, clusterNamespace, clusterName, clusterType, logger)
	if err != nil {
		return err
	}

	if kubeconfig == nil {
		return fmt.Errorf("accessRequest kubeconfig not present yet")
	}

	err = updateSecretWithAccessManagementKubeconfig(ctx, c, clusterNamespace, clusterName, applicant, clusterType,
		kubeconfig, logger)
	if err != nil {
		return err
	}

	err = deployCRDs(ctx, c, clusterNamespace, clusterName, clusterType, logger)
	if err != nil {
		return err
	}

	remoteRestConfig, err := clusterproxy.GetKubernetesRestConfig(ctx, c, clusterNamespace, clusterName,
		"", "", clusterType, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get cluster rest config")
		return err
	}

	logger.V(logs.LogDebug).Info("Deploying sveltos agent")
	// Deploy SveltosAgent
	err = deploySveltosAgent(ctx, remoteRestConfig, clusterNamespace, clusterName, "send-reports", clusterType, logger)
	if err != nil {
		return err
	}

	// Get Classifier that requested this
	classifier, remoteClient, err := getClassifierAndClusterClient(ctx, clusterNamespace, clusterName, applicant, clusterType,
		c, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get classifier and cluster client")
		return err
	}

	// Deploy Classifier instance
	err = deployClassifierInstance(ctx, remoteClient, classifier, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("successuflly deployed classifier CRD and instance")
	return nil
}

func deployClassifierInCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName, applicant, featureID string,
	clusterType libsveltosv1alpha1.ClusterType, options deployer.Options, logger logr.Logger) error {

	logger = logger.WithValues("classifier", applicant)
	logger.V(logs.LogDebug).Info("deploy classifier: do not send reports mode")

	remoteRestConfig, err := clusterproxy.GetKubernetesRestConfig(ctx, c, clusterNamespace, clusterName,
		"", "", clusterType, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get cluster rest config")
		return err
	}

	err = deployCRDs(ctx, c, clusterNamespace, clusterName, clusterType, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("Deploying sveltos agent")
	// Deploy SveltosAgent
	err = deploySveltosAgent(ctx, remoteRestConfig, clusterNamespace, clusterName, "do-not-send-reports", clusterType, logger)
	if err != nil {
		return err
	}

	// Get Classifier that requested this
	classifier, remoteClient, err := getClassifierAndClusterClient(ctx, clusterNamespace, clusterName, applicant, clusterType,
		c, logger)
	if err != nil {
		logger.V(logs.LogInfo).Error(err, "failed to get classifier and cluster client")
		return err
	}

	// Deploy Classifier instance
	err = deployClassifierInstance(ctx, remoteClient, classifier, logger)
	if err != nil {
		return err
	}

	logger.V(logs.LogDebug).Info("successuflly deployed classifier CRD and instance")
	return nil
}

// undeployClassifierFromCluster deletes Classifier instance from cluster
func undeployClassifierFromCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName, applicant, featureID string,
	clusterType libsveltosv1alpha1.ClusterType, options deployer.Options, logger logr.Logger) error {

	logger = logger.WithValues("classifier", applicant)
	logger = logger.WithValues("cluster", fmt.Sprintf("%s/%s", clusterNamespace, clusterName))
	logger.V(logs.LogDebug).Info("Undeploy classifier")

	// Get Cluster
	cluster, err := clusterproxy.GetCluster(ctx, c, clusterNamespace, clusterName, clusterType)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(logs.LogDebug).Info("cluster not found. nothing to clean up")
			return nil
		}
		return err
	}

	if !cluster.GetDeletionTimestamp().IsZero() {
		logger.V(logs.LogInfo).Info("cluster is marked for deletion. Nothing to do.")
		return nil
	}

	remoteClient, err := clusterproxy.GetKubernetesClient(ctx, c, clusterNamespace, clusterName,
		"", "", clusterType, logger)
	if err != nil {
		logger.V(logs.LogDebug).Error(err, "failed to get cluster client")
		return err
	}

	logger.V(logs.LogDebug).Info("Undeploy classifier from cluster")

	currentClassifier := &libsveltosv1alpha1.Classifier{}
	err = remoteClient.Get(ctx, types.NamespacedName{Name: applicant}, currentClassifier)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(logs.LogInfo).Info("classifier not found")
			return nil
		}
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get classifier. Err: %v", err))
		return err
	}

	logger.V(logs.LogDebug).Info("remove classifier instance")
	return remoteClient.Delete(ctx, currentClassifier)
}

func (r *ClassifierReconciler) convertResultStatus(result deployer.Result) *libsveltosv1alpha1.SveltosFeatureStatus {
	switch result.ResultStatus {
	case deployer.Deployed:
		s := libsveltosv1alpha1.SveltosStatusProvisioned
		return &s
	case deployer.Failed:
		s := libsveltosv1alpha1.SveltosStatusFailed
		return &s
	case deployer.InProgress:
		s := libsveltosv1alpha1.SveltosStatusProvisioning
		return &s
	case deployer.Removed:
		s := libsveltosv1alpha1.SveltosStatusRemoved
		return &s
	case deployer.Unavailable:
		return nil
	}

	return nil
}

// getClassifierInClusterHashAndStatus returns the hash of the Classifier that was deployed in a given
// Cluster (if ever deployed)
func (r *ClassifierReconciler) getClassifierInClusterHashAndStatus(classifier *libsveltosv1alpha1.Classifier,
	cluster *corev1.ObjectReference) ([]byte, *libsveltosv1alpha1.SveltosFeatureStatus) {

	for i := range classifier.Status.ClusterInfo {
		cInfo := &classifier.Status.ClusterInfo[i]
		if cInfo.Cluster.Namespace == cluster.Namespace &&
			cInfo.Cluster.Name == cluster.Name &&
			cInfo.Cluster.APIVersion == cluster.APIVersion &&
			cInfo.Cluster.Kind == cluster.Kind {

			return cInfo.Hash, &cInfo.Status
		}
	}

	return nil, nil
}

// isPaused returns true if Sveltos/CAPI Cluster is paused or ClusterSummary has paused annotation.
func (r *ClassifierReconciler) isPaused(ctx context.Context, cluster *corev1.ObjectReference,
	classifier *libsveltosv1alpha1.Classifier) (bool, error) {

	isClusterPaused, err := clusterproxy.IsClusterPaused(ctx, r.Client, cluster.Namespace, cluster.Name,
		clusterproxy.GetClusterType(cluster))

	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	if isClusterPaused {
		return true, nil
	}

	return annotations.HasPaused(classifier), nil
}

// removeClassifier removes Classifier instance from cluster
func (r *ClassifierReconciler) removeClassifier(ctx context.Context, classifierScope *scope.ClassifierScope,
	cluster *corev1.ObjectReference, f feature, logger logr.Logger,
) error {

	classifier := classifierScope.Classifier

	paused, err := r.isPaused(ctx, cluster, classifierScope.Classifier)
	if err != nil {
		return err
	}
	if paused {
		logger.V(logs.LogInfo).Info("cluster is paused. Do nothing.")
		return nil
	}

	// If deploying feature is in progress, wait for it to complete.
	// Otherwise, if we undeploy feature while same feature is still being deployed, if two workers process those request in
	// parallel some resources might end stale.
	if r.Deployer.IsInProgress(cluster.Namespace, cluster.Name, classifier.Name, f.id, clusterproxy.GetClusterType(cluster), false) {
		logger.V(logs.LogDebug).Info("deploy is in progress")
		return fmt.Errorf("deploy of %s in cluster still in progress. Wait before redeploying", f.id)
	}

	result := r.Deployer.GetResult(ctx, cluster.Namespace, cluster.Name, classifier.Name, f.id, clusterproxy.GetClusterType(cluster), true)
	status := r.convertResultStatus(result)

	if status != nil {
		if *status == libsveltosv1alpha1.SveltosStatusProvisioning {
			return fmt.Errorf("feature is still being removed")
		}
		if *status == libsveltosv1alpha1.SveltosStatusRemoved {
			return nil
		}
	} else {
		logger.V(logs.LogDebug).Info("no result is available")
	}

	logger.V(logs.LogDebug).Info("queueing request to un-deploy")
	if err := r.Deployer.Deploy(ctx, cluster.Namespace, cluster.Name, classifier.Name, f.id, clusterproxy.GetClusterType(cluster),
		true, undeployClassifierFromCluster, programDuration, deployer.Options{}); err != nil {
		return err
	}

	return fmt.Errorf("cleanup request is queued")
}

// canProceed returns true if cluster is ready to be programmed and it is not paused.
func (r *ClassifierReconciler) canProceed(ctx context.Context, classifierScope *scope.ClassifierScope,
	cluster *corev1.ObjectReference, logger logr.Logger) (bool, error) {

	logger = logger.WithValues("clusterNamespace", cluster.Namespace, "clusterName", cluster.Name)

	paused, err := r.isPaused(ctx, cluster, classifierScope.Classifier)
	if err != nil {
		return false, err
	}

	if paused {
		logger.V(logs.LogDebug).Info("Cluster is paused")
		return false, nil
	}

	ready, err := clusterproxy.IsClusterReadyToBeConfigured(ctx, r.Client, cluster, classifierScope.Logger)
	if err != nil {
		return false, err
	}

	if !ready {
		logger.V(logs.LogInfo).Info("Cluster is not ready yet")
		return false, nil
	}

	return true, nil
}

// getCurrentHash gets current hash.
// It considers Classifier and if mode is ClassifierReportMode == AgentSendReportsNoGateway also
// the kubeconfig to access management cluster
func (r *ClassifierReconciler) getCurrentHash(ctx context.Context, classifierScope *scope.ClassifierScope,
	cpEndpoint string, cluster *corev1.ObjectReference, f feature, logger logr.Logger) ([]byte, error) {
	// Get Classifier Spec hash (at this very precise moment)
	currentHash := f.currentHash(classifierScope.Classifier)
	var kubeconfig []byte
	var err error
	if r.ClassifierReportMode == AgentSendReportsNoGateway {
		kubeconfig, err = getKubeconfigFromAccessRequest(ctx, r.Client, cluster.Namespace, cluster.Name,
			clusterproxy.GetClusterType(cluster), logger)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
		if kubeconfig != nil {
			h := sha256.New()
			config := string(currentHash)
			config += string(kubeconfig)
			config += cpEndpoint
			h.Write([]byte(config))
			currentHash = h.Sum(nil)
		}
	}
	return currentHash, nil
}

// processClassifier detect whether it is needed to deploy Classifier in current passed cluster.
func (r *ClassifierReconciler) processClassifier(ctx context.Context, classifierScope *scope.ClassifierScope,
	cpEndpoint string, cluster *corev1.ObjectReference, f feature, logger logr.Logger,
) (*libsveltosv1alpha1.ClusterInfo, error) {

	// Get Classifier Spec hash (at this very precise moment)
	currentHash, err := r.getCurrentHash(ctx, classifierScope, cpEndpoint, cluster, f, logger)
	if err != nil {
		return nil, err
	}
	classifier := classifierScope.Classifier

	var proceed bool
	proceed, err = r.canProceed(ctx, classifierScope, cluster, logger)
	if err != nil {
		return nil, err
	} else if !proceed {
		return nil, nil
	}

	// If undeploying feature is in progress, wait for it to complete.
	// Otherwise, if we redeploy feature while same feature is still being cleaned up, if two workers process those request in
	// parallel some resources might end up missing.
	if r.Deployer.IsInProgress(cluster.Namespace, cluster.Name, classifier.Name, f.id, clusterproxy.GetClusterType(cluster), true) {
		logger.V(logs.LogDebug).Info("cleanup is in progress")
		return nil, fmt.Errorf("cleanup of %s in cluster still in progress. Wait before redeploying", f.id)
	}

	// Get the Classifier hash when Classifier was last deployed in this cluster (if ever)
	hash, currentStatus := r.getClassifierInClusterHashAndStatus(classifier, cluster)
	isConfigSame := reflect.DeepEqual(hash, currentHash)
	if !isConfigSame {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("Classifier has changed. Current hash %x. Previous hash %x",
			currentHash, hash))
	}

	var status *libsveltosv1alpha1.SveltosFeatureStatus
	var result deployer.Result

	if isConfigSame {
		logger.V(logs.LogInfo).Info("classifier and kubeconfig have has not changed")
		result = r.Deployer.GetResult(ctx, cluster.Namespace, cluster.Name, classifier.Name, f.id,
			clusterproxy.GetClusterType(cluster), false)
		status = r.convertResultStatus(result)
	}

	if status != nil {
		logger.V(logs.LogDebug).Info("result is available. updating status.")
		var errorMessage string
		if result.Err != nil {
			errorMessage = result.Err.Error()
		}
		clusterInfo := &libsveltosv1alpha1.ClusterInfo{
			Cluster:        *cluster,
			Status:         *status,
			Hash:           currentHash,
			FailureMessage: &errorMessage,
		}

		if *status == libsveltosv1alpha1.SveltosStatusProvisioned {
			return clusterInfo, nil
		}
		if *status == libsveltosv1alpha1.SveltosStatusProvisioning {
			return clusterInfo, fmt.Errorf("classifier is still being provisioned")
		}
	} else if isConfigSame && currentStatus != nil && *currentStatus == libsveltosv1alpha1.SveltosStatusProvisioned {
		logger.V(logs.LogInfo).Info("already deployed")
		s := libsveltosv1alpha1.SveltosStatusProvisioned
		status = &s
	} else {
		logger.V(logs.LogInfo).Info("no result is available. queue job and mark status as provisioning")
		s := libsveltosv1alpha1.SveltosStatusProvisioning
		status = &s

		options := deployer.Options{}
		var handler deployer.RequestHandler
		handler = deployClassifierInCluster
		if r.ClassifierReportMode == AgentSendReportsNoGateway {
			handler = deployClassifierWithKubeconfigInCluster
			options.HandlerOptions = map[string]string{controlplaneendpoint: r.ControlPlaneEndpoint}
		}
		// Getting here means either Classifier failed to be deployed or Classifier has changed.
		// Classifier must be (re)deployed.
		if err := r.Deployer.Deploy(ctx, cluster.Namespace, cluster.Name,
			classifier.Name, f.id, clusterproxy.GetClusterType(cluster), false, handler, programDuration, options); err != nil {
			return nil, err
		}
	}

	clusterInfo := &libsveltosv1alpha1.ClusterInfo{
		Cluster:        *cluster,
		Status:         *status,
		Hash:           currentHash,
		FailureMessage: nil,
	}

	return clusterInfo, nil
}

// deployClassifierCRD deploys Classifier CRD in remote cluster
func deployClassifierCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	classifierCRD, err := utils.GetUnstructured(crd.GetClassifierCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get Classifier CRD unstructured: %v", err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, classifierCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, classifierCRD.GetName(), classifierCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply Classifier CRD: %v", err))
		return err
	}

	return nil
}

// deployClassifierReportCRD deploys ClassifierReport CRD in remote cluster
func deployClassifierReportCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	classifierReportCRD, err := utils.GetUnstructured(crd.GetClassifierReportCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get ClassifierReport CRD unstructured: %v",
			err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, classifierReportCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, classifierReportCRD.GetName(), classifierReportCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply ClassifierReport CRD: %v", err))
		return err
	}

	return nil
}

// deployHealthCheckCRD deploys HealthCheck CRD in remote cluster
func deployHealthCheckCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	healthCheckCRD, err := utils.GetUnstructured(crd.GetHealthCheckCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get HealthCheck CRD unstructured: %v", err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, healthCheckCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, healthCheckCRD.GetName(), healthCheckCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply healthCheck CRD: %v", err))
		return err
	}

	return nil
}

// deployHealthCheckeportCRD deploys HealthCheckReport CRD in remote cluster
func deployHealthCheckReportCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	healthCheckReportCRD, err := utils.GetUnstructured(crd.GetHealthCheckReportCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get healthCheckReport CRD unstructured: %v",
			err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, healthCheckReportCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, healthCheckReportCRD.GetName(), healthCheckReportCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply ClassifierReport CRD: %v", err))
		return err
	}

	return nil
}

// deployEventSourceCRD deploys EventSource CRD in remote cluster
func deployEventSourceCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	eventSourceCRD, err := utils.GetUnstructured(crd.GetEventSourceCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get eventSourceCRD CRD unstructured: %v", err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, eventSourceCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, eventSourceCRD.GetName(), eventSourceCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply eventSourceCRD CRD: %v", err))
		return err
	}

	return nil
}

// deployEventReportCRD deploys EventReport CRD in remote cluster
func deployEventReportCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	eventReportCRD, err := utils.GetUnstructured(crd.GetEventReportCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get eventReportCRD CRD unstructured: %v",
			err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, eventReportCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, eventReportCRD.GetName(), eventReportCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply ClassifierReport CRD: %v", err))
		return err
	}

	return nil
}

func deployClassifierInstance(ctx context.Context, remoteClient client.Client,
	classifier *libsveltosv1alpha1.Classifier, logger logr.Logger) error {

	logger.V(logs.LogDebug).Info("deploy classifier instance")
	currentClassifier := &libsveltosv1alpha1.Classifier{}
	err := remoteClient.Get(ctx, types.NamespacedName{Name: classifier.Name}, currentClassifier)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(logsettings.LogDebug).Info("classifier instance not present. creating it.")
			toDeployClassifier := &libsveltosv1alpha1.Classifier{
				ObjectMeta: metav1.ObjectMeta{
					Name: classifier.Name,
					Annotations: map[string]string{
						libsveltosv1alpha1.DeployedBySveltosAnnotation: "true",
					},
				},
				Spec: classifier.Spec,
			}
			return remoteClient.Create(ctx, toDeployClassifier)
		}
		return err
	}

	currentClassifier.Spec = classifier.Spec
	currentClassifier.Annotations = map[string]string{
		libsveltosv1alpha1.DeployedBySveltosAnnotation: "true",
	}
	return remoteClient.Update(ctx, currentClassifier)
}

// deployDebuggingConfigurationCRD deploys DebuggingConfiguration CRD in remote cluster
func deployDebuggingConfigurationCRD(ctx context.Context, remoteRestConfig *rest.Config,
	logger logr.Logger) error {

	dcCRD, err := utils.GetUnstructured(crd.GetDebuggingConfigurationCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get DebuggingConfiguration CRD unstructured: %v",
			err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, dcCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, dcCRD.GetName(), dcCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply DebuggingConfiguration CRD: %v", err))
		return err
	}

	return nil
}

func deployReloaderCRD(ctx context.Context, remoteRestConfig *rest.Config, logger logr.Logger) error {
	// Deploy Reloader CRD
	reloaderCRD, err := utils.GetUnstructured(crd.GetReloaderCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get Reloader CRD unstructured: %v", err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, reloaderCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(
			fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, reloaderCRD.GetName(), reloaderCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply Reloader CRD: %v", err))
		return err
	}

	return nil
}

func deployReloaderReportCRD(ctx context.Context, remoteRestConfig *rest.Config, logger logr.Logger) error {
	// Deploy Reloader CRD
	reloaderReportCRD, err := utils.GetUnstructured(crd.GetReloaderReportCRDYAML())
	if err != nil {
		logger.V(logsettings.LogInfo).Info(
			fmt.Sprintf("failed to get ReloaderReport CRD unstructured: %v", err))
		return err
	}

	dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, reloaderReportCRD.GroupVersionKind(), "")
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
		return err
	}

	options := metav1.ApplyOptions{
		FieldManager: "application/apply-patch",
	}
	_, err = dr.Apply(ctx, reloaderReportCRD.GetName(), reloaderReportCRD, options)
	if err != nil {
		logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply ReloaderReport CRD: %v", err))
		return err
	}

	return nil
}

func deploySveltosAgent(ctx context.Context, remoteRestConfig *rest.Config,
	clusterNamespace, clusterName, mode string, clusterType libsveltosv1alpha1.ClusterType, logger logr.Logger) error {

	agentYAML := string(agent.GetSveltosAgentYAML())

	if mode != "do-not-send-reports" {
		agentYAML = strings.ReplaceAll(agentYAML, "do-not-send-reports", "send-reports")
	}

	agentYAML = strings.ReplaceAll(agentYAML, "cluster-namespace=", fmt.Sprintf("cluster-namespace=%s", clusterNamespace))
	agentYAML = strings.ReplaceAll(agentYAML, "cluster-name=", fmt.Sprintf("cluster-name=%s", clusterName))
	agentYAML = strings.ReplaceAll(agentYAML, "cluster-type=", fmt.Sprintf("cluster-type=%s", clusterType))

	const separator = "---"
	elements := strings.Split(agentYAML, separator)
	for i := range elements {
		policy, err := utils.GetUnstructured([]byte(elements[i]))
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to parse classifier agent yaml: %v", err))
			return err
		}

		dr, err := utils.GetDynamicResourceInterface(remoteRestConfig, policy.GroupVersionKind(), policy.GetNamespace())
		if err != nil {
			logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to get dynamic client: %v", err))
			return err
		}

		options := metav1.ApplyOptions{
			FieldManager: "application/apply-patch",
		}

		_, err = dr.Apply(ctx, policy.GetName(), policy, options)
		if err != nil {
			logger.V(logsettings.LogInfo).Info(fmt.Sprintf("failed to apply policy Kind: %s Name: %s: %v",
				policy.GetKind(), policy.GetName(), err))
			return err
		}
	}

	return nil
}
