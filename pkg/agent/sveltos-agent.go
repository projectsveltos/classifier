// Generated by *go generate* - DO NOT EDIT
/*
Copyright 2022-23. projectsveltos.io. All rights reserved.

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
package agent

var sveltosAgentYAML = []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: projectsveltos
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: sveltos-agent-manager
  namespace: projectsveltos
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sveltos-agent-manager-role
rules:
- apiGroups:
  - '*'
  resources:
  - '*'
  verbs:
  - get
  - impersonate
  - list
  - watch
- apiGroups:
  - apiextensions.k8s.io
  resources:
  - customresourcedefinitions
  verbs:
  - get
  - list
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - classifierreports
  verbs:
  - create
  - delete
  - get
  - list
  - patch
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - classifierreports/status
  verbs:
  - get
  - patch
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - classifiers
  verbs:
  - get
  - list
  - patch
  - update
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - classifiers/finalizers
  verbs:
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - debuggingconfigurations
  verbs:
  - get
  - list
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - eventreports
  verbs:
  - create
  - delete
  - get
  - list
  - patch
  - update
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - eventreports/finalizers
  verbs:
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - eventreports/status
  verbs:
  - get
  - patch
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - eventsources
  verbs:
  - get
  - list
  - patch
  - update
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - eventsources/finalizers
  verbs:
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - healthcheckreports
  verbs:
  - create
  - delete
  - get
  - list
  - patch
  - update
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - healthcheckreports/finalizers
  verbs:
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - healthcheckreports/status
  verbs:
  - get
  - patch
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - healthchecks
  verbs:
  - get
  - list
  - patch
  - update
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - healthchecks/finalizers
  verbs:
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - reloaderreports
  verbs:
  - create
  - delete
  - get
  - list
  - patch
  - update
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - reloaderreports/status
  verbs:
  - get
  - patch
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - reloaders
  verbs:
  - get
  - list
  - patch
  - watch
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - reloaders/finalizers
  verbs:
  - patch
  - update
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sveltos-agent-metrics-reader
rules:
- nonResourceURLs:
  - /metrics
  verbs:
  - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sveltos-agent-proxy-role
rules:
- apiGroups:
  - authentication.k8s.io
  resources:
  - tokenreviews
  verbs:
  - create
- apiGroups:
  - authorization.k8s.io
  resources:
  - subjectaccessreviews
  verbs:
  - create
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sveltos-agent-manager-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: sveltos-agent-manager-role
subjects:
- kind: ServiceAccount
  name: sveltos-agent-manager
  namespace: projectsveltos
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sveltos-agent-proxy-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: sveltos-agent-proxy-role
subjects:
- kind: ServiceAccount
  name: sveltos-agent-manager
  namespace: projectsveltos
---
apiVersion: v1
kind: Service
metadata:
  labels:
    control-plane: sveltos-agent
  name: sveltos-agent-manager-metrics-service
  namespace: projectsveltos
spec:
  ports:
  - name: https
    port: 8443
    protocol: TCP
    targetPort: https
  selector:
    control-plane: sveltos-agent
---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    control-plane: sveltos-agent
  name: sveltos-agent-manager
  namespace: projectsveltos
spec:
  replicas: 1
  selector:
    matchLabels:
      control-plane: sveltos-agent
  template:
    metadata:
      annotations:
        kubectl.kubernetes.io/default-container: manager
      labels:
        control-plane: sveltos-agent
    spec:
      containers:
      - args:
        - --health-probe-bind-address=:8081
        - --metrics-bind-address=127.0.0.1:8080
        - --v=5
        - --cluster-namespace=
        - --cluster-name=
        - --cluster-type=
        - --current-cluster=managed-cluster
        - --run-mode=do-not-send-reports
        command:
        - /manager
        image: projectsveltos/sveltos-agent-amd64:dev
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
          initialDelaySeconds: 15
          periodSeconds: 20
        name: manager
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8081
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          limits:
            cpu: 500m
            memory: 128Mi
          requests:
            cpu: 10m
            memory: 64Mi
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop:
            - ALL
      - args:
        - --secure-listen-address=0.0.0.0:8443
        - --upstream=http://127.0.0.1:8080/
        - --logtostderr=true
        - --v=0
        image: gcr.io/kubebuilder/kube-rbac-proxy:v0.12.0
        name: kube-rbac-proxy
        ports:
        - containerPort: 8443
          name: https
          protocol: TCP
        resources:
          limits:
            cpu: 500m
            memory: 128Mi
          requests:
            cpu: 5m
            memory: 64Mi
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop:
            - ALL
      securityContext:
        runAsNonRoot: true
      serviceAccountName: sveltos-agent-manager
      terminationGracePeriodSeconds: 10
`)
