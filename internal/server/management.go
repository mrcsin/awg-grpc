// Package server implements the gRPC services of awg-grpc over the vendor tools.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/reconcile"
)

// Management implements awgv1.ManagementServiceServer for the configured interfaces.
type Management struct {
	awgv1.UnimplementedManagementServiceServer

	runner     awg.Runner
	lookup     Lookup
	versions   Versions
	names      []string
	interfaces map[string]*configuredInterface
}

// Versions is what GetStatus reports about the software on the node. GetStatus reads
// ModuleVersionFile on every call, so the value follows the loaded host module.
type Versions struct {
	Wrapper           string
	Tools             string
	ModuleVersionFile string
}

// configuredInterface is one interface from the config directory. lock is a one-slot semaphore
// that serializes the applies on it, so overlapping usher passes never interleave their awg
// calls; a waiting apply gives up when its context ends.
type configuredInterface struct {
	name string
	lock chan struct{}
}

// NewManagement returns the service for the interfaces named in interfaces. GetStatus reports
// them in this order.
func NewManagement(runner awg.Runner, lookup Lookup, interfaces []string, versions Versions) *Management {
	m := &Management{
		runner:     runner,
		lookup:     lookup,
		versions:   versions,
		names:      slices.Clone(interfaces),
		interfaces: make(map[string]*configuredInterface, len(interfaces)),
	}
	for _, name := range interfaces {
		m.interfaces[name] = &configuredInterface{name: name, lock: make(chan struct{}, 1)}
	}
	return m
}

// ApplyPeers makes the kernel peer set of one interface equal to the desired list and returns
// the diff between the kernel before and after the change, or the planned diff for a dry run.
func (m *Management) ApplyPeers(ctx context.Context, req *awgv1.ApplyPeersRequest) (*awgv1.ApplyPeersResponse, error) {
	changes, err := m.applyPeers(ctx, req)
	if err != nil {
		return nil, toStatus(err)
	}
	return &awgv1.ApplyPeersResponse{
		Added:   keysToBytes(changes.Added),
		Removed: keysToBytes(changes.Removed),
		Updated: keysToBytes(changes.Updated),
	}, nil
}

func (m *Management) applyPeers(ctx context.Context, req *awgv1.ApplyPeersRequest) (reconcile.Changes, error) {
	iface, err := m.resolve(req.GetInterfaceName())
	if err != nil {
		return reconcile.Changes{}, err
	}
	addrs, err := m.addresses(iface.name)
	if err != nil {
		return reconcile.Changes{}, err
	}

	select {
	case iface.lock <- struct{}{}:
	case <-ctx.Done():
		return reconcile.Changes{}, fmt.Errorf("waiting for the apply in progress on %s: %w", iface.name, ctx.Err())
	}
	defer func() { <-iface.lock }()

	before, err := m.readDump(ctx, iface.name)
	if err != nil {
		return reconcile.Changes{}, err
	}
	desired, err := reconcile.Desired(req, reconcile.Interface{PublicKey: before.PublicKey, Addresses: addrs})
	if err != nil {
		return reconcile.Changes{}, err
	}
	changes := reconcile.Diff(before.Peers, desired)
	if req.GetDryRun() {
		return changes, nil
	}

	cmd, ok := reconcile.BuildSet(iface.name, changes, desired, before.Peers)
	if !ok {
		return changes, nil
	}
	if _, err := m.runner.Run(ctx, cmd); err != nil {
		return reconcile.Changes{}, fmt.Errorf("applying peers to %s: %w", iface.name, err)
	}
	after, err := m.readDump(ctx, iface.name)
	if err != nil {
		return reconcile.Changes{}, err
	}
	return reconcile.Diff(before.Peers, after.Peers), nil
}

// ListPeers reports the peers the kernel holds for one interface, without preshared keys.
func (m *Management) ListPeers(ctx context.Context, req *awgv1.ListPeersRequest) (*awgv1.ListPeersResponse, error) {
	device, err := m.listPeers(ctx, req.GetInterfaceName())
	if err != nil {
		return nil, toStatus(err)
	}
	peers := make([]*awgv1.PeerStatus, 0, len(device.Peers))
	for _, p := range device.Peers {
		peers = append(peers, peerStatus(p))
	}
	return &awgv1.ListPeersResponse{Peers: peers}, nil
}

func (m *Management) listPeers(ctx context.Context, name string) (awg.Device, error) {
	iface, err := m.resolve(name)
	if err != nil {
		return awg.Device{}, err
	}
	if _, err := m.addresses(iface.name); err != nil {
		return awg.Device{}, err
	}
	return m.readDump(ctx, iface.name)
}

// GetStatus reports the versions and the state of every configured interface.
func (m *Management) GetStatus(ctx context.Context, _ *awgv1.GetStatusRequest) (*awgv1.GetStatusResponse, error) {
	resp, err := m.getStatus(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	return resp, nil
}

func (m *Management) getStatus(ctx context.Context) (*awgv1.GetStatusResponse, error) {
	moduleVersion, err := m.moduleVersion()
	if err != nil {
		return nil, err
	}
	interfaces := make([]*awgv1.InterfaceStatus, 0, len(m.names))
	for _, name := range m.names {
		st, err := m.interfaceStatus(ctx, name)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, st)
	}
	return &awgv1.GetStatusResponse{
		WrapperVersion: m.versions.Wrapper,
		ToolsVersion:   m.versions.Tools,
		ModuleVersion:  moduleVersion,
		Interfaces:     interfaces,
	}, nil
}

// moduleVersion returns the trimmed content of the module version file, or "" when the file
// does not exist because the module is not loaded.
func (m *Management) moduleVersion() (string, error) {
	b, err := os.ReadFile(m.versions.ModuleVersionFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading module version: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// interfaceStatus reports one configured interface; an absent one carries only its name.
func (m *Management) interfaceStatus(ctx context.Context, name string) (*awgv1.InterfaceStatus, error) {
	addrs, present, err := m.lookup(name)
	if err != nil {
		return nil, fmt.Errorf("looking up interface %s: %w", name, err)
	}
	if !present {
		return &awgv1.InterfaceStatus{Name: name}, nil
	}
	device, err := m.readDump(ctx, name)
	if err != nil {
		return nil, err
	}
	params, err := m.readClientParams(ctx, name)
	if err != nil {
		return nil, err
	}
	return &awgv1.InterfaceStatus{
		Name:         name,
		Present:      true,
		PublicKey:    device.PublicKey[:],
		ListenPort:   uint32(device.ListenPort),
		PeerCount:    uint32(len(device.Peers)),
		Addresses:    awg.PrefixStrings(addrs),
		ClientParams: params,
	}, nil
}

func (m *Management) readClientParams(ctx context.Context, name string) ([]*awgv1.ConfigParam, error) {
	showconf, err := m.runner.Run(ctx, awg.Command{Name: "awg", Args: []string{"showconf", name}})
	if err != nil {
		return nil, fmt.Errorf("reading config of %s: %w", name, err)
	}
	params, err := awg.ParseClientParams(showconf)
	if err != nil {
		return nil, fmt.Errorf("parsing config of %s: %w", name, err)
	}
	result := make([]*awgv1.ConfigParam, 0, len(params))
	for _, p := range params {
		result = append(result, &awgv1.ConfigParam{Key: p.Key, Value: p.Value})
	}
	return result, nil
}

// resolve returns the configured interface of a request name. Every later step uses the
// configured name, never the request string.
func (m *Management) resolve(name string) (*configuredInterface, error) {
	iface, ok := m.interfaces[name]
	if !ok {
		return nil, &unknownInterfaceError{name: name}
	}
	return iface, nil
}

func (m *Management) addresses(name string) ([]netip.Prefix, error) {
	addrs, present, err := m.lookup(name)
	if err != nil {
		return nil, fmt.Errorf("looking up interface %s: %w", name, err)
	}
	if !present {
		return nil, &absentInterfaceError{name: name}
	}
	return addrs, nil
}

func (m *Management) readDump(ctx context.Context, name string) (awg.Device, error) {
	out, err := m.runner.Run(ctx, awg.Command{Name: "awg", Args: []string{"show", name, "dump"}})
	if err != nil {
		return awg.Device{}, fmt.Errorf("reading peers of %s: %w", name, err)
	}
	device, err := awg.ParseDump(out)
	if err != nil {
		return awg.Device{}, fmt.Errorf("parsing peers of %s: %w", name, err)
	}
	return device, nil
}

func peerStatus(p awg.Peer) *awgv1.PeerStatus {
	status := &awgv1.PeerStatus{
		PublicKey:  p.PublicKey[:],
		AllowedIps: awg.PrefixStrings(p.AllowedIPs),
		Endpoint:   p.Endpoint,
		RxBytes:    p.RxBytes,
		TxBytes:    p.TxBytes,
	}
	if !p.LastHandshake.IsZero() {
		status.LastHandshake = timestamppb.New(p.LastHandshake)
	}
	return status
}

func keysToBytes(keys []awg.Key) [][]byte {
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		out = append(out, k[:])
	}
	return out
}
