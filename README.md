[![CI](https://github.com/projectsveltos/classifier/actions/workflows/main.yaml/badge.svg)](https://github.com/projectsveltos/classifier/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/projectsveltos/classifier)](https://goreportcard.com/report/github.com/projectsveltos/classifier)
[![Slack](https://img.shields.io/badge/join%20slack-%23projectsveltos-brighteen)](https://join.slack.com/t/projectsveltos/shared_invite/zt-1hraownbr-W8NTs6LTimxLPB8Erj8Q6Q)
[![License](https://img.shields.io/badge/license-Apache-blue.svg)](LICENSE)
[![Twitter Follow](https://img.shields.io/twitter/follow/projectsveltos?style=social)](https://twitter.com/projectsveltos)

# classifier

<img src="https://raw.githubusercontent.com/projectsveltos/sveltos/main/docs/assets/logo.png" width="200">

Please refere to sveltos [documentation](https://projectsveltos.github.io/sveltos/).

Sveltos Classifier is an componet of the Sveltos project and it is used to dynamically classify a cluster based on its runtime configuration (Kubernetes version, deployed resources and more).

Classifier currently supports the following classification criterias:
1. Kubernetes version
2. Kubernetes resources

For instance, this Classifier instance will match any cluster whose Kubernetes version is greater than or equal to "v1.24.0" and strictly less than "v1.25.0"

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

Refer to [examples](./examples/) for more complex examples.

## Sveltos Classifier in action

![Sveltos Classifier in action](https://github.com/projectsveltos/demos/blob/main/classifier/classifier.gif)

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

With the above configuration:
1. Any cluster with a Kubernetes version v1.24.x will get label _gatekeeper:v3.9_ added and because of that Gatekeeper 3.9.0 helm chart will be deployed;
2. Any cluster with a Kubernetes version v1.25.x will get label _gatekeeper:v3.10_ added and because of that Gatekeeper 3.10.0 helm chart will be deployed;
3. As soon a cluster is upgraded from Kubernetes version v1.24.x to v1.25.x, Gatekeeper helm chart will be automatically upgraded from 3.9.0 to 3.10.0

## Contributing [![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/projectsveltos/sveltos-manager/issues)
:heart: Your contributions are always welcome!
If you have questions, noticed any bug or want to get the latest project news, you can connect with us in the following ways:
1. Open a bug/feature enhancement on github;
2. Chat with us on the Slack in the [#projectsveltos](https://join.slack.com/t/projectsveltos/shared_invite/zt-1hraownbr-W8NTs6LTimxLPB8Erj8Q6Q) channel;
3. Submit a pull request.

## License

Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
