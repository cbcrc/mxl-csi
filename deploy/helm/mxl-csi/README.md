# mxl-csi Helm chart

This chart deploys the MXL CSI driver in the standard split mode:

- Controller Deployment (with csi-provisioner + livenessprobe)
- Node DaemonSet (with node-driver-registrar + livenessprobe)

## Prerequisites

- Kubernetes cluster with Linux worker nodes
- Privileged pod support for the node plugin
- Image already pushed to a registry reachable by your cluster
- A Kubernetes image pull secret named ghcr-creds in the target namespace (kube-system in the examples below) for pulling from ghcr.io/cbcrc-ea

Create the secret before installation:

```bash
kubectl -n kube-system create secret docker-registry ghcr-creds \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-enterprise-token> \
  --docker-email=<email>
```

The GitHub Enterprise token should have permission to pull images from ghcr.io/cbcrc-ea (for example, read:packages).

## Install

Run the following commands from the repository root.

1. Install the Helm chart:

```bash
helm upgrade --install mxl-csi ./deploy/helm/mxl-csi \
  --namespace kube-system \
  --create-namespace \
  --set image.repository=ghcr.io/cbcrc-ea/mxl-k8s-csi \
  --set image.tag=0.2.1
```

The CSI driver now includes merged domain lifecycle behavior (create/mount `/run/mxl/domain` tmpfs and grow on-demand), so the bootstrap DaemonSet is optional unless you want a pre-provisioned host setup.

## Install with pre-prod values

```bash
helm upgrade --install mxl-csi ./deploy/helm/mxl-csi \
  --namespace kube-system \
  --create-namespace \
  -f ./deploy/helm/mxl-csi/values-preprod.yaml \
  --set image.repository=ghcr.io/cbcrc-ea/mxl-k8s-csi \
  --set image.tag=0.2.1
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

This repository includes [../../../.github/workflows/helm-chart.yml](../../../.github/workflows/helm-chart.yml), which:

- lints the chart
- packages it into a .tgz artifact
- pushes it to GHCR as an OCI Helm chart under ghcr.io/<owner>/charts
