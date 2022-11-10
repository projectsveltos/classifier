# classifier
Sveltos Classifier can be used to dynamically classify a cluster based on its run time configuration(Kubernetes version, deployed resources and more).

Classifier currently supports following classification criterias:
1. Kubernetes version
2. Kubernetes resources

For instance, posting this Classifier instance will have match any Cluster whose Kubernetes version is greater than or equal to "v1.24.0" and strictly less than "v1.25.0"

```
apiVersion: lib.projectsveltos.io/v1alpha1
kind: Classifier
metadata:
  name: kubernetes-v1.24
spec:
  classifierLabels:
  - key: k8s-version
    value: v1.24
  kubernetesVersionConstraints:
  - comparison: GreaterThanOrEqualTo
    version: 1.24.0
  - comparison: LessThan
    version: 1.25.0    
```

When a cluster is a match for a Classifier instances, all classifierLabels will be automatically added to the Cluster instance.

## A simple use case: upgrade helm charts automatically when Kubernetes cluster is upgraded
Suppose you are managing several Kubernetes clusters with different versions.
And you want to deploy:
1. OPA Gatekeeper version 3.10.0 in any Kubernetes cluster whose version is >= v1.25.0
2. OPA Gatekeeper version 3.9.0 in any Kubernetes cluster whose version is >= v1.24.0 && < v1.25.0

You can create following ClusterProfiles and Classifiers in the management cluster:
```
apiVersion: config.projectsveltos.io/v1alpha1
kind: ClusterProfile
metadata:
  name: deploy-gatekeeper-3-10
spec:
  clusterSelector: gatekeeper=v3-10
  syncMode: Continuous
  helmCharts:
  - repositoryURL: https://open-policy-agent.github.io/gatekeeper/charts
    repositoryName: gatekeeper
    chartName: gatekeeper/gatekeeper
    chartVersion:  3.10.0
    releaseName: gatekeeper
    releaseNamespace: gatekeeper
    helmChartAction: Install
```

```
apiVersion: config.projectsveltos.io/v1alpha1
kind: ClusterProfile
metadata:
  name: deploy-gatekeeper-3-9
spec:
  clusterSelector: gatekeeper=v3-9
  syncMode: Continuous
  helmCharts:
  - repositoryURL: https://open-policy-agent.github.io/gatekeeper/charts
    repositoryName: gatekeeper
    chartName: gatekeeper/gatekeeper
    chartVersion:  3.9.0
    releaseName: gatekeeper
    releaseNamespace: gatekeeper
    helmChartAction: Install
```

Then create following Classifiers

```
apiVersion: lib.projectsveltos.io/v1alpha1
kind: Classifier
metadata:
  name: deploy-gatekeeper-3-10
spec:
  classifierLabels:
  - key: gatekeeper
    value: v3-10
  kubernetesVersionConstraints:
  - comparison: GreaterThanOrEqualTo
    version: 1.25.0
```

```
apiVersion: lib.projectsveltos.io/v1alpha1
kind: Classifier
metadata:
  name: deploy-gatekeeper-3-9
spec:
  classifierLabels:
  - key: gatekeeper
    value: v3-9
  kubernetesVersionConstraints:
  - comparison: GreaterThanOrEqualTo
    version: 1.24.0
  - comparison: LessThan
    version: 1.25.0
```

Because of above configuration:
1. Any cluster with a Kubernetes version v1.24.x will get label _gatekeeper:v3.9_ added and because of that Gatekeeper 3.9.0 helm chart will be deployed;
2. Any cluster with a Kubernetes version v1.25.x will get label _gatekeeper:v3.10_ added and because of that Gatekeeper 3.10.0 helm chart will be deployed;
3. As soon a cluster is upgraded from Kubernetes version v1.24.x to v1.25.x, Gatekeeper helm chart will be automatically upgraded from 3.9.0 to 3.10.0

## Install Sveltos classifier on any local or remote Kubernetes cluster.

Assumptions are:
1. management cluster with ClusterAPI is available;
2. Sveltos manager is deployed.


Apply needed CRDs:
1. Classifier CRD
```
kubectl apply -f https://raw.githubusercontent.com/projectsveltos/libsveltos/dev/config/crd/bases/lib.projectsveltos.io_classifiers.yaml
```

2. ClassifierReport CRD
```
kubectl apply -f https://raw.githubusercontent.com/projectsveltos/libsveltos/dev/config/crd/bases/lib.projectsveltos.io_classifierreports.yaml
```

3. DebuggingConfiguration CRD
```
kubectl apply -f https://raw.githubusercontent.com/projectsveltos/libsveltos/dev/config/crd/bases/lib.projectsveltos.io_debuggingconfigurations.yaml
```

Finally install classifier controller
```
kubectl create -f  https://raw.githubusercontent.com/projectsveltos/classifier/dev/manifest/manifest.yaml
```
