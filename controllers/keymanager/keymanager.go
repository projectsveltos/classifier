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

package keymanager

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

// Multiple Classifier instances can be deployed in a CAPI Cluster and a cluster can then match multiple
// Classifier instances.
// If:
// - a cluster matches multiple Classifier instances
// - two (or more) of such Classifier instances want to set the same label (key) on cluster
// that is a misconfiguration/conflict that needs to be detected.
// Only one Classifier can manage a label (key) on a given Cluster at any point of time.
// Following client is used to solve such scenarios. One Classifier will get the manager role for a given label (key)
// in a given CAPI Cluster.
// All other Classifiers will report a conflict that requires admin intervention to be resolved.

type instance struct {
	chartMux sync.Mutex // use a Mutex to update chart maps as Classifier MaxConcurrentReconciles is higher than one

	// A cluster might match multiple Classifiers, and each of those Classifier instances might try to set same label (key).
	// That needs to be flagged as a misconfiguration.
	// In order to achieve that following map is used. It contains:
	// - per CAPI Cluster (key: clusterNamespace/clusterName)
	//     - per Label (key: <label ke>))
	//         - list of Classifiers that want to set such label in CAPI Cluster.
	// First Classifier able to add entry for a given CAPI Cluster/Label is allowed to manage that key.
	// canManageKey answers whether a Classifier can manage a label (key) for a given CAPI cluster.
	// Any other Classifier will report the misconfiguration.
	//
	// When Classifier managing a label in a CAPI Cluster is deleted or stops managing that key or cluster is not a match anymore,
	// the next Classifier in line will become the new manager.
	// That is achieved as part of ClassifierReconciler. Such reconciler watches for Classifier changes, when
	// a Classifier.Spec.ClassifierLabels changes, requeues all Classifiers.
	//
	// - per Cluster
	//   - per Label (key)
	//     - list of Classifier Names
	perClusterLabelMap map[string]map[string][]string
}

var (
	managerInstance *instance
	lock            = &sync.Mutex{}
)

const (
	keySeparator = "/"
)

// GetKeyManagerInstance return keyManager instance
func GetKeyManagerInstance(ctx context.Context, c client.Client) (*instance, error) {
	if managerInstance == nil {
		lock.Lock()
		defer lock.Unlock()
		if managerInstance == nil {
			managerInstance = &instance{
				perClusterLabelMap: make(map[string]map[string][]string),
				chartMux:           sync.Mutex{},
			}

			if err := managerInstance.rebuildRegistrations(ctx, c); err != nil {
				managerInstance = nil
				return nil, err
			}
		}
	}

	return managerInstance, nil
}

// RegisterClassifierForLabels registers Classifier as one requestor to manage all Spec.ClassifierLabels in
// all CAPI clusters currently matching this Classifier.
// Only first Classifier registering for a given label in a given CAPI Cluster is given the manager role.
func (m *instance) RegisterClassifierForLabels(classifier *libsveltosv1alpha1.Classifier,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) {

	clusterKey := m.getClusterKey(clusterNamespace, clusterName, clusterType)
	classifierKey := m.getClassifierKey(classifier.Name)

	m.chartMux.Lock()
	defer m.chartMux.Unlock()

	for i := range classifier.Spec.ClassifierLabels {
		m.addClusterEntry(clusterKey)
		m.addLabelKeyEntry(clusterKey, classifier.Spec.ClassifierLabels[i].Key)
		m.addClassifierEntry(clusterKey, classifier.Spec.ClassifierLabels[i].Key, classifierKey)
	}
}

// RemoveStaleRegistrations removes stale registrations.
// It considers all the labels (keys) the provided classifier is currently registered.
// Any label (key), not referenced anymore by classifier, for which classifier is currently
// registered, is considered stale and removed.
func (m *instance) RemoveStaleRegistrations(classifier *libsveltosv1alpha1.Classifier,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) {

	m.cleanRegistrations(classifier, clusterNamespace, clusterName, clusterType, false)
}

// RemoveAllRegistrations removes all registrations for a classifier.
func (m *instance) RemoveAllRegistrations(classifier *libsveltosv1alpha1.Classifier,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) {

	m.cleanRegistrations(classifier, clusterNamespace, clusterName, clusterType, true)
}

// cleanRegistrations removes Classifier's registrations.
// If removeAll is set to true, all registrations are removed. Otherwise only registration for
// labels (keys) not referenced anymore are.
func (m *instance) cleanRegistrations(classifier *libsveltosv1alpha1.Classifier,
	clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType, removeAll bool) {

	clusterKey := m.getClusterKey(clusterNamespace, clusterName, clusterType)
	classifierKey := m.getClassifierKey(classifier.Name)

	currentReferencedLabels := make(map[string]bool)
	if !removeAll {
		for i := range classifier.Spec.ClassifierLabels {
			key := classifier.Spec.ClassifierLabels[i].Key
			currentReferencedLabels[key] = true
		}
	}

	m.chartMux.Lock()
	defer m.chartMux.Unlock()

	for labelKey := range m.perClusterLabelMap[clusterKey] {
		if _, ok := currentReferencedLabels[labelKey]; ok {
			// Classifier is still referencing this label key.
			// Nothing to do.
			continue
		}
		// If Classifier was previously registered to manage this label key,
		// consider this entry stale and remove it.
		for i := range m.perClusterLabelMap[clusterKey][labelKey] {
			if m.perClusterLabelMap[clusterKey][labelKey][i] == classifierKey {
				// Order is not important. So move the element at index i with last one in order to avoid moving all elements.
				length := len(m.perClusterLabelMap[clusterKey][labelKey])
				m.perClusterLabelMap[clusterKey][labelKey][i] =
					m.perClusterLabelMap[clusterKey][labelKey][length-1]
				m.perClusterLabelMap[clusterKey][labelKey] = m.perClusterLabelMap[clusterKey][labelKey][:length-1]
				break
			}
		}
	}
}

// CanManageLabel returns true if a Classifier can manage a given label key.
// Only the first Classifier registered for a given label key in a given cluster can manage it.
func (m *instance) CanManageLabel(classifier *libsveltosv1alpha1.Classifier,
	clusterNamespace, clusterName, labelKey string, clusterType libsveltosv1alpha1.ClusterType) bool {

	clusterKey := m.getClusterKey(clusterNamespace, clusterName, clusterType)
	classifierKey := m.getClassifierKey(classifier.Name)

	m.chartMux.Lock()
	defer m.chartMux.Unlock()

	return m.isCurrentlyManager(clusterKey, labelKey, classifierKey)
}

// GetManagerForKey returns the name of the Classifier currently in charge of managing
// label key
// Returns an error if no Classifier is currently managing the label key
func (m *instance) GetManagerForKey(clusterNamespace, clusterName, labelKey string,
	clusterType libsveltosv1alpha1.ClusterType) (string, error) {

	clusterKey := m.getClusterKey(clusterNamespace, clusterName, clusterType)

	if _, ok := m.perClusterLabelMap[clusterKey]; !ok {
		return "", fmt.Errorf("no Classifier manging label key %s", labelKey)
	}

	if _, ok := m.perClusterLabelMap[clusterKey][labelKey]; !ok {
		return "", fmt.Errorf("no Classifier manging label key %s", labelKey)
	}

	if len(m.perClusterLabelMap[clusterKey][labelKey]) == 0 {
		return "", fmt.Errorf("no Classifier manging label key %s", labelKey)
	}

	return m.perClusterLabelMap[clusterKey][labelKey][0], nil
}

// GetRegisteredClassifiers returns all Classifiers currently registered for at
// at least one label key in the provided CAPI cluster
func (m *instance) GetRegisteredClassifiers(clusterNamespace, clusterName string,
	clusterType libsveltosv1alpha1.ClusterType) []string {

	clusterKey := m.getClusterKey(clusterNamespace, clusterName, clusterType)

	classifiers := make(map[string]bool)

	m.chartMux.Lock()
	defer m.chartMux.Unlock()

	if _, ok := m.perClusterLabelMap[clusterKey]; !ok {
		return nil
	}

	for key := range m.perClusterLabelMap[clusterKey] {
		for i := range m.perClusterLabelMap[clusterKey][key] {
			classifiers[m.perClusterLabelMap[clusterKey][key][i]] = true
		}
	}

	result := make([]string, len(classifiers))
	i := 0
	for cl := range classifiers {
		result[i] = cl
		i++
	}

	return result
}

// isCurrentlyManager returns true if classifierKey is currently the designed manager
// for label (key) in the CAPI cluster clusterKey
func (m *instance) isCurrentlyManager(clusterKey, labelKey, classifierKey string) bool {
	if _, ok := m.perClusterLabelMap[clusterKey]; !ok {
		return false
	}

	if _, ok := m.perClusterLabelMap[clusterKey][labelKey]; !ok {
		return false
	}

	if len(m.perClusterLabelMap[clusterKey][labelKey]) > 0 &&
		m.perClusterLabelMap[clusterKey][labelKey][0] == classifierKey {

		return true
	}

	return false
}

// getClusterKey returns the Key representing a CAPI Cluster
func (m *instance) getClusterKey(clusterNamespace, clusterName string, clusterType libsveltosv1alpha1.ClusterType) string {
	return fmt.Sprintf("%s%s%s%s%s", clusterType, keySeparator, clusterNamespace, keySeparator, clusterName)
}

// getClassifierKey returns the key for a Classifier instance
func (m *instance) getClassifierKey(classifierName string) string {
	return classifierName
}

// addClusterEntry adds an entry for clusterKey
func (m *instance) addClusterEntry(clusterKey string) {
	if _, ok := m.perClusterLabelMap[clusterKey]; !ok {
		m.perClusterLabelMap[clusterKey] = make(map[string][]string)
	}
}

// addLabelKeyEntry adds an entry for label key
func (m *instance) addLabelKeyEntry(clusterKey, labelKey string) {
	if _, ok := m.perClusterLabelMap[clusterKey]; !ok {
		m.perClusterLabelMap[clusterKey] = make(map[string][]string)
	}

	if _, ok := m.perClusterLabelMap[clusterKey][labelKey]; !ok {
		m.perClusterLabelMap[clusterKey][labelKey] = make([]string, 0)
	}
}

// addClassifierEntry adds an entry for classifier for a given label (key)
// Method is idempotent. If Classifier is already registered for a given label (key), it won't be added
// again
func (m *instance) addClassifierEntry(clusterKey, labelKey, classifierKey string) {
	if _, ok := m.perClusterLabelMap[clusterKey]; !ok {
		m.perClusterLabelMap[clusterKey] = make(map[string][]string)
	}

	if _, ok := m.perClusterLabelMap[clusterKey][labelKey]; !ok {
		m.perClusterLabelMap[clusterKey][labelKey] = make([]string, 0)
	}

	if isClassifierAlreadyRegistered(m.perClusterLabelMap[clusterKey][labelKey], classifierKey) {
		return
	}

	m.perClusterLabelMap[clusterKey][labelKey] = append(m.perClusterLabelMap[clusterKey][labelKey], classifierKey)
}

// isClassifierAlreadyRegistered returns true if a given Classifier is already present in the slice
func isClassifierAlreadyRegistered(classifiers []string, classifierKey string) bool {
	for i := range classifiers {
		if classifiers[i] == classifierKey {
			return true
		}
	}

	return false
}

// rebuildRegistrations rebuilds internal structures to identify Classifiers managing
// labels and Classifiers currently just registered but not managing.
// Relies completely on Classifier.Status
func (m *instance) rebuildRegistrations(ctx context.Context, c client.Client) error {
	// Lock here
	m.chartMux.Lock()
	defer m.chartMux.Unlock()

	classifierList := &libsveltosv1alpha1.ClassifierList{}
	err := c.List(ctx, classifierList)
	if err != nil {
		return err
	}

	for i := range classifierList.Items {
		cs := &classifierList.Items[i]
		m.addManagers(cs)
	}

	for i := range classifierList.Items {
		cs := &classifierList.Items[i]
		m.addNonManagers(cs)
	}

	return nil
}

// addManagers walks Classifier's status and registers it for each label currently managed
func (m *instance) addManagers(classifier *libsveltosv1alpha1.Classifier) {
	classifierKey := m.getClassifierKey(classifier.Name)

	for i := range classifier.Status.MachingClusterStatuses {
		clusterStatus := &classifier.Status.MachingClusterStatuses[i]
		clusterType := libsveltosv1alpha1.ClusterTypeCapi
		if clusterStatus.ClusterRef.APIVersion == libsveltosv1alpha1.GroupVersion.String() {
			clusterType = libsveltosv1alpha1.ClusterTypeSveltos
		}
		clusterKey := m.getClusterKey(clusterStatus.ClusterRef.Namespace, clusterStatus.ClusterRef.Name, clusterType)

		m.addManagedLabelsInCluster(classifierKey, clusterKey, clusterStatus.ManagedLabels)
	}
}

func (m *instance) addManagedLabelsInCluster(classifierKey, clusterKey string, managedLabels []string) {
	for i := range managedLabels {
		labelKey := managedLabels[i]
		m.addClusterEntry(clusterKey)
		m.addLabelKeyEntry(clusterKey, labelKey)
		m.addClassifierEntry(clusterKey, labelKey, classifierKey)
	}
}

// addNonManagers walks Classifier's status and registers it for each labels currently not managed
// (not managed because other Classifier is)
func (m *instance) addNonManagers(classifier *libsveltosv1alpha1.Classifier) {
	classifierKey := m.getClassifierKey(classifier.Name)

	for i := range classifier.Status.MachingClusterStatuses {
		clusterStatus := &classifier.Status.MachingClusterStatuses[i]
		clusterType := libsveltosv1alpha1.ClusterTypeCapi
		if clusterStatus.ClusterRef.APIVersion == libsveltosv1alpha1.GroupVersion.String() {
			clusterType = libsveltosv1alpha1.ClusterTypeSveltos
		}
		clusterKey := m.getClusterKey(clusterStatus.ClusterRef.Namespace, clusterStatus.ClusterRef.Name, clusterType)

		unManagedLabels := m.buildSliceOfUnManagedLabels(clusterStatus.UnManagedLabels)
		m.addManagedLabelsInCluster(classifierKey, clusterKey, unManagedLabels)
	}
}

func (m *instance) buildSliceOfUnManagedLabels(unManaged []libsveltosv1alpha1.UnManagedLabel) []string {
	result := make([]string, len(unManaged))
	for i := range unManaged {
		result[i] = unManaged[i].Key
	}

	return result
}
