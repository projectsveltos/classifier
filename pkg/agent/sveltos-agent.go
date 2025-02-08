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
  - ""
  resources:
  - configmaps
  verbs:
  - create
  - get
  - list
  - update
  - watch
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
  - eventreports/status
  - healthcheckreports/status
  - reloaderreports/status
  verbs:
  - get
  - patch
  - update
- apiGroups:
  - lib.projectsveltos.io
  resources:
  - classifiers
  - eventsources
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
  - classifiers/finalizers
  - eventreports/finalizers
  - eventsources/finalizers
  - healthcheckreports/finalizers
  - healthchecks/finalizers
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
  - healthcheckreports
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
        - --diagnostics-address=:8443
        - --v=5
        - --cluster-namespace=
        - --cluster-name=
        - --cluster-type=
        - --version=main
        - --current-cluster=managed-cluster
        - --run-mode=do-not-send-reports
        command:
        - /manager
        image: docker.io/projectsveltos/sveltos-agent@sha256:b6df4552f028ca1159b3ca81f475518d5bb1cd432100feace598e7910e5c4fc1
        livenessProbe:
          failureThreshold: 3
          httpGet:
            path: /healthz
            port: healthz
            scheme: HTTP
          initialDelaySeconds: 15
          periodSeconds: 20
        name: manager
        ports:
        - containerPort: 8443
          name: metrics
          protocol: TCP
        - containerPort: 9440
          name: healthz
          protocol: TCP
        readinessProbe:
          failureThreshold: 3
          httpGet:
            path: /readyz
            port: healthz
            scheme: HTTP
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
          requests:
            cpu: 10m
            memory: 128Mi
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
