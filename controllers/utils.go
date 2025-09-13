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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // SA1019: We are unable to update the dependency at this time.
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=debuggingconfigurations,verbs=get;list;watch
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

const (
	accessRequestClassifierLabel = "projectsveltos.io/classifierrequest"
)

var (
	version string
)

func InitScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := libsveltosv1beta1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

// getKeyFromObject returns the Key that can be used in the internal reconciler maps.
func getKeyFromObject(scheme *runtime.Scheme, obj client.Object) *corev1.ObjectReference {
	addTypeInformationToObject(scheme, obj)

	return &corev1.ObjectReference{
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
		APIVersion: obj.GetObjectKind().GroupVersionKind().String(),
	}
}

func addTypeInformationToObject(scheme *runtime.Scheme, obj client.Object) {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		panic(1)
	}

	for _, gvk := range gvks {
		if gvk.Kind == "" {
			continue
		}
		if gvk.Version == "" || gvk.Version == runtime.APIVersionInternal {
			continue
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		break
	}
}

func getClusterRefFromClassifierReport(report *libsveltosv1beta1.ClassifierReport) *corev1.ObjectReference {
	cluster := corev1.ObjectReference{
		Namespace: report.Spec.ClusterNamespace,
		Name:      report.Spec.ClusterName,
	}
	switch report.Spec.ClusterType {
	case libsveltosv1beta1.ClusterTypeCapi:
		cluster.APIVersion = clusterv1.GroupVersion.String()
		cluster.Kind = "Cluster"
	case libsveltosv1beta1.ClusterTypeSveltos:
		cluster.APIVersion = libsveltosv1beta1.GroupVersion.String()
		cluster.Kind = libsveltosv1beta1.SveltosClusterKind
	default:
		panic(1)
	}
	return &cluster
}

func SetVersion(v string) {
	version = v
}

func getVersion() string {
	return version
}

func convertPointerSliceToValueSlice(pointerSlice []*unstructured.Unstructured) []unstructured.Unstructured {
	valueSlice := make([]unstructured.Unstructured, len(pointerSlice))
	for i, ptr := range pointerSlice {
		if ptr != nil {
			valueSlice[i] = *ptr
		}
	}
	return valueSlice
}

// Identifies and removes sveltos-agent deployments that are no longer associated with active clusters
// Identifies and removes classifierReports instances from clusters that are no longer existing
func removeStaleClassifierResources(ctx context.Context, logger logr.Logger) {
	listOptions := []client.ListOption{
		client.MatchingLabels{
			sveltosAgentFeatureLabelKey: sveltosAgent,
		},
	}

	for {
		const five = 5
		time.Sleep(five * time.Minute)

		c := getManagementClusterClient()
		sveltosAgentDeployments := &appsv1.DeploymentList{}
		err := c.List(ctx, sveltosAgentDeployments, listOptions...)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to collect sveltos-agent deployment: %v", err))
			continue
		}

		for i := range sveltosAgentDeployments.Items {
			depl := &sveltosAgentDeployments.Items[i]

			exist, clusterNs, clusterName, clusterType := deplAssociatedClusterExist(ctx, c, depl, logger)
			if !exist {
				_, err = cleanClusterStaleResources(ctx, c, clusterNs, clusterName, clusterType, logger)
				if err != nil {
					logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove sveltos-agent resources: %v", err))
					continue
				}
			}
		}
	}
}

func deplAssociatedClusterExist(ctx context.Context, c client.Client, depl *appsv1.Deployment,
	logger logr.Logger) (exist bool, clusterName, clusterNamespace string, clusterType libsveltosv1beta1.ClusterType) {

	if depl.Labels == nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("driftDetection %s/%s has no label",
			depl.Namespace, depl.Name))
		return true, "", "", ""
	}

	clusterNamespace, ok := depl.Labels[sveltosAgentClusterNamespaceLabel]
	if !ok {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("sveltos-agent %s/%s has no %s label",
			depl.Namespace, depl.Name, sveltosAgentClusterNamespaceLabel))
		return true, "", "", ""
	}

	clusterName, ok = depl.Labels[sveltosAgentClusterNameLabel]
	if !ok {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("sveltos-agent %s/%s has no %s label",
			depl.Namespace, depl.Name, sveltosAgentClusterNameLabel))
		return true, "", "", ""
	}

	clusterTypeString, ok := depl.Labels[sveltosAgentClusterTypeLabel]
	if !ok {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("sveltos-agent %s/%s has no %s label",
			depl.Namespace, depl.Name, sveltosAgentClusterTypeLabel))
		return true, "", "", ""
	}

	if strings.EqualFold(clusterTypeString, string(libsveltosv1beta1.ClusterTypeSveltos)) {
		clusterType = libsveltosv1beta1.ClusterTypeSveltos
	} else if strings.EqualFold(clusterTypeString, string(libsveltosv1beta1.ClusterTypeCapi)) {
		clusterType = libsveltosv1beta1.ClusterTypeCapi
	}

	_, err := clusterproxy.GetCluster(ctx, c, clusterNamespace, clusterName, clusterType)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, clusterNamespace, clusterName, clusterType
		}
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get cluster %s:%s/%s: %v",
			clusterNamespace, clusterName, clusterTypeString, err))
	}

	return true, "", "", ""
}
