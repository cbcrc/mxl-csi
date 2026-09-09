# csi-test: MXL flow validation with CSI dynamic provisioning

This folder is an adaptation of the qvest-mxl-k8s-test scenario that uses mxl-csi for dynamic provisioning of the MXL domain storage used by the workloads.
It is designed to validate both the mxl-csi driver and the Qvest mxl-k8s operator in the same end-to-end media workflow.

Unlike the static PV and PVC manifests in qvest-mxl-k8s-test, the csi-test manifests request storage through CSI (StorageClass mxl-domain-sc) using inline ephemeral volumeClaimTemplate blocks. This allows Kubernetes to dynamically provision the MXL domain volume claims for producer and consumer workloads.

## Prerequisites

- Qvest mxl-k8s operator is installed and healthy in the cluster.
- mxl-csi is installed and its controller and node components are running.
- A CSI StorageClass named mxl-domain-sc exists and points to the mxl CSI driver.
- MXL CRDs (for example MxlReceiver, MxlFlow, MxlFlowMirror) are present.

## What each file is

- mxl-video-flow.yaml
  - ConfigMap containing flow.json metadata for the test flow (1080p29, v210, BT709).
  - Provides the flow ID and media description used by the producer and receiver flow logic.

- media-producer.yaml
  - Producer pod pinned to node mtllppdmh001.
  - Uses a CSI-provisioned ephemeral PVC (StorageClass mxl-domain-sc) mounted at /run/mxl/domain.
  - Runs mxl-gst-testsrc to generate the source MXL flow.

- mxl-receiver.yaml
  - MxlReceiver custom resource that declares interest in the producer flow ID.
  - Selects the consumer workload by label and triggers operator mirror/reconciliation behavior.

- media-consumer.yaml
  - Consumer deployment pinned to node mtllppdmh002.
  - Uses a CSI-provisioned ephemeral PVC (StorageClass mxl-domain-sc) mounted at /run/mxl/domain.
  - Reads the mirrored flow and publishes it to MediaMTX over RTSP.

- mediamtx.yaml
  - MediaMTX deployment plus internal and external services.
  - Receives the consumer-published stream and exposes it through RTSP (NodePort 30554).

## Apply order

Create the resources in this order:

```bash
kubectl apply -f tests/csi-test/mxl-video-flow.yaml
kubectl apply -f tests/csi-test/media-producer.yaml
kubectl apply -f tests/csi-test/mxl-receiver.yaml
kubectl apply -f tests/csi-test/media-consumer.yaml
kubectl apply -f tests/csi-test/mediamtx.yaml
```

## Notes

- This scenario assumes mxl-k8s operator components and mxl-csi are already installed and running.
- The CSI StorageClass name used by these manifests is mxl-domain-sc.
