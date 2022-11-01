# classifier
Sveltos Classifier dynamically classify a cluster based on run time information (Kubernetes version, deployed resources and more)

Classifier currently supports following classification criterias:
1. Kubernetes version
2. Kubernetes resources

For instance, posting this Classifier instance will have match any Cluster whose Kubernetes version is greater than or equal to "v1.25.0"

```
apiVersion: lib.projectsveltos.io/v1alpha1
kind: Classifier
metadata:
  name: kubernetes-v1.25
spec:
  classifierLabels:
  - key: k8s-version
    value: v1.25
    kubernetesVersion:
      comparison: GreaterThanOrEqualTo
      version: 1.25.0
```

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