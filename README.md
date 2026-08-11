# ti-eng-mxl-k8s-csi
CSI definition for dynamic binding of mxl tmpfs to k8s PVCs

## Overview

This Container Storage Interface (CSI) driver enables Kubernetes pods to access mxl tmpfs ring buffers through standard Kubernetes PVC workflows. The goal is to abstract low-level host mount operations so application teams can consume mxl shared memory by referencing a StorageClass, without manually wiring hostPath mounts into each workload.

### Problem Solved

- **Before**: Applications needing access to mxl ring buffers required manual host-level setup, privileged container access, or volume mount configuration outside the Kubernetes API.
- **After**: Any pod can request access declaratively via PVC + StorageClass, and the CSI driver bind-mounts the shared mxl tmpfs path into the pod volume target.

### Important Behavior

This driver is **not a traditional dynamic provisioner** that allocates isolated per-PVC storage. Instead, it provides a **dynamic mount** of a shared host directory (default `/run/mxl/domain`) into pod volume target paths.

- PVC events are used as the Kubernetes control-plane trigger.
- The mounted data source remains a shared host tmpfs location.
- Per-volume backend allocation is intentionally bypassed.

### How It Works

1. **StorageClass**: Administrators create a StorageClass that references this mxl CSI driver.
2. **PersistentVolumeClaim**: Pods request storage by creating a PVC that uses the mxl StorageClass.
3. **Controller Intercept**: The controller service satisfies CSI create/delete volume calls so Kubernetes can complete its PVC lifecycle, but does not carve dedicated backend storage.
4. **Node Bind Mount**: On node publish, the driver bind-mounts the shared host path into the pod's requested target path.
5. **Pod Access**: The pod accesses the mount as a normal volume while the backing data remains the shared mxl tmpfs ring buffer.

### CSI Implementation Notes

- **Driver identity**: Defaults to name `mxl.csi.k8s.local`, version `0.1.0`.
- **Shared host path**: Defaults to `/run/mxl/domain`, configurable via `--shared-host-path` (or `MXL_SHARED_HOST_PATH`).
- **Identity service**: Advertises controller service capability.
- **Controller service**: Advertises `CREATE_DELETE_VOLUME` capability to integrate with PVC workflows.
- **Create/Delete behavior**: Validates requests and returns success metadata, but does not provision isolated storage units.
- **Node service**: Uses `NodePublishVolume` and `NodeUnpublishVolume` to perform bind mount and unmount operations.
- **Unsupported operations**: Snapshots, expansion, and several optional controller/node operations return `Unimplemented` by design to keep the driver focused on mount abstraction.

### Key Features

- **Dynamic Mount Binding**: Mounts are handled on-demand per pod/PVC request path.
- **Kubernetes-Native**: Uses standard CSI and Kubernetes volume APIs.
- **Abstraction**: Encapsulates mxl ring buffer host-mount complexity behind familiar storage interfaces.
- **Lightweight Behavior**: Minimal CSI surface, focused on mapping pods to shared mxl tmpfs.
- **Multi-Platform**: Supports Kubernetes and OpenShift deployments.

## Repository Structure

```text
ti-eng-mxl-k8s-csi/
├── cmd/
│   └── mxl-csi/                     # main entrypoint
├── build/
│   └── docker/                      # runtime image Dockerfiles
├── deploy/
│   ├── bootstrap/                   # host MXL domain lifecycle manifest
│   ├── helm/
│   │   └── mxl-csi/                 # Helm chart for CSI deployment
│   └── patches/
│       └── mxl-k8s-rc.20-patches/   # operator rc.20 value overrides and patches
├── tests/
│   ├── csi-test/                    # CSI dynamic provisioning scenario
│   └── qvest-mxl-k8s-test/          # static PV/PVC baseline scenario
├── go.mod
├── go.sum
└── README.md
```

## Installation

Install mxl-csi using the Helm chart in this repository.

For full prerequisites and installation commands, see:

- [deploy/helm/mxl-csi/README.md](deploy/helm/mxl-csi/README.md)

## Test Scenarios

This repository contains two end-to-end test folders that validate the same MXL media flow behavior with different storage approaches.
Together, these scenarios are designed to validate both the mxl-k8s-csi driver behavior and the Qvest mxl-k8s operator flow/mirror reconciliation behavior.

### Scenario 1: Static PV/PVC Wiring (qvest-mxl-k8s-test)

Use [tests/qvest-mxl-k8s-test/README.md](tests/qvest-mxl-k8s-test/README.md) when you want to validate cross-node MXL flow behavior with statically declared local PVs and PVCs.

- Focus: baseline operator and media-flow behavior across nodes.
- Storage model: static local PV/PVC manifests mapped to host path /run/mxl/domain.
- Includes: full manifest explanations, interconnect flow, apply order, and verification steps.

### Scenario 2: CSI Dynamic Provisioning (csi-test)

Use [tests/csi-test/README.md](tests/csi-test/README.md) when you want to validate the same media workflow using this CSI driver for dynamic provisioning and mount binding.

- Focus: replacing static PV/PVC wiring with CSI-backed dynamic claims.
- Storage model: inline ephemeral volumeClaimTemplate requests using StorageClass mxl-domain-sc.
- Includes: file-by-file explanation and ordered apply sequence for flow, producer, receiver, consumer, and MediaMTX manifests.

### Which One To Run

- Start with [tests/qvest-mxl-k8s-test/README.md](tests/qvest-mxl-k8s-test/README.md) for baseline functional validation of the MXL operator flow/mirror path.
- Run [tests/csi-test/README.md](tests/csi-test/README.md) to validate CSI-based dynamic provisioning behavior in the same test pattern.

## Build Images

Prebuilt multi-architecture images for `linux/amd64` and `linux/arm64` are already available in GHCR:

- [`ghcr.io/cbcrc-ea/ti-eng-mxl-k8s-csi`](https://github.com/orgs/cbcrc-ea/packages/container/package/ti-eng-mxl-k8s-csi)

If you wish to build images locally instead, use the steps below.

This repository includes two Dockerfiles:

- `build/docker/Dockerfile`: default runtime image based on Alpine.
- `build/docker/Dockerfile.ubi`: OpenShift-friendly runtime image based on Red Hat UBI minimal.

Build with Docker:

```bash
# Alpine-based image (default Dockerfile)
docker build -f build/docker/Dockerfile -t ti-eng-mxl-k8s-csi:alpine .

# UBI-based image (explicit Dockerfile)
docker build -f build/docker/Dockerfile.ubi -t ti-eng-mxl-k8s-csi:ubi .
```

Build with Podman:

```bash
# Alpine-based image
podman build -f build/docker/Dockerfile -t ti-eng-mxl-k8s-csi:alpine .

# UBI-based image
podman build -f build/docker/Dockerfile.ubi -t ti-eng-mxl-k8s-csi:ubi .
```

## OpenShift Tag and Push Example

Replace `quay.io/<org>` with your registry namespace:

```bash
# Example with Docker
docker tag ti-eng-mxl-k8s-csi:ubi quay.io/<org>/ti-eng-mxl-k8s-csi:ubi
docker push quay.io/<org>/ti-eng-mxl-k8s-csi:ubi

# Example with Podman
podman tag ti-eng-mxl-k8s-csi:ubi quay.io/<org>/ti-eng-mxl-k8s-csi:ubi
podman push quay.io/<org>/ti-eng-mxl-k8s-csi:ubi
```
