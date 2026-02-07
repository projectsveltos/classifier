/*
Copyright 2026. projectsveltos.io. All rights reserved.

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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

type PatchTracker struct {
	// Maps ConfigMap NamespacedName to a set of Clusters using it
	trackedConfigMaps map[types.NamespacedName]*libsveltosset.Set
	mux               sync.RWMutex
}

var (
	instance *PatchTracker
	once     sync.Once
)

// GetPatchTracker returns the singleton instance
func getPatchTracker() *PatchTracker {
	once.Do(func() {
		instance = &PatchTracker{
			trackedConfigMaps: make(map[types.NamespacedName]*libsveltosset.Set),
		}
	})
	return instance
}

// TrackConfigMap registers a ConfigMap and the cluster that requested it
func (p *PatchTracker) TrackConfigMap(configMap types.NamespacedName, clusterRef *corev1.ObjectReference) {
	p.mux.Lock()
	defer p.mux.Unlock()

	if _, exists := p.trackedConfigMaps[configMap]; !exists {
		p.trackedConfigMaps[configMap] = &libsveltosset.Set{}
	}
	p.trackedConfigMaps[configMap].Insert(clusterRef)
}

// IsPatchConfigMap checks if a specific NamespacedName is known to contain patches
func (p *PatchTracker) IsPatchConfigMap(configMap types.NamespacedName) bool {
	p.mux.RLock()
	defer p.mux.RUnlock()

	_, exists := p.trackedConfigMaps[configMap]
	return exists
}
