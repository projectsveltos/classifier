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
	"crypto/sha256"
	"reflect"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/gdexlab/go-render/render"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/projectsveltos/classifier/controllers"
	"github.com/projectsveltos/classifier/controllers/keymanager"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/deployer"
	fakedeployer "github.com/projectsveltos/libsveltos/lib/deployer/fake"
)

var _ = Describe("Classifier Deployer", func() {
	var logger logr.Logger

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
		controllers.SetVersion(version)
	})

	It("classifierHash returns Classifier hash", func() {
		classifier := getClassifierInstance(randomString())
		classifier.Spec.DeployedResourceConstraint = &libsveltosv1beta1.DeployedResourceConstraint{
			ResourceSelectors: []libsveltosv1beta1.ResourceSelector{
				{
					Namespace: randomString(),
					Group:     randomString(),
					Kind:      randomString(),
					Version:   randomString(),
					LabelFilters: []libsveltosv1beta1.LabelFilter{
						{
							Key:   randomString(),
							Value: randomString(),
						},
					},
				},
				{
					Namespace: randomString(),
					Group:     randomString(),
					Kind:      randomString(),
					Version:   randomString(),
					LabelFilters: []libsveltosv1beta1.LabelFilter{
						{
							Key:   randomString(),
							Value: randomString(),
						},
					},
				},
			},
		}

		h := sha256.New()
		var config string

		config += version
		config += render.AsCode(classifier.Spec)
		h.Write([]byte(config))
		hash := h.Sum(nil)

		currentHash := controllers.ClassifierHash(classifier)
		Expect(reflect.DeepEqual(currentHash, hash)).To(BeTrue())
	})

	It("deployClassifierCRD deploys Classifier CRD", func() {
		Expect(controllers.DeployClassifierCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			classifierCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "classifiers.lib.projectsveltos.io"}, classifierCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployClassifierReportCRD deploys ClassifierReport CRD", func() {
		Expect(controllers.DeployClassifierReportCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			classifierReportCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "classifierreports.lib.projectsveltos.io"}, classifierReportCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployHealthCheckCRD deploys HealthCheck CRD", func() {
		Expect(controllers.DeployHealthCheckCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			healthCheckCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "healthchecks.lib.projectsveltos.io"}, healthCheckCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployHealthCheckReportCRD deploys HealthCheckReport CRD", func() {
		Expect(controllers.DeployHealthCheckReportCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			healthCheckReportCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "healthcheckreports.lib.projectsveltos.io"}, healthCheckReportCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployEventSourceCRD deploys EventSource CRD", func() {
		Expect(controllers.DeployEventSourceCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			eventSourceCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "eventsources.lib.projectsveltos.io"}, eventSourceCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployEventReportCRD deploys EventReport CRD", func() {
		Expect(controllers.DeployEventReportCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			eventReportCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "eventreports.lib.projectsveltos.io"}, eventReportCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployClassifierInstance creates Classifier instance", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		classifier := getClassifierInstance(randomString())
		Expect(controllers.DeployClassifierInstance(ctx, c, classifier, logger)).To(Succeed())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
	})

	It("deployDebuggingConfigurationCRD deploys DebuggingConfiguration CRD", func() {
		Expect(controllers.DeployDebuggingConfigurationCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			dcCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "debuggingconfigurations.lib.projectsveltos.io"}, dcCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployReloaderCRD deploys Reloader CRD", func() {
		Expect(controllers.DeployReloaderCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			reloaderCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "reloaders.lib.projectsveltos.io"}, reloaderCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployReloaderReportCRD deploys ReloaderReport CRD", func() {
		Expect(controllers.DeployReloaderReportCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			reloaderReportCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "reloaderreports.lib.projectsveltos.io"}, reloaderReportCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("deployClassifierInstance creates Classifier instance", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		classifier := getClassifierInstance(randomString())
		Expect(controllers.DeployClassifierInstance(ctx, c, classifier, logger)).To(Succeed())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
	})

	It("deployClassifierInstance updates Classifier instance", func() {
		classifier := getClassifierInstance(randomString())

		initObjects := []client.Object{
			classifier,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		classifier.Spec.ClassifierLabels = []libsveltosv1beta1.ClassifierLabel{{Key: randomString(), Value: randomString()}}
		Expect(controllers.DeployClassifierInstance(ctx, c, classifier, logger)).To(Succeed())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, currentClassifier)).To(Succeed())
		Expect(reflect.DeepEqual(currentClassifier.Spec, classifier.Spec)).To(BeTrue())
	})

	It("deployClassifierInCluster deploys Classifier CRD and instance in remote cluster", func() {
		cluster := prepareCluster()
		classifier := getClassifierInstance(randomString())

		Expect(testEnv.Create(context.TODO(), classifier))

		// Just verify result is success (testEnv is used to simulate both management and workload cluster and because
		// classifier is expected in the management cluster, above line is required
		Expect(controllers.DeployClassifierInCluster(context.TODO(), testEnv.Client, cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1beta1.FeatureClassifier, libsveltosv1beta1.ClusterTypeCapi, deployer.Options{},
			logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			classifierCRD := &apiextensionsv1.CustomResourceDefinition{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Name: "classifiers.lib.projectsveltos.io"}, classifierCRD)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("processClassifier detects classifier needs to be deployed in cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), logger, testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)
		classifier := getClassifierInstance(randomString())
		classifierScope := getClassifierScope(testEnv.Client, logger, classifier)

		cluster := prepareCluster()

		f := controllers.GetHandlersForFeature(libsveltosv1beta1.FeatureClassifier)
		clusterInfo, err := controllers.ProcessClassifier(classifierReconciler, context.TODO(), classifierScope, "",
			getClusterRef(cluster), f, logger)
		Expect(err).To(BeNil())
		Expect(clusterInfo).ToNot(BeNil())
		Expect(clusterInfo.Status).To(Equal(libsveltosv1beta1.SveltosStatusProvisioning))
	})

	It("processClassifier detects classifier does not need to be deployed in cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(),
			logger, testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		cluster := prepareCluster()

		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)
		classifier := getClassifierInstance(randomString())

		// Following are needed to make ProcessClassifier think all that is needed is already deployed
		// Deploy Classifier CRD
		Expect(controllers.DeployClassifierCRD(context.TODO(), testEnv.Config, logger)).To(Succeed())
		// Deploy Classifier instance
		Expect(controllers.DeployClassifierInstance(ctx, testEnv, classifier, logger)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		hash := controllers.ClassifierHash(classifier)
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, classifier)).To(Succeed())
		classifier.Status = libsveltosv1beta1.ClassifierStatus{
			ClusterInfo: []libsveltosv1beta1.ClusterInfo{
				{
					Cluster: corev1.ObjectReference{
						Namespace: cluster.Namespace, Name: cluster.Name,
						APIVersion: clusterv1.GroupVersion.String(), Kind: clusterKind,
					},
					Status: libsveltosv1beta1.SveltosStatusProvisioned,
					Hash:   hash,
				},
			},
		}
		Expect(testEnv.Status().Update(context.TODO(), classifier)).To(Succeed())

		Eventually(func() bool {
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name}, classifier)
			if err != nil {
				return false
			}
			return len(classifier.Status.ClusterInfo) == 1 &&
				classifier.Status.ClusterInfo[0].Status == libsveltosv1beta1.SveltosStatusProvisioned
		}, timeout, pollingInterval).Should(BeTrue())

		classifierScope := getClassifierScope(testEnv.Client, logger, classifier)

		f := controllers.GetHandlersForFeature(libsveltosv1beta1.FeatureClassifier)
		clusterInfo, err := controllers.ProcessClassifier(classifierReconciler, context.TODO(), classifierScope, "",
			getClusterRef(cluster), f, logger)
		Expect(err).To(BeNil())
		Expect(clusterInfo).ToNot(BeNil())
		Expect(clusterInfo.Status).To(Equal(libsveltosv1beta1.SveltosStatusProvisioned))
	})

	It("removeClassifier queue job to remove Classifier from Cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), logger, testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)
		classifier := getClassifierInstance(randomString())
		classifierScope := getClassifierScope(testEnv.Client, logger, classifier)

		cluster := prepareCluster()
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1beta1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{
					Namespace: cluster.Namespace, Name: cluster.Name,
					APIVersion: clusterv1.GroupVersion.String(), Kind: clusterKind,
				},
				Status: libsveltosv1beta1.SveltosStatusProvisioned,
				Hash:   []byte(randomString()),
			},
		}

		Expect(testEnv.Status().Update(context.TODO(), currentClassifier)).To(Succeed())
		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err == nil && len(currentClassifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		f := controllers.GetHandlersForFeature(libsveltosv1beta1.FeatureClassifier)
		err := controllers.RemoveClassifier(classifierReconciler, context.TODO(), classifierScope,
			getClusterRef(cluster), f, logger)
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(Equal("cleanup request is queued"))

		key := deployer.GetKey(cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1beta1.FeatureClassifier, libsveltosv1beta1.ClusterTypeCapi, true)
		Expect(dep.IsKeyInProgress(key)).To(BeTrue())
	})

	It("undeployClassifierFromCluster removes Classifier from Cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), logger, testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		classifier := getClassifierInstance(randomString())

		cluster := prepareCluster()
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1beta1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{
					Namespace: cluster.Namespace, Name: cluster.Name,
					APIVersion: clusterv1.GroupVersion.String(), Kind: clusterKind,
				},
				Status: libsveltosv1beta1.SveltosStatusProvisioned,
				Hash:   []byte(randomString()),
			},
		}

		Expect(testEnv.Status().Update(context.TODO(), currentClassifier)).To(Succeed())
		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err == nil && len(currentClassifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		err := controllers.UndeployClassifierFromCluster(context.TODO(), testEnv, cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1beta1.FeatureClassifier, libsveltosv1beta1.ClusterTypeCapi,
			deployer.Options{}, logger)
		Expect(err).To(BeNil())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			currentClassifier := &libsveltosv1beta1.Classifier{}
			err = testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err != nil && apierrors.IsNotFound(err)
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("undeployClassifier queues a job to remove classifier from cluster", func() {
		dep := fakedeployer.GetClient(context.TODO(), logger, testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())
		classifierReconciler := getClassifierReconciler(testEnv.Client, dep)

		classifier := getClassifierInstance(randomString())

		cluster := prepareCluster()

		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
			currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1beta1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{
					Namespace: cluster.Namespace, Name: cluster.Name,
					APIVersion: clusterv1.GroupVersion.String(), Kind: clusterKind,
				},
				Status: libsveltosv1beta1.SveltosStatusProvisioned,
				Hash:   []byte(randomString()),
			},
		}

		Expect(testEnv.Status().Update(context.TODO(), currentClassifier)).To(Succeed())
		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			err := testEnv.Get(context.TODO(), types.NamespacedName{Name: classifier.Name},
				currentClassifier)
			return err == nil && len(currentClassifier.Status.ClusterInfo) == 1
		}, timeout, pollingInterval).Should(BeTrue())

		classifierScope := getClassifierScope(testEnv.Client, logger, currentClassifier)

		f := controllers.GetHandlersForFeature(libsveltosv1beta1.FeatureClassifier)
		err := controllers.UndeployClassifier(classifierReconciler, context.TODO(), classifierScope, f, logger)
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(Equal("still in the process of removing Classifier from 1 clusters"))

		key := deployer.GetKey(cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1beta1.FeatureClassifier, libsveltosv1beta1.ClusterTypeCapi, true)
		Expect(dep.IsKeyInProgress(key)).To(BeTrue())
	})

	It("deploySveltosAgent deploys sveltos agent", func() {
		Expect(controllers.DeploySveltosAgentInManagedCluster(ctx, testEnv.Config, randomString(), randomString(),
			"do-not-send-reports", libsveltosv1beta1.ClusterTypeCapi, nil, logger)).To(Succeed())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() error {
			currentSveltosAgent := &appsv1.Deployment{}
			return testEnv.Get(context.TODO(),
				types.NamespacedName{Namespace: "projectsveltos", Name: "sveltos-agent-manager"},
				currentSveltosAgent)
		}, timeout, pollingInterval).Should(BeNil())
	})

	It("createAccessRequest creates AccessRequest instance", func() {
		classifier := getClassifierInstance(randomString())

		clusterNamespace := randomString()
		clusterName := randomString()

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNamespace,
			},
		}

		initObjects := []client.Object{classifier, ns}

		options := deployer.Options{
			HandlerOptions: map[string]string{controllers.Controlplaneendpoint: "http://192.168.10.1:443"},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()
		Expect(controllers.CreateAccessRequest(ctx, c, clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeCapi,
			options)).To(Succeed())

		accessRequest := &libsveltosv1beta1.AccessRequest{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{
				Namespace: clusterNamespace,
				Name:      controllers.GetAccessRequestName(clusterName, libsveltosv1beta1.ClusterTypeCapi),
			},
			accessRequest)).To(Succeed())
	})

	It("getKubeconfigFromAccessRequest returns Kubeconfig contained in the AccessRequest Secret", func() {
		classifier := getClassifierInstance(randomString())

		clusterNamespace := randomString()
		clusterName := randomString()

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNamespace,
			},
		}

		kubeconfig := []byte(randomString())
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
			Data: map[string][]byte{
				"kubeconfig": kubeconfig,
			},
		}

		accessRequest := libsveltosv1beta1.AccessRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      controllers.GetAccessRequestName(clusterName, libsveltosv1beta1.ClusterTypeCapi),
				Namespace: clusterNamespace,
			},
			Status: libsveltosv1beta1.AccessRequestStatus{
				SecretRef: &corev1.ObjectReference{
					Namespace: secret.Namespace,
					Name:      secret.Name,
				},
			},
		}

		initObjects := []client.Object{classifier, secret, ns, &accessRequest}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()
		currentKubeconfig, err := controllers.GetKubeconfigFromAccessRequest(context.TODO(), c,
			clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeCapi, logger)
		Expect(err).To(BeNil())
		Expect(currentKubeconfig).ToNot(BeNil())
		Expect(reflect.DeepEqual(currentKubeconfig, kubeconfig)).To(BeTrue())
	})

	It("updateSecretWithAccessManagementKubeconfig creates a secret containing kubeconfig to access management cluster", func() {
		classifier := getClassifierInstance(randomString())
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		kubeconfig := []byte(randomString())

		cluster := prepareCluster()

		Expect(controllers.UpdateSecretWithAccessManagementKubeconfig(context.TODO(), testEnv.Client, cluster.Namespace, cluster.Name,
			classifier.Name, libsveltosv1beta1.ClusterTypeCapi, kubeconfig, logger)).To(BeNil())

		Eventually(func() bool {
			secret := &corev1.Secret{}
			err := testEnv.Get(context.TODO(),
				types.NamespacedName{Namespace: libsveltosv1beta1.ClassifierSecretNamespace, Name: libsveltosv1beta1.ClassifierSecretName},
				secret)
			return err == nil
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("deploy/remove SveltosAgent resources to/from management cluster", func() {
		clusterNamespace := randomString()
		clusterName := randomString()
		clusterType := libsveltosv1beta1.ClusterTypeSveltos

		_, err := keymanager.GetKeyManagerInstance(context.TODO(), testEnv.Client)
		Expect(err).To(BeNil())

		Expect(controllers.DeploySveltosAgentInManagementCluster(context.TODO(), testEnv.Config,
			testEnv.Client, clusterNamespace, clusterName, "", clusterType, nil, logger)).To(Succeed())

		expectedLabels := controllers.GetSveltosAgentLabels(clusterNamespace, clusterName, clusterType)

		listOptions := []client.ListOption{
			client.InNamespace(controllers.GetSveltosAgentNamespace()),
		}
		Eventually(func() bool {
			deployments := &appsv1.DeploymentList{}
			err := testEnv.List(context.TODO(), deployments, listOptions...)
			if err != nil {
				return false
			}

			if len(deployments.Items) == 0 {
				return false
			}

			for i := range deployments.Items {
				d := &deployments.Items[i]
				if verifyLabels(d.Labels, expectedLabels) {
					return true
				}
			}
			return false
		}, timeout, pollingInterval).Should(BeTrue())

		Expect(controllers.RemoveSveltosAgentFromManagementCluster(context.TODO(), clusterNamespace, clusterName,
			clusterType, logger)).To(Succeed())

		// Verify resources are gone
		Eventually(func() bool {
			deployments := &appsv1.DeploymentList{}
			err := testEnv.List(context.TODO(), deployments, listOptions...)
			if err != nil {
				return false
			}
			for i := range deployments.Items {
				d := &deployments.Items[i]
				if verifyLabels(d.Labels, expectedLabels) {
					return false
				}
			}
			return true
		}, timeout, pollingInterval).Should(BeTrue())
	})
})

func prepareCluster() *clusterv1.Cluster {
	namespace := randomString()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      randomString(),
		},
	}

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      randomString(),
			Labels: map[string]string{
				clusterv1.ClusterNameLabel:         cluster.Name,
				clusterv1.MachineControlPlaneLabel: "ok",
			},
		},
	}

	Expect(testEnv.Create(context.TODO(), cluster)).To(Succeed())
	Expect(testEnv.Create(context.TODO(), machine)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

	cluster.Status = clusterv1.ClusterStatus{
		InfrastructureReady: true,
		ControlPlaneReady:   true,
	}
	Expect(testEnv.Status().Update(context.TODO(), cluster)).To(Succeed())

	machine.Status = clusterv1.MachineStatus{
		Phase: string(clusterv1.MachinePhaseRunning),
	}
	Expect(testEnv.Status().Update(context.TODO(), machine)).To(Succeed())

	// Create a secret with cluster kubeconfig

	By("Create the secret with cluster kubeconfig")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      cluster.Name + "-kubeconfig",
		},
		Data: map[string][]byte{
			"value": testEnv.Kubeconfig,
		},
	}
	Expect(testEnv.Client.Create(context.TODO(), secret)).To(Succeed())
	Expect(waitForObject(context.TODO(), testEnv.Client, secret)).To(Succeed())

	By("Create the ConfigMap with sveltos-agent version")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: projectsveltosNamespace,
			Name:      "sveltos-agent-version",
		},
		Data: map[string]string{
			"sveltos-agent-version": version,
		},
	}
	err := testEnv.Client.Create(context.TODO(), cm)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}
	Expect(waitForObject(context.TODO(), testEnv.Client, cm)).To(Succeed())

	Expect(addTypeInformationToObject(scheme, cluster)).To(Succeed())

	return cluster
}

func getClusterRef(cluster client.Object) *corev1.ObjectReference {
	apiVersion, kind := cluster.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()
	return &corev1.ObjectReference{
		Namespace:  cluster.GetNamespace(),
		Name:       cluster.GetName(),
		APIVersion: apiVersion,
		Kind:       kind,
	}
}

// verifyLabels verifies that all labels in expectedLabels are also present
// in currentLabels with same value
func verifyLabels(currentLabels, expectedLabels map[string]string) bool {
	if currentLabels == nil {
		return false
	}

	for k := range expectedLabels {
		v, ok := currentLabels[k]
		if !ok {
			return false
		}
		if v != expectedLabels[k] {
			return false
		}
	}

	return true
}
