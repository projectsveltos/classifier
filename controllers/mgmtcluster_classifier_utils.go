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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	lua "github.com/yuin/gopher-lua"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	sveltoscel "github.com/projectsveltos/libsveltos/lib/cel"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	sveltoslua "github.com/projectsveltos/libsveltos/lib/lua"
)

// clusterRef is the struct that classificationLua must return entries of.
type clusterRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
}

// resourceMatchStatus mirrors the return type of per-resource Lua evaluate scripts.
type resourceMatchStatus struct {
	Matching bool   `json:"matching"`
	Message  string `json:"message"`
}

// fetchResourcesForSelector lists all management-cluster resources matching rs,
// applying name, namespace, LabelFilter, Selector, per-resource Evaluate and EvaluateCEL filters.
func fetchResourcesForSelector(ctx context.Context, c client.Client,
	rs *libsveltosv1beta1.ResourceSelector, logger logr.Logger) ([]*unstructured.Unstructured, error) {

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   rs.Group,
		Version: rs.Version,
		Kind:    rs.Kind + "List",
	})

	listOpts := []client.ListOption{}
	if rs.Namespace != "" {
		listOpts = append(listOpts, client.InNamespace(rs.Namespace))
	}
	if rs.Selector != nil {
		sel, err := metav1.LabelSelectorAsSelector(rs.Selector)
		if err != nil {
			return nil, fmt.Errorf("invalid selector for %s/%s/%s: %w", rs.Group, rs.Version, rs.Kind, err)
		}
		listOpts = append(listOpts, client.MatchingLabelsSelector{Selector: sel})
	}

	if err := c.List(ctx, list, listOpts...); err != nil {
		return nil, err
	}

	// Compile per-resource Lua once so we don't re-parse it for every resource.
	var luaState *lua.LState
	if rs.Evaluate != "" {
		luaState = lua.NewState()
		sveltoslua.LoadModulesAndRegisterMethods(luaState)
		if err := luaState.DoString(rs.Evaluate); err != nil {
			luaState.Close()
			return nil, fmt.Errorf("failed to compile ResourceSelector.Evaluate: %w", err)
		}
		defer luaState.Close()
	}

	result := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		u := &list.Items[i]

		if !u.GetDeletionTimestamp().IsZero() {
			continue
		}
		if rs.Name != "" && u.GetName() != rs.Name {
			continue
		}
		if !doesMatchLabelFilters(u, rs.LabelFilters) {
			continue
		}

		match, err := isResourceMatch(u, rs, luaState, logger)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to evaluate resource %s/%s: %v",
				u.GetNamespace(), u.GetName(), err))
			continue
		}
		if match {
			result = append(result, u)
		}
	}

	return result, nil
}

// isResourceMatch evaluates CEL rules and per-resource Lua for u.
// If neither filter is defined the resource matches by default.
func isResourceMatch(u *unstructured.Unstructured, rs *libsveltosv1beta1.ResourceSelector,
	luaState *lua.LState, logger logr.Logger) (bool, error) {

	if len(rs.EvaluateCEL) == 0 && luaState == nil {
		return true, nil
	}

	if len(rs.EvaluateCEL) > 0 {
		celMatch, err := sveltoscel.EvaluateRules(u, rs.EvaluateCEL, logger)
		if err != nil {
			return false, err
		}
		if celMatch {
			return true, nil
		}
	}

	if luaState != nil {
		return isMatchForEvaluateScript(u, luaState, logger)
	}

	return false, nil
}

// doesMatchLabelFilters returns true when u satisfies every LabelFilter in filters.
func doesMatchLabelFilters(u *unstructured.Unstructured, filters []libsveltosv1beta1.LabelFilter) bool {
	resourceLabels := labels.Set(u.GetLabels())
	for i := range filters {
		f := &filters[i]
		switch f.Operation {
		case libsveltosv1beta1.OperationEqual:
			v, ok := resourceLabels[f.Key]
			if !ok || v != f.Value {
				return false
			}
		case libsveltosv1beta1.OperationDifferent:
			v, ok := resourceLabels[f.Key]
			if ok && v == f.Value {
				return false
			}
		case libsveltosv1beta1.OperationHas:
			if _, ok := resourceLabels[f.Key]; !ok {
				return false
			}
		case libsveltosv1beta1.OperationDoesNotHave:
			if _, ok := resourceLabels[f.Key]; ok {
				return false
			}
		}
	}
	return true
}

// isMatchForEvaluateScript calls the pre-compiled Lua state's evaluate function for u.
func isMatchForEvaluateScript(u *unstructured.Unstructured, l *lua.LState, logger logr.Logger) (bool, error) {
	obj := sveltoslua.MapToTable(u.UnstructuredContent())

	if err := l.CallByParam(lua.P{
		Fn:      l.GetGlobal("evaluate"),
		NRet:    1,
		Protect: true,
	}, obj); err != nil {
		return false, err
	}

	lv := l.Get(-1)
	l.Pop(1)

	tbl, ok := lv.(*lua.LTable)
	if !ok {
		return false, fmt.Errorf("%s", sveltoslua.LuaTableError)
	}

	goResult := sveltoslua.ToGoValue(tbl)
	resultJSON, err := json.Marshal(goResult)
	if err != nil {
		return false, err
	}

	var status resourceMatchStatus
	if err := json.Unmarshal(resultJSON, &status); err != nil {
		return false, err
	}
	if status.Message != "" {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("Lua evaluate message: %s", status.Message))
	}
	return status.Matching, nil
}

// runClassificationLua calls classificationLua's evaluate function with all matched resources
// and returns the cluster references it yields.
func runClassificationLua(luaScript string, resources []*unstructured.Unstructured,
	logger logr.Logger) ([]clusterRef, error) {

	l := lua.NewState()
	defer l.Close()
	sveltoslua.LoadModulesAndRegisterMethods(l)

	if err := l.DoString(luaScript); err != nil {
		return nil, fmt.Errorf("failed to compile classificationLua: %w", err)
	}

	argTable := l.CreateTable(len(resources), 0)
	for i := range resources {
		obj := sveltoslua.MapToTable(resources[i].UnstructuredContent())
		l.RawSet(argTable, lua.LNumber(i+1), obj)
	}
	l.SetGlobal("resources", argTable)

	if err := l.CallByParam(lua.P{
		Fn:      l.GetGlobal("evaluate"),
		NRet:    1,
		Protect: true,
	}, argTable); err != nil {
		return nil, fmt.Errorf("classificationLua evaluate failed: %w", err)
	}

	lv := l.Get(-1)
	l.Pop(1)

	tbl, ok := lv.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("%s", sveltoslua.LuaTableError)
	}

	goResult := sveltoslua.ToGoValue(tbl)
	resultJSON, err := json.Marshal(goResult)
	if err != nil {
		return nil, err
	}

	var rawResult interface{}
	if err := json.Unmarshal(resultJSON, &rawResult); err != nil {
		return nil, fmt.Errorf("failed to parse classificationLua return: %w", err)
	}

	return convertToClusterRefs(rawResult, logger)
}

// convertToClusterRefs converts the raw Lua return value to []clusterRef.
// gopher-lua's ToGoValue returns array tables as map[string]interface{} with numeric string keys.
func convertToClusterRefs(raw interface{}, logger logr.Logger) ([]clusterRef, error) {
	switch v := raw.(type) {
	case []interface{}:
		return parseClusterRefSlice(v, logger)
	case map[string]interface{}:
		ordered := make([]interface{}, 0, len(v))
		for i := 1; i <= len(v); i++ {
			entry, ok := v[fmt.Sprintf("%d", i)]
			if !ok {
				break
			}
			ordered = append(ordered, entry)
		}
		return parseClusterRefSlice(ordered, logger)
	default:
		if raw == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected classificationLua return type %T", raw)
	}
}

func parseClusterRefSlice(items []interface{}, logger logr.Logger) ([]clusterRef, error) {
	refs := make([]clusterRef, 0, len(items))
	for _, item := range items {
		entryJSON, err := json.Marshal(item)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to marshal cluster ref entry: %v", err))
			continue
		}
		var ref clusterRef
		if err := json.Unmarshal(entryJSON, &ref); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to parse cluster ref entry: %v", err))
			continue
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// listMgmtClassifierReports returns all ManagementClusterClassifierReports labeled for classifierName.
func listMgmtClassifierReports(ctx context.Context, c client.Client,
	classifierName string) ([]libsveltosv1beta1.ManagementClusterClassifierReport, error) {

	list := &libsveltosv1beta1.ManagementClusterClassifierReportList{}
	if err := c.List(ctx, list, client.MatchingLabels{
		libsveltosv1beta1.ManagementClusterClassifierNameLabel: classifierName,
	}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// getMgmtClassifierReport fetches the ManagementClusterClassifierReport for the given (classifier, cluster) pair.
func getMgmtClassifierReport(ctx context.Context, c client.Client,
	classifierName, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType) (*libsveltosv1beta1.ManagementClusterClassifierReport, error) {

	name := libsveltosv1beta1.GetManagementClusterClassifierReportName(classifierName, clusterName, &clusterType)
	report := &libsveltosv1beta1.ManagementClusterClassifierReport{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: clusterNamespace, Name: name}, report); err != nil {
		return nil, err
	}
	return report, nil
}

// ensureMgmtClassifierReport creates the report if it does not yet exist. Idempotent.
func ensureMgmtClassifierReport(ctx context.Context, c client.Client,
	classifierName, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType) error {

	name := libsveltosv1beta1.GetManagementClusterClassifierReportName(classifierName, clusterName, &clusterType)
	existing := &libsveltosv1beta1.ManagementClusterClassifierReport{}
	err := c.Get(ctx, types.NamespacedName{Namespace: clusterNamespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	report := &libsveltosv1beta1.ManagementClusterClassifierReport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: clusterNamespace,
			Name:      name,
			Labels: libsveltosv1beta1.GetManagementClusterClassifierReportLabels(
				classifierName, clusterName, &clusterType),
		},
		Spec: libsveltosv1beta1.ManagementClusterClassifierReportSpec{
			ClassifierName:   classifierName,
			ClusterNamespace: clusterNamespace,
			ClusterName:      clusterName,
			ClusterType:      clusterType,
		},
	}
	if createErr := c.Create(ctx, report); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
		return createErr
	}
	return nil
}

// deleteMgmtClassifierReport deletes the report for the given pair if it exists.
func deleteMgmtClassifierReport(ctx context.Context, c client.Client,
	classifierName, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType) error {

	report, err := getMgmtClassifierReport(ctx, c, classifierName, clusterNamespace, clusterName, clusterType)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return c.Delete(ctx, report)
}

// updateMgmtClassifierReportStatus patches the report status with the current label ownership state.
func updateMgmtClassifierReportStatus(ctx context.Context, c client.Client,
	classifierName, clusterNamespace, clusterName string,
	clusterType libsveltosv1beta1.ClusterType,
	managed []string, unmanaged []libsveltosv1beta1.UnManagedLabel) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		report, err := getMgmtClassifierReport(ctx, c, classifierName, clusterNamespace, clusterName, clusterType)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		patch := client.MergeFrom(report.DeepCopy())
		report.Status.ManagedLabels = managed
		report.Status.UnManagedLabels = unmanaged
		return c.Status().Patch(ctx, report, patch)
	})
}

// applyLabelsToCluster adds classifierLabels to the cluster.
func applyLabelsToCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType,
	classifierLabels []libsveltosv1beta1.ClassifierLabel, logger logr.Logger) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := getClusterObject(ctx, c, clusterNamespace, clusterName, clusterType)
		if err != nil {
			return err
		}

		current := cluster.GetLabels()
		if current == nil {
			current = make(map[string]string)
		}
		for i := range classifierLabels {
			current[classifierLabels[i].Key] = classifierLabels[i].Value
		}
		cluster.SetLabels(current)

		if err := c.Update(ctx, cluster); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to apply labels on cluster %s/%s: %v",
				clusterNamespace, clusterName, err))
			return err
		}
		return nil
	})
}

// removeLabelsFromCluster deletes the given label keys from the cluster.
func removeLabelsFromCluster(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType,
	labelKeys []string, logger logr.Logger) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := getClusterObject(ctx, c, clusterNamespace, clusterName, clusterType)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		current := cluster.GetLabels()
		if current == nil {
			return nil
		}
		changed := false
		for _, k := range labelKeys {
			if _, ok := current[k]; ok {
				delete(current, k)
				changed = true
			}
		}
		if !changed {
			return nil
		}
		cluster.SetLabels(current)

		if err := c.Update(ctx, cluster); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to remove labels from cluster %s/%s: %v",
				clusterNamespace, clusterName, err))
			return err
		}
		return nil
	})
}

// getClusterObject fetches the cluster as an Unstructured object regardless of type.
func getClusterObject(ctx context.Context, c client.Client,
	clusterNamespace, clusterName string, clusterType libsveltosv1beta1.ClusterType) (*unstructured.Unstructured, error) {

	u := &unstructured.Unstructured{}
	switch clusterType {
	case libsveltosv1beta1.ClusterTypeCapi:
		u.SetGroupVersionKind(clusterv1.GroupVersion.WithKind("Cluster"))
	case libsveltosv1beta1.ClusterTypeSveltos:
		u.SetGroupVersionKind(libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind))
	default:
		return nil, fmt.Errorf("unknown cluster type %q", clusterType)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: clusterNamespace, Name: clusterName}, u); err != nil {
		return nil, err
	}
	return u, nil
}

// mgmtClassifierAsClassifier returns a *Classifier whose Name carries the "mgmt:" prefix so that
// the keymanager tracks ManagementClusterClassifier entries without collision with Classifier names.
// The colon character is not valid in Kubernetes resource names, making the collision structurally impossible.
func mgmtClassifierAsClassifier(mcc *libsveltosv1beta1.ManagementClusterClassifier) *libsveltosv1beta1.Classifier {
	c := &libsveltosv1beta1.Classifier{}
	c.Name = "mgmt:" + mcc.Name
	c.Spec.ClassifierLabels = mcc.Spec.ClassifierLabels
	return c
}

// clusterTypeFromKind converts the kind string from classificationLua to a ClusterType.
func clusterTypeFromKind(kind string) (libsveltosv1beta1.ClusterType, error) {
	switch {
	case strings.EqualFold(kind, "Cluster"):
		return libsveltosv1beta1.ClusterTypeCapi, nil
	case strings.EqualFold(kind, libsveltosv1beta1.SveltosClusterKind):
		return libsveltosv1beta1.ClusterTypeSveltos, nil
	default:
		return "", fmt.Errorf("unknown kind %q: must be Cluster or SveltosCluster", kind)
	}
}

// setMgmtClassifierFailureMessage patches ManagementClusterClassifier.Status.FailureMessage.
func setMgmtClassifierFailureMessage(ctx context.Context, c client.Client,
	mcc *libsveltosv1beta1.ManagementClusterClassifier, msg string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &libsveltosv1beta1.ManagementClusterClassifier{}
		if err := c.Get(ctx, types.NamespacedName{Name: mcc.Name}, current); err != nil {
			return err
		}
		patch := client.MergeFrom(current.DeepCopy())
		if msg == "" {
			current.Status.FailureMessage = nil
		} else {
			current.Status.FailureMessage = stringPtr(msg)
		}
		return c.Status().Patch(ctx, current, patch)
	})
}

// classifierLabelKeys returns just the key strings from a ClassifierLabel slice.
func classifierLabelKeys(cl []libsveltosv1beta1.ClassifierLabel) []string {
	keys := make([]string, len(cl))
	for i := range cl {
		keys[i] = cl[i].Key
	}
	return keys
}
