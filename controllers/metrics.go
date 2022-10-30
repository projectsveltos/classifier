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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	classifyv1alpha1 "github.com/projectsveltos/classifier/api/v1alpha1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

var (
	programClassifierDurationHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "projectsveltos",
			Name:      "program_classifier_time_seconds",
			Help:      "Program Classifier on a workload cluster duration distribution",
			Buckets:   []float64{0.1, 0.5, 1, 5, 10, 20, 30},
		},
	)
)

//nolint:gochecknoinits // forced pattern, can't workaround
func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(programClassifierDurationHistogram)
}

func newClassifierHistogram(clusterNamespace, clusterName string,
	logger logr.Logger) prometheus.Histogram {

	clusterInfo := strings.ReplaceAll(fmt.Sprintf("%s_%s", clusterNamespace, clusterName), "-", "_")
	histogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: clusterInfo,
			Name:      "program_classifier_time_seconds",
			Help:      "Program Classifier on a workload cluster duration distribution",
			Buckets:   []float64{0.1, 0.5, 1, 5, 10, 20, 30},
		},
	)

	err := metrics.Registry.Register(histogram)
	if err != nil {
		var registrationError *prometheus.AlreadyRegisteredError
		ok := errors.As(err, &registrationError)
		if ok {
			_, ok = registrationError.ExistingCollector.(prometheus.Histogram)
			if ok {
				return registrationError.ExistingCollector.(prometheus.Histogram)
			}
			logCollectorError(err, logger)
			return nil
		}
		logCollectorError(err, logger)
		return nil
	}

	return histogram
}

func logCollectorError(err error, logger logr.Logger) {
	logger.V(logs.LogInfo).Info(fmt.Sprint("failed to register collector: %w", err))
}

func programDuration(elapsed time.Duration, clusterNamespace, clusterName, featureID string,
	logger logr.Logger) {

	if featureID == string(classifyv1alpha1.FeatureClassifier) {
		programClassifierDurationHistogram.Observe(elapsed.Seconds())
		clusterHistogram := newClassifierHistogram(clusterNamespace, clusterName, logger)
		if clusterHistogram != nil {
			logger.V(logs.LogVerbose).Info(fmt.Sprintf("register data for %s/%s %s",
				clusterNamespace, clusterName, featureID))
			clusterHistogram.Observe(elapsed.Seconds())
		}
	}
}
