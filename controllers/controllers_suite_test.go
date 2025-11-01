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

package controllers_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // SA1019: We are unable to update the dependency at this time.
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/classifier/controllers"
	"github.com/projectsveltos/classifier/internal/test/helpers"
	"github.com/projectsveltos/classifier/pkg/scope"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/crd"
	"github.com/projectsveltos/libsveltos/lib/deployer"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var (
	testEnv *helpers.TestEnvironment
	cancel  context.CancelFunc
	ctx     context.Context
	scheme  *runtime.Scheme
)

var (
	cacheSyncBackoff = wait.Backoff{
		Duration: 100 * time.Millisecond,
		Factor:   1.5,
		Steps:    8,
		Jitter:   0.4,
	}
)

const (
	upstreamClusterNamePrefix = "upstream-cluster"
	upstreamMachineNamePrefix = "upstream-machine"
	clusterKind               = "Cluster"
	projectsveltosNamespace   = "projectsveltos"
)

const (
	timeout         = 40 * time.Second
	pollingInterval = 2 * time.Second
	version         = "v0.31.0"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

var _ = BeforeSuite(func() {
	By("bootstrapping test environment")

	ctrl.SetLogger(klog.Background())

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	scheme, err = setupScheme()
	Expect(err).To(BeNil())

	testEnvConfig := helpers.NewTestEnvironmentConfiguration([]string{}, scheme)
	testEnv, err = testEnvConfig.Build(scheme)
	if err != nil {
		panic(err)
	}

	controllers.SetManagementClusterAccess(testEnv.Config, testEnv.Client)
	controllers.CreatFeatureHandlerMaps()

	go func() {
		By("Starting the manager")
		err = testEnv.StartManager(ctx)
		if err != nil {
			panic(fmt.Sprintf("Failed to start the envtest manager: %v", err))
		}
	}()

	sveltosClusterCRD, err := k8s_utils.GetUnstructured(crd.GetSveltosClusterCRDYAML())
	Expect(err).To(BeNil())
	Expect(testEnv.Create(ctx, sveltosClusterCRD)).To(Succeed())

	classifierCRD, err := k8s_utils.GetUnstructured(crd.GetClassifierCRDYAML())
	Expect(err).To(BeNil())
	Expect(testEnv.Create(ctx, classifierCRD)).To(Succeed())

	classifierReportCRD, err := k8s_utils.GetUnstructured(crd.GetClassifierReportCRDYAML())
	Expect(err).To(BeNil())
	Expect(testEnv.Create(ctx, classifierReportCRD)).To(Succeed())

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: projectsveltosNamespace,
		},
	}
	Expect(testEnv.Create(ctx, ns)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

	if synced := testEnv.GetCache().WaitForCacheSync(ctx); !synced {
		time.Sleep(time.Second)
	}
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

func setupScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := libsveltosv1beta1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

func randomString() string {
	const length = 10
	return util.RandomString(length)
}

func getClassifierReport(classifierName, clusterNamespace, clusterName string) *libsveltosv1beta1.ClassifierReport {
	return &libsveltosv1beta1.ClassifierReport{
		ObjectMeta: metav1.ObjectMeta{
			Name: randomString(),
			Labels: map[string]string{
				libsveltosv1beta1.ClassifierlNameLabel: classifierName,
			},
		},
		Spec: libsveltosv1beta1.ClassifierReportSpec{
			ClusterNamespace: clusterNamespace,
			ClusterName:      clusterName,
			ClassifierName:   classifierName,
			ClusterType:      libsveltosv1beta1.ClusterTypeCapi,
		},
	}
}

func getClassifierInstance(name string) *libsveltosv1beta1.Classifier {
	classifierLabels := []libsveltosv1beta1.ClassifierLabel{{Key: "version", Value: "v1.25.3"}}
	return &libsveltosv1beta1.Classifier{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: libsveltosv1beta1.ClassifierSpec{
			KubernetesVersionConstraints: []libsveltosv1beta1.KubernetesVersionConstraint{
				{
					Version:    "1.25.3",
					Comparison: string(libsveltosv1beta1.ComparisonEqual),
				},
			},
			ClassifierLabels: classifierLabels,
		},
	}
}

func getClassifierReconciler(c client.Client, dep deployer.DeployerInterface) *controllers.ClassifierReconciler {
	return &controllers.ClassifierReconciler{
		Client:        c,
		Scheme:        scheme,
		Deployer:      dep,
		ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
		ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
		Mux:           sync.Mutex{},
	}
}

func getClassifierScope(c client.Client, logger logr.Logger,
	classifier *libsveltosv1beta1.Classifier) *scope.ClassifierScope {

	classifierScope, err := scope.NewClassifierScope(scope.ClassifierScopeParams{
		Client:         c,
		Logger:         logger,
		Classifier:     classifier,
		ControllerName: "classifier",
	})
	Expect(err).To(BeNil())
	return classifierScope
}

// waitForObject waits for the cache to be updated helps in preventing test flakes due to the cache sync delays.
func waitForObject(ctx context.Context, c client.Client, obj client.Object) error {
	// Makes sure the cache is updated with the new object
	objCopy := obj.DeepCopyObject().(client.Object)
	key := client.ObjectKeyFromObject(obj)
	if err := wait.ExponentialBackoff(
		cacheSyncBackoff,
		func() (done bool, err error) {
			if err := c.Get(ctx, key, objCopy); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}); err != nil {
		return errors.Wrapf(err, "object %s, %s is not being added to the testenv client cache", obj.GetObjectKind().GroupVersionKind().String(), key)
	}
	return nil
}

func addTypeInformationToObject(scheme *runtime.Scheme, obj client.Object) error {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("missing apiVersion or kind and cannot assign it; %w", err)
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

	return nil
}
