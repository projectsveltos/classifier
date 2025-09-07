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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
)

var (
	k8sClient           client.Client
	scheme              *runtime.Scheme
	kindWorkloadCluster *unstructured.Unstructured // This is the name of the kind workload cluster, in the form namespace/name

)

const (
	timeout         = 2 * time.Minute
	pollingInterval = 5 * time.Second
)

const (
	deplNamespace        = "projectsveltos"
	deplName             = "classifier-manager"
	managerContainerName = "manager"
)

var (
	configMapConfig = `    apiVersion: v1
    data:
      patch: |-
        apiVersion: apps/v1
        kind: Deployment
        metadata:
          name: "sveltos-ag*"
        spec:
          template:
            spec:
              containers:
              - name: manager
                resources:
                  requests:
                    memory: 256Mi
    kind: ConfigMap
    metadata:
      name: sveltos-agent
      namespace: projectsveltos`
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

var _ = SynchronizedBeforeSuite(func() []byte {
	ctrl.SetLogger(klog.Background())

	restConfig := ctrl.GetConfigOrDie()
	// To get rid of the annoying request.go log
	restConfig.QPS = 100
	restConfig.Burst = 100

	scheme = runtime.NewScheme()

	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(libsveltosv1beta1.AddToScheme(scheme)).To(Succeed())

	var err error
	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	cm, err := k8s_utils.GetUnstructured([]byte(configMapConfig))
	Expect(err).To(BeNil())
	err = k8sClient.Create(context.TODO(), cm)
	if err != nil {
		// BeforeSuite runs on every parallel process
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}

	setSveltosAgentConfig(cm.GetName())

	setClassifierMode(controllers.CollectFromManagementCluster)

	return []byte{}
}, func(data []byte) {
	restConfig := ctrl.GetConfigOrDie()
	// To get rid of the annoying request.go log
	restConfig.QPS = 100
	restConfig.Burst = 100

	scheme = runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(clusterv1.AddToScheme(scheme)).To(Succeed())
	Expect(libsveltosv1beta1.AddToScheme(scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())

	var err error
	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	if isCAPIInstalled(context.TODO(), k8sClient) {
		verifyCAPICluster()
	} else {
		verifySveltosCluster()
	}
})

func verifySveltosCluster() {
	clusterList := &libsveltosv1beta1.SveltosClusterList{}
	listOptions := []client.ListOption{
		client.MatchingLabels(
			map[string]string{"cluster-name": "clusterapi-workload"}, // This label is added by Makefile
		),
	}

	Expect(k8sClient.List(context.TODO(), clusterList, listOptions...)).To(Succeed())
	Expect(len(clusterList.Items)).To(Equal(1))
	unstructuredMap, err :=
		runtime.DefaultUnstructuredConverter.ToUnstructured(&clusterList.Items[0])
	Expect(err).To(BeNil())

	kindWorkloadCluster = &unstructured.Unstructured{Object: unstructuredMap}

	Byf("Set Cluster %s:%s unpaused and add label %s/%s", kindWorkloadCluster.GetNamespace(), kindWorkloadCluster.GetName(), key, value)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: kindWorkloadCluster.GetName()},
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
}

func verifyCAPICluster() {
	var err error
	clusterList := &clusterv1.ClusterList{}
	listOptions := []client.ListOption{
		client.MatchingLabels(
			map[string]string{clusterv1.ClusterNameLabel: "clusterapi-workload"},
		),
	}

	Expect(k8sClient.List(context.TODO(), clusterList, listOptions...)).To(Succeed())
	Expect(len(clusterList.Items)).To(Equal(1))

	unstructuredMap, err :=
		runtime.DefaultUnstructuredConverter.ToUnstructured(&clusterList.Items[0])
	Expect(err).To(BeNil())

	kindWorkloadCluster = &unstructured.Unstructured{Object: unstructuredMap}

	Byf("Wait for machine in cluster %s/%s to be ready", kindWorkloadCluster.GetNamespace(), kindWorkloadCluster.GetName())
	Eventually(func() bool {
		machineList := &clusterv1.MachineList{}
		listOptions = []client.ListOption{
			client.InNamespace(kindWorkloadCluster.GetNamespace()),
			client.MatchingLabels{clusterv1.ClusterNameLabel: kindWorkloadCluster.GetName()},
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

	Byf("Set Cluster %s:%s unpaused and add label %s/%s", kindWorkloadCluster.GetNamespace(), kindWorkloadCluster.GetName(), key, value)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.GetNamespace(), Name: kindWorkloadCluster.GetName()},
			currentCluster)).To(Succeed())

		currentLabels := currentCluster.Labels
		if currentLabels == nil {
			currentLabels = make(map[string]string)
		}
		currentLabels[key] = value
		currentCluster.Labels = currentLabels
		paused := false
		currentCluster.Spec.Paused = &paused

		return k8sClient.Update(context.TODO(), currentCluster)
	})
	Expect(err).To(BeNil())
}

// isCAPIInstalled returns true if CAPI is installed, false otherwise
func isCAPIInstalled(ctx context.Context, c client.Client) bool {
	clusterCRD := &apiextensionsv1.CustomResourceDefinition{}

	err := c.Get(ctx, types.NamespacedName{Name: "clusters.cluster.x-k8s.io"}, clusterCRD)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false
		}
		Expect(err).To(BeNil())
	}

	return true
}

func setClassifierMode(reportMode controllers.ReportMode) {
	depl := &appsv1.Deployment{}

	var from, to string
	switch reportMode {
	case controllers.CollectFromManagementCluster:
		from = fmt.Sprintf("--report-mode=%d", controllers.AgentSendReportsNoGateway)
		to = fmt.Sprintf("--report-mode=%d", controllers.CollectFromManagementCluster)
	case controllers.AgentSendReportsNoGateway:
		from = fmt.Sprintf("--report-mode=%d", controllers.CollectFromManagementCluster)
		to = fmt.Sprintf("--report-mode=%d", controllers.AgentSendReportsNoGateway)
	default:
		// Never get here
		Expect(1).To(BeZero())
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		Byf("Get classifier deployment")
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: deplNamespace, Name: deplName},
			depl)).To(Succeed())

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

func setSveltosAgentConfig(cmName string) {
	depl := &appsv1.Deployment{}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		Byf("Get classifier deployment")
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: deplNamespace, Name: deplName},
			depl)).To(Succeed())

		add := true
		for i := range depl.Spec.Template.Spec.Containers {
			if depl.Spec.Template.Spec.Containers[i].Name == managerContainerName {
				for j := range depl.Spec.Template.Spec.Containers[i].Args {
					if strings.Contains(depl.Spec.Template.Spec.Containers[i].Args[j], "sveltos-agent-config") {
						add = false
					}
				}
			}
		}

		if add {
			Byf("Set sveltos-agent-config")
			for i := range depl.Spec.Template.Spec.Containers {
				if depl.Spec.Template.Spec.Containers[i].Name == managerContainerName {
					depl.Spec.Template.Spec.Containers[i].Args = append(
						depl.Spec.Template.Spec.Containers[i].Args,
						fmt.Sprintf("--sveltos-agent-config=%s", cmName))
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

func isAgentLessMode() bool {
	By("Getting classifier pod")
	classfierDepl := &appsv1.Deployment{}
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: deplNamespace, Name: deplName},
		classfierDepl)).To(Succeed())

	Expect(len(classfierDepl.Spec.Template.Spec.Containers)).To(Equal(1))

	for i := range classfierDepl.Spec.Template.Spec.Containers[0].Args {
		if strings.Contains(classfierDepl.Spec.Template.Spec.Containers[0].Args[i], "agent-in-mgmt-cluster=true") {
			By("Classifier in agentless mode")
			return true
		}
	}

	return false
}
