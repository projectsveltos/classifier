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

package scope

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	classifyv1alpha1 "github.com/projectsveltos/classifier/api/v1alpha1"
)

// ClassifierScopeParams defines the input parameters used to create a new Classifier Scope.
type ClassifierScopeParams struct {
	Client         client.Client
	Logger         logr.Logger
	Classifier     *classifyv1alpha1.Classifier
	ControllerName string
}

// NewClassifierScope creates a new Classifier Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewClassifierScope(params ClassifierScopeParams) (*ClassifierScope, error) {
	if params.Client == nil {
		return nil, errors.New("client is required when creating a ClassifierScope")
	}
	if params.Classifier == nil {
		return nil, errors.New("failed to generate new scope from nil Classifier")
	}

	helper, err := patch.NewHelper(params.Classifier, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}
	return &ClassifierScope{
		Logger:         params.Logger,
		client:         params.Client,
		Classifier:     params.Classifier,
		patchHelper:    helper,
		controllerName: params.ControllerName,
	}, nil
}

// ClassifierScope defines the basic context for an actuator to operate upon.
type ClassifierScope struct {
	logr.Logger
	client         client.Client
	patchHelper    *patch.Helper
	Classifier     *classifyv1alpha1.Classifier
	controllerName string
}

// PatchObject persists the feature configuration and status.
func (s *ClassifierScope) PatchObject(ctx context.Context) error {
	return s.patchHelper.Patch(
		ctx,
		s.Classifier,
	)
}

// Close closes the current scope persisting the Classifier configuration and status.
func (s *ClassifierScope) Close(ctx context.Context) error {
	return s.PatchObject(ctx)
}

// Name returns the Classifier name.
func (s *ClassifierScope) Name() string {
	return s.Classifier.Name
}

// ControllerName returns the name of the controller that
// created the ClassifierScope.
func (s *ClassifierScope) ControllerName() string {
	return s.controllerName
}

// SetMatchingClusterRefs sets the feature status.
func (s *ClassifierScope) SetMachingClusterStatuses(matchingClusters []classifyv1alpha1.MachingClusterStatus) {
	s.Classifier.Status.MachingClusterStatuses = matchingClusters
}

// SetMatchingClusterRefs sets the feature status.
func (s *ClassifierScope) SetClusterInfo(clusterInfo []classifyv1alpha1.ClusterInfo) {
	s.Classifier.Status.ClusterInfo = clusterInfo
}
