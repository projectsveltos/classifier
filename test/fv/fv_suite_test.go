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

package fv_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TwiN/go-color"
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

var (
	k8sClient           client.Client
	scheme              *runtime.Scheme
	kindWorkloadCluster *clusterv1.Cluster // This is the name of the kind workload cluster, in the form namespace/name
)

const (
	timeout         = 2 * time.Minute
	pollingInterval = 5 * time.Second
)

func TestFv(t *testing.T) {
	RegisterFailHandler(Fail)

	suiteConfig, reporterConfig := GinkgoConfiguration()
	reporterConfig.FullTrace = true
	reporterConfig.JSONReport = "out.json"
	report := func(report ginkgotypes.Report) {
		for i := range report.SpecReports {
			specReport := report.SpecReports[i]
			if specReport.State.String() == "skipped" {
				GinkgoWriter.Printf(color.Colorize(color.Blue, fmt.Sprintf("[Skipped]: %s\n", specReport.FullText())))
			}
		}
		for i := range report.SpecReports {
			specReport := report.SpecReports[i]
			if specReport.Failed() {
				GinkgoWriter.Printf(color.Colorize(color.Red, fmt.Sprintf("[Failed]: %s\n", specReport.FullText())))
			}
		}
	}
	ReportAfterSuite("report", report)

	RunSpecs(t, "FV Suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	ctrl.SetLogger(klog.Background())

	restConfig := ctrl.GetConfigOrDie()
	// To get rid of the annoying request.go log
	restConfig.QPS = 100
	restConfig.Burst = 100

	scheme = runtime.NewScheme()

	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(clusterv1.AddToScheme(scheme)).To(Succeed())
	Expect(libsveltosv1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())

	var err error
	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	setClassifierMode(controllers.CollectFromManagementCluster)

	clusterList := &clusterv1.ClusterList{}
	listOptions := []client.ListOption{
		client.MatchingLabels(
			map[string]string{clusterv1.ClusterNameLabel: "clusterapi-workload"},
		),
	}

	Expect(k8sClient.List(context.TODO(), clusterList, listOptions...)).To(Succeed())
	Expect(len(clusterList.Items)).To(Equal(1))
	kindWorkloadCluster = &clusterList.Items[0]

	Byf("Wait for machine in cluster %s/%s to be ready", kindWorkloadCluster.Namespace, kindWorkloadCluster.Name)
	Eventually(func() bool {
		machineList := &clusterv1.MachineList{}
		listOptions = []client.ListOption{
			client.InNamespace(kindWorkloadCluster.Namespace),
			client.MatchingLabels{clusterv1.ClusterNameLabel: kindWorkloadCluster.Name},
		}
		err = k8sClient.List(context.TODO(), machineList, listOptions...)
		if err != nil {
			return false
		}
		for i := range machineList.Items {
			m := machineList.Items[i]
			if m.Status.Phase == string(clusterv1.MachinePhaseRunning) {
				return true
			}
		}
		return false
	}, timeout, pollingInterval).Should(BeTrue())

	Byf("Set Cluster %s:%s unpaused and add label %s/%s", kindWorkloadCluster.Namespace, kindWorkloadCluster.Name, key, value)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())

		currentLabels := currentCluster.Labels
		if currentLabels == nil {
			currentLabels = make(map[string]string)
		}
		currentLabels[key] = value
		currentCluster.Labels = currentLabels
		currentCluster.Spec.Paused = false

		return k8sClient.Update(context.TODO(), currentCluster)
	})
	Expect(err).To(BeNil())
})

func setClassifierMode(reportMode controllers.ReportMode) {
	deplNamespace := "projectsveltos"
	deplName := "classifier-manager"
	depl := &appsv1.Deployment{}

	var from, to string
	if reportMode == controllers.CollectFromManagementCluster {
		from = fmt.Sprintf("--report-mode=%d", controllers.AgentSendReportsNoGateway)
		to = fmt.Sprintf("--report-mode=%d", controllers.CollectFromManagementCluster)
	} else if reportMode == controllers.AgentSendReportsNoGateway {
		from = fmt.Sprintf("--report-mode=%d", controllers.CollectFromManagementCluster)
		to = fmt.Sprintf("--report-mode=%d", controllers.AgentSendReportsNoGateway)
	} else {
		// Never get here
		Expect(1).To(BeZero())
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		Byf("Get classifier deployment")
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: deplNamespace, Name: deplName},
			depl)).To(Succeed())

		managerContainerName := "manager"
		Byf("Setting Classifer report mode to collect classifierReports")
		for i := range depl.Spec.Template.Spec.Containers {
			if depl.Spec.Template.Spec.Containers[i].Name == managerContainerName {
				for j := range depl.Spec.Template.Spec.Containers[i].Args {
					depl.Spec.Template.Spec.Containers[i].Args[j] =
						strings.ReplaceAll(depl.Spec.Template.Spec.Containers[i].Args[j],
							from, to)
				}
			}
		}

		found := false
		for i := range depl.Spec.Template.Spec.Containers {
			if depl.Spec.Template.Spec.Containers[i].Name == managerContainerName {
				for j := range depl.Spec.Template.Spec.Containers[i].Args {
					if strings.Contains(depl.Spec.Template.Spec.Containers[i].Args[j],
						"--control-plane-endpoint=https://sveltos-management-control-plane:6443") {

						found = true
					}
				}
				if !found {
					depl.Spec.Template.Spec.Containers[i].Args = append(depl.Spec.Template.Spec.Containers[i].Args,
						"--control-plane-endpoint=https://sveltos-management-control-plane:6443")
				}
			}
		}

		return k8sClient.Update(context.TODO(), depl)
	})

	Expect(err).To(BeNil())

	Byf("Waiting for deployment replicas to be available")
	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: deplNamespace, Name: deplName}, depl)
		if err != nil {
			return false
		}
		return depl.Status.AvailableReplicas == *depl.Spec.Replicas
	}, timeout, pollingInterval).Should(BeTrue())
}
