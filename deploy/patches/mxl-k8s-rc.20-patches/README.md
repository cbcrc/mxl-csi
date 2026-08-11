# mxl-k8s rc.20 patches

This directory contains the install-time Helm values and post-install Kubernetes manifests used with `mxl-k8s` `v1.0.0-rc.20`.

## Files

- `mxl-k8s-values.yaml`
- `mxl-k8s-operator-rbac.yaml`
- `agent-patch.yaml`
- `gateway-patch.yaml`

## Helm install (rc.20)

Use the following command to install `rc.20` of the Qvest `mxl-k8s` operator:

```bash
helm upgrade --install mxl oci://ghcr.io/qvest-digital/mxl-k8s/charts/mxl-k8s \
  --version 1.0.0-rc.20 \
  --namespace mxl-system \
  -f deploy/patches/mxl-k8s-rc.20-patches/mxl-k8s-values.yaml
```

## Values override used at install time

The `mxl-k8s-values.yaml` file was used during Helm install to:

- Override gateway health and metrics bind ports to `:9081` and `:9082`, because the default ports `8080` and `8081` are already in use by the PTP service.
- Set gateway metrics service port to `9082`.
- Deploy `3` replicas of the operator.
- Enable leader election for multi-replica operator deployment.

## Operator leader-election RBAC

The `mxl-k8s-operator-rbac.yaml` manifest provides the `Role` and `RoleBinding` required by operator leader election when running multiple replicas.

Apply it with:

```bash
kubectl apply -f deploy/patches/mxl-k8s-rc.20-patches/mxl-k8s-operator-rbac.yaml
```

## Gateway and agent patches

The `gateway-patch.yaml` and `agent-patch.yaml` manifests contain the gateway and agent modifications used to force traffic/provider use of the `black01` interface.

Apply both patches with `kubectl apply -f`:

```bash
kubectl apply -f deploy/patches/mxl-k8s-rc.20-patches/gateway-patch.yaml
kubectl apply -f deploy/patches/mxl-k8s-rc.20-patches/agent-patch.yaml
```
