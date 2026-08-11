package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	defaultDriverName    = "mxl.csi.k8s.local"
	defaultDriverVersion = "0.1.0"
	defaultEndpoint      = "unix:///csi/csi.sock"
	defaultSharedPath    = "/run/mxl/domain"
)

type driver struct {
	name       string
	version    string
	nodeID     string
	sharedPath string
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer
	csi.UnimplementedNodeServer
}

func main() {
	var (
		driverName = flag.String("driver-name", defaultDriverName, "CSI driver name")
		driverVer  = flag.String("driver-version", defaultDriverVersion, "CSI driver version")
		endpoint   = flag.String("endpoint", defaultEndpoint, "CSI endpoint, e.g. unix:///csi/csi.sock")
		nodeID     = flag.String("node-id", "", "Kubernetes node ID for NodeGetInfo")
		sharedPath = flag.String("shared-host-path", envOrDefault("MXL_SHARED_HOST_PATH", defaultSharedPath), "Host path shared by all PVCs")
	)
	flag.Parse()

	if *nodeID == "" {
		log.Fatal("--node-id is required")
	}

	d := &driver{
		name:       *driverName,
		version:    *driverVer,
		nodeID:     *nodeID,
		sharedPath: *sharedPath,
	}

	if err := d.serve(*endpoint); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func (d *driver) serve(endpoint string) error {
	network, addr, err := parseEndpoint(endpoint)
	if err != nil {
		return err
	}

	if network != "unix" {
		return fmt.Errorf("unsupported endpoint network %q; only unix is supported", network)
	}

	if err := os.MkdirAll(filepath.Dir(addr), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	if err := os.Remove(addr); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}

	lis, err := net.Listen(network, addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", endpoint, err)
	}
	defer lis.Close()

	grpcServer := grpc.NewServer()
	csi.RegisterIdentityServer(grpcServer, d)
	csi.RegisterControllerServer(grpcServer, d)
	csi.RegisterNodeServer(grpcServer, d)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("starting CSI driver %s (%s), endpoint=%s sharedHostPath=%s", d.name, d.version, endpoint, d.sharedPath)
		errCh <- grpcServer.Serve(lis)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received signal %s, shutting down", sig)
		grpcServer.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}

func parseEndpoint(endpoint string) (string, string, error) {
	parts := strings.SplitN(endpoint, "://", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid endpoint %q, expected <network>://<address>", endpoint)
	}
	return parts[0], parts[1], nil
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (d *driver) GetPluginInfo(context.Context, *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          d.name,
		VendorVersion: d.version,
	}, nil
}

func (d *driver) GetPluginCapabilities(context.Context, *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{Type: csi.PluginCapability_Service_CONTROLLER_SERVICE},
				},
			},
		},
	}, nil
}

func (d *driver) Probe(context.Context, *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: &wrapperspb.BoolValue{Value: true}}, nil
}

func (d *driver) CreateVolume(_ context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.GetName(),
			CapacityBytes: 0,
			VolumeContext: map[string]string{
				"sharedHostPath": d.sharedPath,
			},
		},
	}, nil
}

func (d *driver) DeleteVolume(context.Context, *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *driver) ControllerPublishVolume(context.Context, *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControllerPublishVolume is not required")
}

func (d *driver) ControllerUnpublishVolume(context.Context, *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControllerUnpublishVolume is not required")
}

func (d *driver) ValidateVolumeCapabilities(_ context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

func (d *driver) ListVolumes(context.Context, *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListVolumes is not implemented")
}

func (d *driver) GetCapacity(context.Context, *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return &csi.GetCapacityResponse{AvailableCapacity: 0}, nil
}

func (d *driver) ControllerGetCapabilities(context.Context, *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME},
				},
			},
		},
	}, nil
}

func (d *driver) CreateSnapshot(context.Context, *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "snapshots are not supported")
}

func (d *driver) DeleteSnapshot(context.Context, *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "snapshots are not supported")
}

func (d *driver) ListSnapshots(context.Context, *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "snapshots are not supported")
}

func (d *driver) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "expansion is not supported")
}

func (d *driver) NodeStageVolume(context.Context, *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *driver) NodeUnstageVolume(context.Context, *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *driver) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	targetPath := strings.TrimSpace(req.GetTargetPath())
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume_capability is required")
	}

	if err := os.MkdirAll(d.sharedPath, 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create shared host path %q: %v", d.sharedPath, err)
	}
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create target path %q: %v", targetPath, err)
	}

	mounted, err := isMountPoint(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check mountpoint %q: %v", targetPath, err)
	}
	if mounted {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if out, err := exec.Command("mount", "--bind", d.sharedPath, targetPath).CombinedOutput(); err != nil {
		return nil, status.Errorf(codes.Internal, "bind mount %q -> %q failed: %v: %s", d.sharedPath, targetPath, err, strings.TrimSpace(string(out)))
	}

	if req.GetReadonly() {
		if out, err := exec.Command("mount", "-o", "remount,bind,ro", targetPath).CombinedOutput(); err != nil {
			return nil, status.Errorf(codes.Internal, "remount read-only %q failed: %v: %s", targetPath, err, strings.TrimSpace(string(out)))
		}
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *driver) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := strings.TrimSpace(req.GetTargetPath())
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path is required")
	}

	mounted, err := isMountPoint(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check mountpoint %q: %v", targetPath, err)
	}
	if mounted {
		if out, err := exec.Command("umount", targetPath).CombinedOutput(); err != nil {
			return nil, status.Errorf(codes.Internal, "umount %q failed: %v: %s", targetPath, err, strings.TrimSpace(string(out)))
		}
	}

	if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, status.Errorf(codes.Internal, "remove target path %q: %v", targetPath, err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *driver) NodeGetVolumeStats(context.Context, *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeGetVolumeStats is not implemented")
}

func (d *driver) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeExpandVolume is not implemented")
}

func (d *driver) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{},
	}, nil
}

func (d *driver) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: d.nodeID}, nil
}

func isMountPoint(target string) (bool, error) {
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	needle := strings.TrimSpace(target)
	if needle == "" {
		return false, nil
	}

	for _, line := range strings.Split(string(mountInfo), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, " ")
		if len(parts) < 5 {
			continue
		}
		if decodeMountInfoPath(parts[4]) == needle {
			return true, nil
		}
	}

	return false, nil
}

func decodeMountInfoPath(s string) string {
	s = strings.ReplaceAll(s, "\\040", " ")
	s = strings.ReplaceAll(s, "\\011", "\t")
	s = strings.ReplaceAll(s, "\\012", "\n")
	s = strings.ReplaceAll(s, "\\134", "\\")
	return s
}
