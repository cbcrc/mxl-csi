# mxl-csi Helm chart

This chart deploys the MXL CSI driver in the standard split mode:

- Controller Deployment (with csi-provisioner + livenessprobe)
- Node DaemonSet (with node-driver-registrar + livenessprobe)

## Prerequisites

- Kubernetes cluster with Linux worker nodes
- Privileged pod support for the node plugin
- Image already pushed to a registry reachable by your cluster

## Install

```bash
helm upgrade --install mxl-csi ./charts/mxl-csi \
  --namespace kube-system \
  --create-namespace \
  --set image.repository=ghcr.io/cbcrc-ea/ti-eng-mxl-k8s-csi \
  --set image.tag=0.1.0
```

## Install with production values

```bash
helm upgrade --install mxl-csi ./charts/mxl-csi \
  --namespace kube-system \
  --create-namespace \
  -f ./charts/mxl-csi/values-preprod.yaml \
  --set image.repository=ghcr.io/cbcrc-ea/ti-eng-mxl-k8s-csi \
  --set image.tag=0.1.0
```

## Verify

```bash
kubectl -n kube-system get pods -l app.kubernetes.io/instance=mxl-csi
kubectl get csidriver
kubectl get storageclass
```

## Common overrides

```bash
--set driver.name=mxl.csi.k8s.local
--set storageClass.name=mxl-domain-sc
--set driver.sharedHostPath=/run/mxl/domain
```

## Scheduling controls

You can pin controller and node workloads with dedicated knobs in values:

```yaml
controller:
  nodeSelector: {}
  tolerations: []
  affinity: {}

node:
  nodeSelector: {}
  tolerations: []
  affinity: {}
```

## CI packaging and publishing

This repository includes [../../.github/workflows/helm-chart.yml](../../.github/workflows/helm-chart.yml), which:

- lints the chart
- packages it into a .tgz artifact
- pushes it to GHCR as an OCI Helm chart under ghcr.io/<owner>/charts
