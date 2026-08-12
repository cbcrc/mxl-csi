package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	defaultDriverName    = "mxl.csi.k8s.local"
	defaultDriverVersion = "0.2.4"
	defaultEndpoint      = "unix:///csi/csi.sock"
	defaultSharedPath    = "/run/mxl/domain"

	minTmpfsSizeBytes    int64       = 32 * 1024 * 1024
	defaultDomainOwnerID int         = 1000
	defaultDomainMode    os.FileMode = 0o775
	stateFilename        string      = ".mxl-csi-state.json"
	volumeCtxSizeKey     string      = "requestedBytes"
	volumeCtxCleanupKey  string      = "flowCleanupPolicy"

	cleanupPolicyOnDelete        string = "onDelete"
	cleanupPolicyOnLastUnpublish string = "onLastUnpublish"
)

type driver struct {
	name       string
	version    string
	nodeID     string
	sharedPath string
	statePath  string

	mu sync.Mutex
	csi.UnimplementedIdentityServer
	csi.UnimplementedControllerServer
	csi.UnimplementedNodeServer
}

type managedVolume struct {
	RequestedBytes int64    `json:"requestedBytes"`
	PublishCount   int      `json:"publishCount"`
	Deleting       bool     `json:"deleting"`
	CleanupPolicy  string   `json:"cleanupPolicy,omitempty"`
	BaselineFlows  []string `json:"baselineFlows,omitempty"`
	ManagedFlows   []string `json:"managedFlows,omitempty"`
}

type driverState struct {
	Volumes map[string]*managedVolume `json:"volumes"`
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
		statePath:  filepath.Join(filepath.Dir(*sharedPath), stateFilename),
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

	reqBytes := normalizeCapacity(req.GetCapacityRange().GetRequiredBytes())

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.GetName(),
			CapacityBytes: reqBytes,
			VolumeContext: map[string]string{
				"sharedHostPath":    d.sharedPath,
				volumeCtxSizeKey:    strconv.FormatInt(reqBytes, 10),
				volumeCtxCleanupKey: cleanupPolicyOnLastUnpublish,
			},
		},
	}, nil
}

func (d *driver) DeleteVolume(_ context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volID := strings.TrimSpace(req.GetVolumeId())
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	st, err := d.loadStateLocked()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load state: %v", err)
	}

	v := st.ensureVolume(volID)
	v.Deleting = true

	if v.PublishCount == 0 {
		if err := d.cleanupVolumeFlowsLocked(v); err != nil {
			return nil, status.Errorf(codes.Internal, "cleanup volume flows for %q: %v", volID, err)
		}
		delete(st.Volumes, volID)
	}

	if err := d.saveStateLocked(st); err != nil {
		return nil, status.Errorf(codes.Internal, "save state: %v", err)
	}

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
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{Type: csi.ControllerServiceCapability_RPC_EXPAND_VOLUME},
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

func (d *driver) ControllerExpandVolume(_ context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	volID := strings.TrimSpace(req.GetVolumeId())
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}

	reqBytes := normalizeCapacity(req.GetCapacityRange().GetRequiredBytes())

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         reqBytes,
		NodeExpansionRequired: true,
	}, nil
}

func (d *driver) NodeStageVolume(context.Context, *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *driver) NodeUnstageVolume(context.Context, *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *driver) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	targetPath := strings.TrimSpace(req.GetTargetPath())
	volID := strings.TrimSpace(req.GetVolumeId())
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path is required")
	}
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume_capability is required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

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

	reqBytes := normalizeCapacity(parseRequestedBytes(req.GetVolumeContext()))
	st, err := d.loadStateLocked()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load state: %v", err)
	}
	v := st.ensureVolume(volID)
	v.CleanupPolicy = resolveCleanupPolicy(req.GetVolumeContext(), v.CleanupPolicy)
	if reqBytes > v.RequestedBytes {
		v.RequestedBytes = reqBytes
	}
	if v.RequestedBytes == 0 {
		v.RequestedBytes = minTmpfsSizeBytes
	}
	if v.PublishCount == 0 {
		flows, err := d.listFlowEntriesLocked()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "snapshot baseline flows: %v", err)
		}
		v.BaselineFlows = flows
	}
	v.PublishCount++

	targetBytes := maxInt64(minTmpfsSizeBytes, st.totalRequestedBytes())
	if err := d.ensureSharedTmpfsLocked(targetBytes); err != nil {
		v.PublishCount--
		if v.PublishCount == 0 && !v.Deleting {
			v.BaselineFlows = nil
		}
		_ = d.saveStateLocked(st)
		return nil, status.Errorf(codes.Internal, "ensure tmpfs: %v", err)
	}
	if err := d.saveStateLocked(st); err != nil {
		return nil, status.Errorf(codes.Internal, "save state: %v", err)
	}

	if out, err := exec.Command("mount", "--bind", d.sharedPath, targetPath).CombinedOutput(); err != nil {
		st, stateErr := d.loadStateLocked()
		if stateErr == nil {
			if sv, ok := st.Volumes[volID]; ok {
				sv.PublishCount--
				if sv.PublishCount < 0 {
					sv.PublishCount = 0
				}
				_ = d.saveStateLocked(st)
			}
		}
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
	volID := strings.TrimSpace(req.GetVolumeId())
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target_path is required")
	}
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

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

	st, err := d.loadStateLocked()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load state: %v", err)
	}
	v, ok := st.Volumes[volID]
	if !ok {
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}
	if v.PublishCount > 0 {
		v.PublishCount--
	}
	if v.PublishCount == 0 {
		if len(v.ManagedFlows) == 0 {
			flows, listErr := d.listFlowEntriesLocked()
			if listErr != nil {
				return nil, status.Errorf(codes.Internal, "list flow entries: %v", listErr)
			}
			v.ManagedFlows = setDifference(flows, v.BaselineFlows)
		}
		cleanupNow := v.Deleting || v.effectiveCleanupPolicy() == cleanupPolicyOnLastUnpublish
		if cleanupNow {
			if cleanErr := d.cleanupVolumeFlowsLocked(v); cleanErr != nil {
				return nil, status.Errorf(codes.Internal, "cleanup volume flows for %q: %v", volID, cleanErr)
			}
			delete(st.Volumes, volID)
		}
	}

	if err := d.saveStateLocked(st); err != nil {
		return nil, status.Errorf(codes.Internal, "save state: %v", err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *driver) NodeGetVolumeStats(context.Context, *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeGetVolumeStats is not implemented")
}

func (d *driver) NodeExpandVolume(_ context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	volID := strings.TrimSpace(req.GetVolumeId())
	if volID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id is required")
	}

	reqBytes := normalizeCapacity(req.GetCapacityRange().GetRequiredBytes())

	d.mu.Lock()
	defer d.mu.Unlock()

	st, err := d.loadStateLocked()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load state: %v", err)
	}
	v := st.ensureVolume(volID)
	if reqBytes > v.RequestedBytes {
		v.RequestedBytes = reqBytes
	}

	targetBytes := maxInt64(minTmpfsSizeBytes, st.totalRequestedBytes())
	if err := d.ensureSharedTmpfsLocked(targetBytes); err != nil {
		return nil, status.Errorf(codes.Internal, "ensure tmpfs: %v", err)
	}
	if err := d.saveStateLocked(st); err != nil {
		return nil, status.Errorf(codes.Internal, "save state: %v", err)
	}

	return &csi.NodeExpandVolumeResponse{CapacityBytes: v.RequestedBytes}, nil
}

func (d *driver) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME},
				},
			},
		},
	}, nil
}

func (d *driver) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: d.nodeID}, nil
}

func (d *driver) ensureSharedTmpfsLocked(targetBytes int64) error {
	if targetBytes <= 0 {
		targetBytes = minTmpfsSizeBytes
	}

	hostParentDir := filepath.Dir(d.sharedPath)
	if err := os.MkdirAll(hostParentDir, 0o755); err != nil {
		return fmt.Errorf("create parent dir %q: %w", hostParentDir, err)
	}
	if err := os.MkdirAll(d.sharedPath, defaultDomainMode); err != nil {
		return fmt.Errorf("create shared path %q: %w", d.sharedPath, err)
	}

	// Neither /proc/self/mountinfo (masked by this container's own hostPath
	// bind mount of d.sharedPath) nor the tmpfs filesystem type (since /run is
	// itself commonly tmpfs on systemd hosts, independent of our own mount)
	// reliably indicate whether our sized/owned tmpfs was actually created
	// here. domain_def.json is only ever written by this code, right after a
	// successful mount, so its presence is the one signal we control.
	domainDefPath := filepath.Join(d.sharedPath, "domain_def.json")
	_, statErr := os.Stat(domainDefPath)
	initialized := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat %q: %w", domainDefPath, statErr)
	}

	if !initialized {
		opts := fmt.Sprintf("size=%d,nosuid,nodev,noexec,strictatime,mode=%o,uid=%d,gid=%d", targetBytes, defaultDomainMode, defaultDomainOwnerID, defaultDomainOwnerID)
		if out, err := exec.Command("mount", "-t", "tmpfs", "-o", opts, "tmpfs", d.sharedPath).CombinedOutput(); err != nil {
			return fmt.Errorf("mount tmpfs on %q: %w: %s", d.sharedPath, err, strings.TrimSpace(string(out)))
		}
		_ = os.Chown(d.sharedPath, defaultDomainOwnerID, defaultDomainOwnerID)
		if err := os.Chmod(d.sharedPath, defaultDomainMode); err != nil {
			return fmt.Errorf("chmod shared path %q: %w", d.sharedPath, err)
		}

		domainJSON := fmt.Sprintf(`{"id":"99ef9b5c-98c1-5f98-9def-1d61ee9a4fdb","label":"mxl-domain-%s","description":"MXL CSI dynamic tmpfs domain"}`+"\n", d.nodeID)
		if err := os.WriteFile(domainDefPath, []byte(domainJSON), 0o644); err != nil {
			return fmt.Errorf("write domain_def.json: %w", err)
		}
		_ = os.Chown(domainDefPath, defaultDomainOwnerID, defaultDomainOwnerID)
		return nil
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(d.sharedPath, &stat); err != nil {
		return fmt.Errorf("statfs %q: %w", d.sharedPath, err)
	}
	currentBytes := int64(stat.Blocks) * int64(stat.Bsize)
	if targetBytes <= currentBytes {
		return nil
	}

	remountOpts := fmt.Sprintf("remount,size=%d,nosuid,nodev,noexec,strictatime,mode=%o,uid=%d,gid=%d", targetBytes, defaultDomainMode, defaultDomainOwnerID, defaultDomainOwnerID)
	if out, err := exec.Command("mount", "-o", remountOpts, "tmpfs", d.sharedPath).CombinedOutput(); err != nil {
		return fmt.Errorf("remount tmpfs on %q to %d bytes: %w: %s", d.sharedPath, targetBytes, err, strings.TrimSpace(string(out)))
	}

	return nil
}

func (d *driver) listFlowEntriesLocked() ([]string, error) {
	entries, err := os.ReadDir(d.sharedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	flows := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".mxl-flow") {
			flows = append(flows, name)
		}
	}
	return flows, nil
}

func (d *driver) cleanupVolumeFlowsLocked(v *managedVolume) error {
	for _, name := range uniqueStrings(v.ManagedFlows) {
		if name == "" {
			continue
		}
		path := filepath.Join(d.sharedPath, name)
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (d *driver) loadStateLocked() (*driverState, error) {
	b, err := os.ReadFile(d.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &driverState{Volumes: map[string]*managedVolume{}}, nil
		}
		return nil, err
	}

	st := &driverState{}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	if st.Volumes == nil {
		st.Volumes = map[string]*managedVolume{}
	}
	return st, nil
}

func (d *driver) saveStateLocked(st *driverState) error {
	if st == nil {
		return errors.New("nil state")
	}
	if st.Volumes == nil {
		st.Volumes = map[string]*managedVolume{}
	}
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.statePath, b, 0o644)
}

func (st *driverState) ensureVolume(volID string) *managedVolume {
	if st.Volumes == nil {
		st.Volumes = map[string]*managedVolume{}
	}
	v, ok := st.Volumes[volID]
	if !ok {
		v = &managedVolume{RequestedBytes: minTmpfsSizeBytes, CleanupPolicy: cleanupPolicyOnLastUnpublish}
		st.Volumes[volID] = v
	}
	if v.CleanupPolicy == "" {
		v.CleanupPolicy = cleanupPolicyOnLastUnpublish
	}
	return v
}

func (v *managedVolume) effectiveCleanupPolicy() string {
	if v == nil || strings.TrimSpace(v.CleanupPolicy) == "" {
		return cleanupPolicyOnLastUnpublish
	}
	return v.CleanupPolicy
}

func (st *driverState) totalRequestedBytes() int64 {
	if st == nil {
		return 0
	}
	var total int64
	for _, v := range st.Volumes {
		if v == nil || v.PublishCount <= 0 {
			continue
		}
		req := normalizeCapacity(v.RequestedBytes)
		total += req
	}
	return total
}

func parseRequestedBytes(ctx map[string]string) int64 {
	if ctx == nil {
		return 0
	}
	raw := strings.TrimSpace(ctx[volumeCtxSizeKey])
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func resolveCleanupPolicy(ctx map[string]string, fallback string) string {
	if ctx != nil {
		raw := strings.TrimSpace(ctx[volumeCtxCleanupKey])
		switch raw {
		case cleanupPolicyOnDelete, cleanupPolicyOnLastUnpublish:
			return raw
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return cleanupPolicyOnLastUnpublish
}

func normalizeCapacity(v int64) int64 {
	if v <= 0 {
		return minTmpfsSizeBytes
	}
	if v < minTmpfsSizeBytes {
		return minTmpfsSizeBytes
	}
	return v
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func setDifference(current, baseline []string) []string {
	baseSet := make(map[string]struct{}, len(baseline))
	for _, b := range baseline {
		if b != "" {
			baseSet[b] = struct{}{}
		}
	}
	out := make([]string, 0, len(current))
	for _, c := range current {
		if c == "" {
			continue
		}
		if _, ok := baseSet[c]; !ok {
			out = append(out, c)
		}
	}
	return uniqueStrings(out)
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
