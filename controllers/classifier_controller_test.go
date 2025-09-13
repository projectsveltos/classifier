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
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // SA1019: We are unable to update the dependency at this time.
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/projectsveltos/classifier/controllers"
	"github.com/projectsveltos/classifier/controllers/keymanager"
	"github.com/projectsveltos/classifier/pkg/scope"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	fakedeployer "github.com/projectsveltos/libsveltos/lib/deployer/fake"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
)

var _ = Describe("Classifier: Reconciler", func() {
	var classifier *libsveltosv1beta1.Classifier

	BeforeEach(func() {
		classifier = getClassifierInstance(randomString())
	})

	It("Adds finalizer", func() {
		initObjects := []client.Object{
			classifier,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
		}

		classifierName := client.ObjectKey{
			Name: classifier.Name,
		}
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: classifierName,
		})
		Expect(err).ToNot(HaveOccurred())

		currentClassifier := &libsveltosv1beta1.Classifier{}
		err = c.Get(context.TODO(), classifierName, currentClassifier)
		Expect(err).ToNot(HaveOccurred())
		Expect(
			controllerutil.ContainsFinalizer(
				currentClassifier,
				libsveltosv1beta1.ClassifierFinalizer,
			),
		).Should(BeTrue())
	})

	It("Remove finalizer", func() {
		Expect(controllerutil.AddFinalizer(classifier, libsveltosv1beta1.ClassifierFinalizer)).To(BeTrue())

		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
			},
		}

		Expect(addTypeInformationToObject(scheme, cluster)).To(Succeed())

		initObjects := []client.Object{
			classifier,
			cluster,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		classifierName := client.ObjectKey{
			Name: classifier.Name,
		}

		currentClassifier := &libsveltosv1beta1.Classifier{}

		Expect(c.Get(context.TODO(), classifierName, currentClassifier)).To(Succeed())
		Expect(c.Delete(context.TODO(), currentClassifier)).To(Succeed())

		Expect(c.Get(context.TODO(), classifierName, currentClassifier)).To(Succeed())
		currentClassifier.Status.ClusterInfo = []libsveltosv1beta1.ClusterInfo{
			{
				Cluster: corev1.ObjectReference{
					Namespace:  cluster.Namespace,
					Name:       cluster.Name,
					APIVersion: cluster.APIVersion,
					Kind:       cluster.Kind,
				},
				Status: libsveltosv1beta1.SveltosStatusProvisioned,
				Hash:   []byte(randomString()),
			},
		}

		Expect(c.Status().Update(context.TODO(), currentClassifier)).To(Succeed())

		dep := fakedeployer.GetClient(context.TODO(),
			textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))), testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
			Deployer:      dep,
		}

		// Because Classifier is currently deployed in a Cluster (Status.ClusterInfo is set
		// indicating that) Reconcile won't be removed Finalizer
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: classifierName,
		})
		Expect(err).ToNot(HaveOccurred())

		err = c.Get(context.TODO(), classifierName, currentClassifier)
		Expect(err).ToNot(HaveOccurred())
		Expect(controllerutil.ContainsFinalizer(currentClassifier, libsveltosv1beta1.ClassifierFinalizer)).To(BeTrue())

		Expect(c.Get(context.TODO(), classifierName, currentClassifier)).To(Succeed())

		currentClassifier.Status.ClusterInfo = []libsveltosv1beta1.ClusterInfo{}
		Expect(c.Status().Update(context.TODO(), currentClassifier)).To(Succeed())

		// Because Classifier is currently deployed nowhere (Status.ClusterInfo is set
		// indicating that) Reconcile will be removed Finalizer
		_, err = reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: classifierName,
		})
		Expect(err).ToNot(HaveOccurred())

		err = c.Get(context.TODO(), classifierName, currentClassifier)
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("updateMatchingClustersAndRegistrations updates Classifier Status with matching clusters", func() {
		clusterNamespace := randomString()
		clusterName := randomString()
		classifierReport0 := getClassifierReport(classifier.Name, clusterNamespace, clusterName)
		classifierReport0.Spec.Match = true
		classifierReport1 := getClassifierReport(classifier.Name, randomString(), randomString())
		classifierReport1.Spec.Match = false
		classifierReport2 := getClassifierReport(randomString(), randomString(), randomString())

		initObjects := []client.Object{
			classifier,
			classifierReport0,
			classifierReport1,
			classifierReport2,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
		}

		classifierScope, err := scope.NewClassifierScope(scope.ClassifierScopeParams{
			Client:         c,
			Logger:         textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))),
			Classifier:     classifier,
			ControllerName: "classifier",
		})
		Expect(err).To(BeNil())

		Expect(controllers.UpdateMatchingClustersAndRegistrations(reconciler, context.TODO(), classifierScope,
			textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))).To(Succeed())

		Expect(classifier.Status.MachingClusterStatuses).ToNot(BeNil())
		Expect(len(classifier.Status.MachingClusterStatuses)).To(Equal(1))
		Expect(classifier.Status.MachingClusterStatuses[0].ManagedLabels).ToNot(BeNil())
		Expect(len(classifier.Status.MachingClusterStatuses[0].ManagedLabels)).To(Equal(len(classifier.Spec.ClassifierLabels)))
	})

	It("updateMatchingClustersAndRegistrations updates Classifier Status with detected misconfigurations", func() {
		clusterNamespace := randomString()
		clusterName := randomString()
		classifierReport0 := getClassifierReport(classifier.Name, clusterNamespace, clusterName)
		classifierReport0.Spec.Match = true

		// Create a second classifier with same ClassifierLabels as first classifier
		classifier1 := &libsveltosv1beta1.Classifier{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
			Spec: classifier.Spec,
		}
		// Have cluster be a match for this second classifier (so to have a conflict)
		classifierReport1 := getClassifierReport(classifier1.Name, clusterNamespace, clusterName)
		classifierReport1.Spec.Match = true

		initObjects := []client.Object{
			classifier,
			classifier1,
			classifierReport0,
			classifierReport1,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		manager, err := keymanager.GetKeyManagerInstance(ctx, c)
		Expect(err).To(BeNil())

		// Register classifier1 as manager for all labels in cluster
		// because of this classifier won't be able to manage any of its labels on the
		// cluster even though cluster is a match
		manager.RegisterClassifierForLabels(classifier1, clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeCapi)

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
		}

		classifierScope, err := scope.NewClassifierScope(scope.ClassifierScopeParams{
			Client:         c,
			Logger:         textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))),
			Classifier:     classifier,
			ControllerName: "classifier",
		})
		Expect(err).To(BeNil())

		Expect(controllers.UpdateMatchingClustersAndRegistrations(reconciler, context.TODO(), classifierScope,
			textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))).To(Succeed())

		Expect(classifier.Status.MachingClusterStatuses).ToNot(BeNil())
		Expect(len(classifier.Status.MachingClusterStatuses)).To(Equal(1))
		Expect(classifier.Status.MachingClusterStatuses[0].ClusterRef.Namespace).To(Equal(clusterNamespace))
		Expect(classifier.Status.MachingClusterStatuses[0].ClusterRef.Name).To(Equal(clusterName))
		Expect(classifier.Status.MachingClusterStatuses[0].ClusterRef.APIVersion).To(Equal(clusterv1.GroupVersion.String()))
		Expect(classifier.Status.MachingClusterStatuses[0].ClusterRef.Kind).To(Equal(clusterKind))
		Expect(len(classifier.Status.MachingClusterStatuses[0].ManagedLabels)).To(BeZero())
		Expect(len(classifier.Status.MachingClusterStatuses[0].UnManagedLabels)).To(Equal(len(classifier.Spec.ClassifierLabels)))
	})

	It("updateLabelsOnMatchingClusters updates CAPI Cluster labels", func() {
		clusterKey := randomString()
		clusterValue := randomString()
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Labels:    map[string]string{clusterKey: clusterValue},
			},
		}

		managedLabels := make([]string, 0)
		for i := range classifier.Spec.ClassifierLabels {
			managedLabels = append(managedLabels, classifier.Spec.ClassifierLabels[i].Key)
		}

		classifier.Status.MachingClusterStatuses = []libsveltosv1beta1.MachingClusterStatus{
			{
				ClusterRef: corev1.ObjectReference{
					Namespace:  cluster.Namespace,
					Name:       cluster.Name,
					Kind:       clusterKind,
					APIVersion: clusterv1.GroupVersion.String(),
				},
				ManagedLabels: managedLabels,
			},
		}

		initObjects := []client.Object{
			classifier,
			cluster,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
		}

		classifierScope, err := scope.NewClassifierScope(scope.ClassifierScopeParams{
			Client:         c,
			Logger:         textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))),
			Classifier:     classifier,
			ControllerName: "classifier",
		})
		Expect(err).To(BeNil())

		Expect(addTypeInformationToObject(scheme, cluster)).To(Succeed())

		// Call HandleLabelRegistrations so that Classifier is the manager for all its keys
		currentMatchingClusters := map[corev1.ObjectReference]bool{
			{Namespace: cluster.Namespace, Name: cluster.Name, APIVersion: cluster.APIVersion, Kind: cluster.Kind}: true,
		}
		oldMatchingClusters := map[corev1.ObjectReference]bool{}
		Expect(controllers.HandleLabelRegistrations(reconciler, context.TODO(), classifier, currentMatchingClusters,
			oldMatchingClusters, textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))).To(Succeed())

		Expect(controllers.UpdateLabelsOnMatchingClusters(reconciler, context.TODO(), classifierScope,
			textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))).To(Succeed())

		currentCluster := &clusterv1.Cluster{}
		Expect(c.Get(context.TODO(),
			types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, currentCluster)).To(Succeed())
		Expect(currentCluster.Labels).ToNot(BeNil())
		v, ok := currentCluster.Labels[clusterKey]
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal(clusterValue))

		for i := range classifier.Spec.ClassifierLabels {
			label := classifier.Spec.ClassifierLabels[i]
			v, ok := currentCluster.Labels[label.Key]
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(label.Value))
		}
	})

	It("removeAllRegistrations removes all label registrations", func() {
		label := randomString()
		clusterNamespace := randomString()
		clusterName := randomString()
		classifier.Spec.ClassifierLabels = []libsveltosv1beta1.ClassifierLabel{
			{Key: label, Value: randomString()},
		}
		classifier.Status.MachingClusterStatuses = []libsveltosv1beta1.MachingClusterStatus{
			{
				ClusterRef: corev1.ObjectReference{
					Namespace:  clusterNamespace,
					Name:       clusterName,
					Kind:       clusterKind,
					APIVersion: clusterv1.GroupVersion.String(),
				},
				ManagedLabels: []string{label},
			},
		}

		initObjects := []client.Object{
			classifier,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		manager, err := keymanager.GetKeyManagerInstance(ctx, c)
		Expect(err).To(BeNil())

		manager.RegisterClassifierForLabels(classifier, clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeCapi)
		Expect(manager.CanManageLabel(classifier, clusterNamespace, clusterName, label, libsveltosv1beta1.ClusterTypeCapi)).To(BeTrue())

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
		}

		classifierScope, err := scope.NewClassifierScope(scope.ClassifierScopeParams{
			Client:         c,
			Logger:         textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))),
			Classifier:     classifier,
			ControllerName: "classifier",
		})
		Expect(err).To(BeNil())

		Expect(controllers.RemoveAllRegistrations(reconciler, context.TODO(), classifierScope,
			textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))).To(Succeed())
		Expect(manager.CanManageLabel(classifier, clusterNamespace, clusterName, label,
			libsveltosv1beta1.ClusterTypeCapi)).To(BeFalse())
	})

	It("classifyLabels divides labels in managed and unmanaged", func() {
		clusterNamespace := randomString()
		clusterName := randomString()
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: clusterNamespace,
				Name:      clusterName,
			},
		}

		managedLabel := libsveltosv1beta1.ClassifierLabel{Key: randomString(), Value: randomString()}
		unManagedLabel := libsveltosv1beta1.ClassifierLabel{Key: randomString(), Value: randomString()}

		classifier.Spec.ClassifierLabels = []libsveltosv1beta1.ClassifierLabel{
			managedLabel, unManagedLabel}

		// Create an otherClassifier conflicting with first classifier for unManagedLabel
		otherClassifier := getClassifierInstance(randomString())
		otherClassifier.Spec.ClassifierLabels = []libsveltosv1beta1.ClassifierLabel{
			unManagedLabel,
		}

		initObjects := []client.Object{
			classifier, otherClassifier, cluster,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).
			WithObjects(initObjects...).Build()

		manager, err := keymanager.GetKeyManagerInstance(ctx, c)
		Expect(err).To(BeNil())
		// Register in this order, so otherClassifier can manage "unManagedLabel"
		// classifier can only manage "managedLabel" and has conflict for "unManagedLabel"
		manager.RegisterClassifierForLabels(otherClassifier, clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeCapi)
		manager.RegisterClassifierForLabels(classifier, clusterNamespace, clusterName, libsveltosv1beta1.ClusterTypeCapi)

		reconciler := &controllers.ClassifierReconciler{
			Client:        c,
			Scheme:        scheme,
			ClusterMap:    make(map[corev1.ObjectReference]*libsveltosset.Set),
			ClassifierMap: make(map[corev1.ObjectReference]*libsveltosset.Set),
			Mux:           sync.Mutex{},
		}

		clusterRef := &corev1.ObjectReference{
			Namespace:  clusterNamespace,
			Name:       clusterName,
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Cluster",
		}

		managed, unManaged, err := controllers.ClassifyLabels(reconciler, context.TODO(), classifier,
			clusterRef, textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))))
		Expect(err).To(BeNil())
		Expect(len(managed)).To(Equal(1))
		Expect(len(unManaged)).To(Equal(1))
		Expect(unManaged[0].Key).To(Equal(unManagedLabel.Key))
	})
})

var _ = Describe("ClassifierReconciler: requeue methods", func() {
	var classifier *libsveltosv1beta1.Classifier
	var cluster *clusterv1.Cluster

	BeforeEach(func() {
		cluster = prepareCluster()
		controllers.SetVersion(version)

		classifier = getClassifierInstance(randomString())
	})

	AfterEach(func() {
		ns := &corev1.Namespace{}
		Expect(testEnv.Get(context.TODO(), types.NamespacedName{Name: cluster.Namespace}, ns)).To(Succeed())
		Expect(testEnv.Delete(context.TODO(), classifier)).To(Succeed())
		Expect(testEnv.Delete(context.TODO(), cluster)).To(Succeed())
		Expect(testEnv.Delete(context.TODO(), ns)).To(Succeed())
	})

	It("RequeueClassifierForCluster returns all existing Classifiers", func() {
		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, classifier)).To(Succeed())

		classifierName := client.ObjectKey{
			Name: classifier.Name,
		}

		dep := fakedeployer.GetClient(context.TODO(), textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))),
			testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		clusterProfileReconciler := getClassifierReconciler(testEnv.Client, dep)
		_, err := clusterProfileReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: classifierName,
		})
		Expect(err).ToNot(HaveOccurred())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			classifierList := controllers.RequeueClassifierForCluster(clusterProfileReconciler,
				context.TODO(), cluster)
			result := reconcile.Request{NamespacedName: types.NamespacedName{Name: classifier.Name}}
			for i := range classifierList {
				if classifierList[i] == result {
					return true
				}
			}
			return false
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("RequeueClassifierForMachine returns correct Classifier for a CAPI machine", func() {
		cpMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Namespace,
				Name:      cluster.Name + randomString(),
				Labels: map[string]string{
					clusterv1.ClusterNameLabel:         cluster.Name,
					clusterv1.MachineControlPlaneLabel: "ok",
				},
			},
		}
		cpMachine.Status.SetTypedPhase(clusterv1.MachinePhaseRunning)

		Expect(testEnv.Create(context.TODO(), classifier)).To(Succeed())
		Expect(testEnv.Create(context.TODO(), cpMachine)).To(Succeed())

		Expect(waitForObject(context.TODO(), testEnv.Client, cpMachine)).To(Succeed())

		classifierName := client.ObjectKey{
			Name: classifier.Name,
		}

		dep := fakedeployer.GetClient(context.TODO(), textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1))),
			testEnv.Client)
		Expect(dep.RegisterFeatureID(libsveltosv1beta1.FeatureClassifier)).To(Succeed())

		clusterProfileReconciler := getClassifierReconciler(testEnv.Client, dep)
		_, err := clusterProfileReconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: classifierName,
		})
		Expect(err).ToNot(HaveOccurred())

		// Eventual loop so testEnv Cache is synced
		Eventually(func() bool {
			classifierList := controllers.RequeueClassifierForMachine(clusterProfileReconciler,
				context.TODO(), cpMachine)
			result := reconcile.Request{NamespacedName: types.NamespacedName{Name: classifier.Name}}
			for i := range classifierList {
				if classifierList[i] == result {
					return true
				}
			}
			return false
		}, timeout, pollingInterval).Should(BeTrue())
	})
})
