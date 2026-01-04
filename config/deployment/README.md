Deployment example

This folder mirrors the existing example manifests and groups them into a single deployment set.

Files
- browser-controller.yaml: controller Deployment
- browser-config.yaml: BrowserConfig example
- usergroup_configmap.yaml: user/group configmap for browser pods
- browser-service-rbac.yaml: ServiceAccount + RBAC for browser-service
- browser-service.yaml: browser-service Deployment + Service
- selenosis.yaml: selenosis ServiceAccount + Deployment + Service

NodePort note
- Services keep type NodePort, but the nodePort field is omitted so Kubernetes assigns one automatically.
- If you need a fixed nodePort, add it under the Service port (nodePort: <port>).
- If you do not need NodePort, change Service type to ClusterIP.

Apply
Make sure the CRDs in ../../crd are installed before applying BrowserConfig.

kubectl apply -f .
