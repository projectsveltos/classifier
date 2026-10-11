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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/eventreasons"
)

// recordWarningf records a Warning event on regarding. It does nothing when no recorder is set.
// The note is capped, so an oversized message is not rejected by the API server. Identical events
// are aggregated by the recorder, so notes must not contain anything that changes between
// reconciliations (timestamps, counters).
func recordWarningf(recorder events.EventRecorder, regarding runtime.Object, reason, action, format string, args ...any) {
	if recorder == nil {
		return
	}

	note := eventreasons.TruncateNote(fmt.Sprintf(format, args...))
	recorder.Eventf(regarding, nil, corev1.EventTypeWarning, reason, action, "%s", note)
}

// unmanagedLabelKeys returns the keys of the labels the Classifier wants to manage but cannot,
// because they are managed by another instance, as a single comma separated string.
func unmanagedLabelKeys(unmanaged []libsveltosv1beta1.UnManagedLabel) string {
	keys := make([]string, len(unmanaged))
	for i := range unmanaged {
		keys[i] = unmanaged[i].Key
	}

	return strings.Join(keys, ", ")
}
