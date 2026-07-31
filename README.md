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

## k3s Test Quick Start

For the end-to-end cross-node MXL flow sharing validation scenario (producer -> operator flow/mirror reconciliation -> consumer -> MediaMTX -> VLC), see:

- [k3s-test/README.md](k3s-test/README.md)

That guide includes:

- Manifest-by-manifest explanation
- Runtime interconnection flow
- Suggested apply order
- Verification commands
- VLC playback settings (RTSP over TCP)

## Build Images

This repository includes two Dockerfiles:

- `Dockerfile`: default runtime image based on Alpine.
- `Dockerfile.ubi`: OpenShift-friendly runtime image based on Red Hat UBI minimal.

Build with Docker:

```bash
# Alpine-based image (default Dockerfile)
docker build -t ti-eng-mxl-k8s-csi:alpine .

# UBI-based image (explicit Dockerfile)
docker build -f Dockerfile.ubi -t ti-eng-mxl-k8s-csi:ubi .
```

Build with Podman:

```bash
# Alpine-based image
podman build -t ti-eng-mxl-k8s-csi:alpine .

# UBI-based image
podman build -f Dockerfile.ubi -t ti-eng-mxl-k8s-csi:ubi .
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
