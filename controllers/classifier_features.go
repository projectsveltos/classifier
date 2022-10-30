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
	"fmt"
	"os"

	"github.com/go-logr/logr"

	classifyv1alpha1 "github.com/projectsveltos/classifier/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/deployer"
)

var (
	featuresHandlers map[string]feature
)

func RegisterFeatures(d deployer.DeployerInterface, setupLog logr.Logger) {
	err := d.RegisterFeatureID(classifyv1alpha1.FeatureClassifier)
	if err != nil {
		setupLog.Error(err, "failed to register feature FeatureClassifier")
		os.Exit(1)
	}

	creatFeatureHandlerMaps()
}

func creatFeatureHandlerMaps() {
	featuresHandlers = make(map[string]feature)

	featuresHandlers[classifyv1alpha1.FeatureClassifier] = feature{id: classifyv1alpha1.FeatureClassifier,
		currentHash: classifierHash, deploy: deployClassifierInCluster, undeploy: undeployClassifierFromCluster}
}

func getHandlersForFeature(featureID string) feature {
	v, ok := featuresHandlers[featureID]
	if !ok {
		panic(fmt.Errorf("feature %s has no feature handler registered", featureID))
	}

	return v
}
