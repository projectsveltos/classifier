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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/klogr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	classifyv1alpha1 "github.com/projectsveltos/classifier/api/v1alpha1"
	"github.com/projectsveltos/classifier/controllers"
)

var _ = Describe("Classifier Predicates: ClusterPredicates", func() {
	var logger logr.Logger
	var cluster *clusterv1.Cluster

	BeforeEach(func() {
		logger = klogr.New()
		cluster = &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      upstreamClusterNamePrefix + randomString(),
				Namespace: "predicates" + randomString(),
			},
		}
	})

	It("Create reprocesses when v1Cluster is unpaused", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)

		cluster.Spec.Paused = false

		e := event.CreateEvent{
			Object: cluster,
		}

		result := clusterPredicate.Create(e)
		Expect(result).To(BeTrue())
	})
	It("Create does not reprocess when v1Cluster is paused", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)

		cluster.Spec.Paused = true
		cluster.Annotations = map[string]string{clusterv1.PausedAnnotation: "true"}

		e := event.CreateEvent{
			Object: cluster,
		}

		result := clusterPredicate.Create(e)
		Expect(result).To(BeFalse())
	})
	It("Delete does reprocess ", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)

		e := event.DeleteEvent{
			Object: cluster,
		}

		result := clusterPredicate.Delete(e)
		Expect(result).To(BeTrue())
	})
	It("Update reprocesses when v1Cluster paused changes from true to false", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)

		cluster.Spec.Paused = false

		oldCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
		}
		oldCluster.Spec.Paused = true
		oldCluster.Annotations = map[string]string{clusterv1.PausedAnnotation: "true"}

		e := event.UpdateEvent{
			ObjectNew: cluster,
			ObjectOld: oldCluster,
		}

		result := clusterPredicate.Update(e)
		Expect(result).To(BeTrue())
	})
	It("Update does not reprocess when v1Cluster paused changes from false to true", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)

		cluster.Spec.Paused = true
		cluster.Annotations = map[string]string{clusterv1.PausedAnnotation: "true"}
		oldCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
		}
		oldCluster.Spec.Paused = false

		e := event.UpdateEvent{
			ObjectNew: cluster,
			ObjectOld: oldCluster,
		}

		result := clusterPredicate.Update(e)
		Expect(result).To(BeFalse())
	})
	It("Update does not reprocess when v1Cluster paused has not changed", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)

		cluster.Spec.Paused = false
		oldCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
		}
		oldCluster.Spec.Paused = false

		e := event.UpdateEvent{
			ObjectNew: cluster,
			ObjectOld: oldCluster,
		}

		result := clusterPredicate.Update(e)
		Expect(result).To(BeFalse())
	})
	It("Update reprocesses when v1Cluster Status ControlPlaneReady changes", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)
		cluster.Status.ControlPlaneReady = true

		oldCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
			Status: clusterv1.ClusterStatus{
				ControlPlaneReady: false,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: cluster,
			ObjectOld: oldCluster,
		}

		result := clusterPredicate.Update(e)
		Expect(result).To(BeTrue())
	})
	It("Update reprocesses when v1Cluster Status InfrastructureReady changes", func() {
		clusterPredicate := controllers.ClusterPredicates(logger)
		cluster.Status.InfrastructureReady = true

		oldCluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
			Status: clusterv1.ClusterStatus{
				InfrastructureReady: false,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: cluster,
			ObjectOld: oldCluster,
		}

		result := clusterPredicate.Update(e)
		Expect(result).To(BeTrue())
	})
})

var _ = Describe("Classifier Predicates: MachinePredicates", func() {
	var logger logr.Logger
	var machine *clusterv1.Machine

	BeforeEach(func() {
		logger = klogr.New()
		machine = &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      upstreamMachineNamePrefix + randomString(),
				Namespace: "predicates" + randomString(),
			},
		}
	})

	It("Create reprocesses when v1Machine is Running", func() {
		machinePredicate := controllers.MachinePredicates(logger)

		machine.Status.Phase = string(clusterv1.MachinePhaseRunning)

		e := event.CreateEvent{
			Object: machine,
		}

		result := machinePredicate.Create(e)
		Expect(result).To(BeTrue())
	})
	It("Create does not reprocess when v1Machine is not Running", func() {
		machinePredicate := controllers.MachinePredicates(logger)

		e := event.CreateEvent{
			Object: machine,
		}

		result := machinePredicate.Create(e)
		Expect(result).To(BeFalse())
	})
	It("Delete does not reprocess ", func() {
		machinePredicate := controllers.MachinePredicates(logger)

		e := event.DeleteEvent{
			Object: machine,
		}

		result := machinePredicate.Delete(e)
		Expect(result).To(BeFalse())
	})
	It("Update reprocesses when v1Machine Phase changed from not running to running", func() {
		machinePredicate := controllers.MachinePredicates(logger)

		machine.Status.Phase = string(clusterv1.MachinePhaseRunning)

		oldMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      machine.Name,
				Namespace: machine.Namespace,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: machine,
			ObjectOld: oldMachine,
		}

		result := machinePredicate.Update(e)
		Expect(result).To(BeTrue())
	})
	It("Update does not reprocess when v1Machine Phase changes from not Phase not set to Phase set but not running", func() {
		machinePredicate := controllers.MachinePredicates(logger)

		machine.Status.Phase = "Provisioning"

		oldMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      machine.Name,
				Namespace: machine.Namespace,
			},
		}

		e := event.UpdateEvent{
			ObjectNew: machine,
			ObjectOld: oldMachine,
		}

		result := machinePredicate.Update(e)
		Expect(result).To(BeFalse())
	})
	It("Update does not reprocess when v1Machine Phases does not change", func() {
		machinePredicate := controllers.MachinePredicates(logger)
		machine.Status.Phase = string(clusterv1.MachinePhaseRunning)

		oldMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      machine.Name,
				Namespace: machine.Namespace,
			},
		}
		oldMachine.Status.Phase = machine.Status.Phase

		e := event.UpdateEvent{
			ObjectNew: machine,
			ObjectOld: oldMachine,
		}

		result := machinePredicate.Update(e)
		Expect(result).To(BeFalse())
	})
})

var _ = Describe("Classifier Predicates: ClassifierReportPredicate", func() {
	var logger logr.Logger
	var report *classifyv1alpha1.ClassifierReport

	BeforeEach(func() {
		logger = klogr.New()
		report = &classifyv1alpha1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      upstreamClusterNamePrefix + randomString(),
				Namespace: "predicates" + randomString(),
			},
		}
	})

	It("Create reprocesses always", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		e := event.CreateEvent{
			Object: report,
		}

		result := reportPredicate.Create(e)
		Expect(result).To(BeTrue())
	})
	It("Delete does reprocess ", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		e := event.DeleteEvent{
			Object: report,
		}

		result := reportPredicate.Delete(e)
		Expect(result).To(BeTrue())
	})
	It("Update reprocesses when Spec.Match changes", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		report.Spec.Match = true

		oldReport := &classifyv1alpha1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      report.Name,
				Namespace: report.Namespace,
			},
		}
		oldReport.Spec.Match = false

		e := event.UpdateEvent{
			ObjectNew: report,
			ObjectOld: oldReport,
		}

		result := reportPredicate.Update(e)
		Expect(result).To(BeTrue())
	})
	It("Update does not reprocess when Spec.Match does not change", func() {
		reportPredicate := controllers.ClassifierReportPredicate(logger)

		report.Spec.Match = true
		report.Labels = map[string]string{randomString(): randomString()}

		oldReport := &classifyv1alpha1.ClassifierReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      report.Name,
				Namespace: report.Namespace,
			},
		}
		oldReport.Spec.Match = true

		e := event.UpdateEvent{
			ObjectNew: report,
			ObjectOld: oldReport,
		}

		result := reportPredicate.Update(e)
		Expect(result).To(BeFalse())
	})
})
