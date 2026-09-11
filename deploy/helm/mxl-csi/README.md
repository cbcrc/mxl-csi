# mxl-csi Helm chart

This chart deploys the MXL CSI driver in the standard split mode:

- Controller Deployment (with csi-provisioner + livenessprobe)
- Node DaemonSet (with node-driver-registrar + livenessprobe)

## Prerequisites

- Kubernetes cluster with Linux worker nodes
- Privileged pod support for the node plugin
- Image already pushed to a registry reachable by your cluster

The published `ghcr.io/cbcrc/mxl-csi` image is public, so no Kubernetes image pull secret is required for the default install.

## Install

Run the following commands from the repository root.

1. Install the Helm chart:

```bash
helm upgrade --install mxl-csi ./deploy/helm/mxl-csi \
  --namespace kube-system \
  --create-namespace \
  --set image.repository=ghcr.io/cbcrc/mxl-csi \
  --set image.tag=latest
```

The CSI driver now includes merged domain lifecycle behavior (create/mount `/run/mxl/domain` tmpfs and grow on-demand), so the bootstrap DaemonSet is optional unless you want a pre-provisioned host setup.

## Install with custom values

1. Create a custom values file:

```yaml
# deploy/helm/mxl-csi/values-custom.yaml
driver:
  sharedHostPath: /dev/shm/mxl

storageClass:
  name: custom-mxl-domain-sc

controller:
  nodeSelector: {}
  tolerations: []

node:
  nodeSelector: {}
  tolerations: []
```

2. Install the Helm chart with the custom values file:

```bash
helm upgrade --install mxl-csi ./deploy/helm/mxl-csi \
  --namespace kube-system \
  --create-namespace \
  -f ./deploy/helm/mxl-csi/values-custom.yaml \
  --set image.repository=ghcr.io/cbcrc/mxl-csi \
  --set image.tag=latest
```

## Verify

```bash
kubectl -n kube-system get pods -l app.kubernetes.io/instance=mxl-csi
kubectl get csidriver
kubectl get storageclass
```

## Consumer pod requirements

The shared domain directory (`/run/mxl/domain`) is mounted as tmpfs owned by uid/gid `1000:1000` with mode `0775`. Containers running as uid `1000` or as root can read/write it out of the box. Any other container UID needs `gid 1000` added as a supplemental group so it can write to the domain, since the group owner is fixed for the shared, multi-tenant tmpfs (per-volume ownership isn't possible here):

```yaml
spec:
  securityContext:
    supplementalGroups: [1000]
```

This is a pod-level `securityContext` field — it adds `gid 1000` to the container process's supplementary groups regardless of the image's own default user, without requiring privileged access or a matching `runAsUser`.

## Common overrides

```bash
--set driver.name=mxl.csi.cbcrc.ca
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
