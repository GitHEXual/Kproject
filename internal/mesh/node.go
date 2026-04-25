package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

type Event struct {
	Kind     string
	PeerID   string
	ConnType string
	Message  string
	Err      error
	At       time.Time
}

type Envelope struct {
	Type    string          `json:"type"`
	From    string          `json:"from"`
	Name    string          `json:"name,omitempty"`
	Addrs   []string        `json:"addrs,omitempty"`
	Peers   []PeerRef       `json:"peers,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	SentAt  time.Time       `json:"sent_at"`
}

type Node struct {
	Host     host.Host
	PeerName string
	Topology *Topology
	Events   <-chan Event

	cfg       Config
	ctx       context.Context
	cancel    context.CancelFunc
	events    chan Event
	notifiee  *network.NotifyBundle
	bootstrap []peer.AddrInfo
	targets   []peer.AddrInfo
	logger    *log.Logger
	closeOnce sync.Once
}

func NewNode(parent context.Context, cfg Config) (*Node, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)

	bootstrap, err := parseAddrInfos(cfg.BootstrapPeers)
	if err != nil {
		cancel()
		return nil, err
	}
	targets, err := parseAddrInfos(cfg.TargetPeers)
	if err != nil {
		cancel()
		return nil, err
	}

	options := []libp2p.Option{
		libp2p.ListenAddrStrings(cfg.ListenAddrs...),
		libp2p.EnableRelay(),
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoNATv2(),
	}
	if len(bootstrap) > 0 {
		options = append(options, libp2p.EnableAutoRelayWithStaticRelays(bootstrap))
	}
	if cfg.ForcePrivate {
		options = append(options, libp2p.ForceReachabilityPrivate())
	}

	h, err := libp2p.New(options...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create mesh host: %w", err)
	}

	node := &Node{
		Host:      h,
		PeerName:  cfg.Name,
		Topology:  NewTopology(),
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		events:    make(chan Event, 128),
		bootstrap: bootstrap,
		targets:   targets,
		logger:    log.Default(),
	}
	node.Events = node.events
	node.installHandlers()
	node.installNotifiee()
	node.registerSelf()
	node.startBackgroundLoops()

	return node, nil
}

func (n *Node) Connect(ctx context.Context) error {
	peers := make([]peer.AddrInfo, 0, len(n.bootstrap)+len(n.targets))
	peers = append(peers, n.bootstrap...)
	peers = append(peers, n.targets...)
	if len(peers) == 0 {
		n.emit(Event{Kind: "idle", Message: "mesh node started without bootstrap peers"})
		return nil
	}

	var firstErr error
	successes := 0
	seen := make(map[peer.ID]struct{}, len(peers))
	for _, info := range peers {
		if info.ID == n.Host.ID() {
			continue
		}
		if _, ok := seen[info.ID]; ok {
			continue
		}
		seen[info.ID] = struct{}{}
		if err := n.connectPeer(ctx, info); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		successes++
	}

	if successes == 0 && firstErr != nil {
		return firstErr
	}
	return nil
}

func (n *Node) Close() error {
	var err error
	n.closeOnce.Do(func() {
		n.cancel()
		if n.notifiee != nil {
			n.Host.Network().StopNotify(n.notifiee)
		}
		err = n.Host.Close()
		close(n.events)
	})
	return err
}

func (n *Node) FullAddrs() []ma.Multiaddr {
	hostAddr, _ := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s", n.Host.ID()))
	full := make([]ma.Multiaddr, 0, len(n.Host.Addrs()))
	for _, addr := range n.Host.Addrs() {
		full = append(full, addr.Encapsulate(hostAddr))
	}
	return full
}

func (n *Node) PeerSnapshots() []PeerSnapshot {
	return n.Topology.Snapshot()
}

func (n *Node) installHandlers() {
	n.Host.SetStreamHandler(handshakeProtocol(n.cfg.Namespace), n.handleHandshakeStream)
	n.Host.SetStreamHandler(topologyProtocol(n.cfg.Namespace), n.handleTopologyStream)
	n.Host.SetStreamHandler(pingProtocol(n.cfg.Namespace), n.handlePingStream)
}

func (n *Node) installNotifiee() {
	n.notifiee = &network.NotifyBundle{
		ConnectedF: func(_ network.Network, conn network.Conn) {
			pid := conn.RemotePeer()
			n.Topology.UpdatePeer(PeerSnapshot{
				PeerID:   pid.String(),
				State:    "connected",
				ConnType: connectionTypeForPeer(n.Host, pid),
				Addrs:    peerstoreAddrStrings(n.Host, pid),
				LastSeen: time.Now().UTC(),
			})
			n.emit(Event{
				Kind:     "connected",
				PeerID:   pid.String(),
				ConnType: connectionTypeForPeer(n.Host, pid),
				Message:  "peer connection established",
			})
		},
		DisconnectedF: func(_ network.Network, conn network.Conn) {
			pid := conn.RemotePeer()
			n.Topology.MarkDisconnected(pid)
			n.emit(Event{
				Kind:     "disconnected",
				PeerID:   pid.String(),
				ConnType: "unknown",
				Message:  "peer connection closed",
			})
		},
	}
	n.Host.Network().Notify(n.notifiee)
}

func (n *Node) registerSelf() {
	n.Topology.UpdatePeer(PeerSnapshot{
		PeerID:   n.Host.ID().String(),
		Name:     n.PeerName,
		State:    "self",
		ConnType: "direct",
		Addrs:    multiaddrsToStrings(n.FullAddrs()),
		LastSeen: time.Now().UTC(),
	})
}

func (n *Node) startBackgroundLoops() {
	go n.pingLoop()
	go n.topologyLoop()
}

func (n *Node) pingLoop() {
	interval := defaultPingInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			for _, snap := range n.Topology.Snapshot() {
				if snap.PeerID == n.Host.ID().String() || snap.State == "disconnected" {
					continue
				}
				pid, err := peer.Decode(snap.PeerID)
				if err != nil {
					continue
				}
				_ = n.sendPing(n.ctx, pid)
			}
		}
	}
}

func (n *Node) topologyLoop() {
	interval := defaultTopologyInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			for _, snap := range n.Topology.Snapshot() {
				if snap.PeerID == n.Host.ID().String() || snap.State == "disconnected" {
					continue
				}
				pid, err := peer.Decode(snap.PeerID)
				if err != nil {
					continue
				}
				_ = n.sendTopology(n.ctx, pid)
			}
		}
	}
}

func (n *Node) connectPeer(ctx context.Context, info peer.AddrInfo) error {
	dialCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	n.Host.Peerstore().AddAddrs(info.ID, info.Addrs, defaultPeerTTL)
	if err := n.Host.Connect(dialCtx, info); err != nil {
		n.emit(Event{
			Kind:    "dial_failed",
			PeerID:  info.ID.String(),
			Message: "failed to connect to peer",
			Err:     err,
		})
		return fmt.Errorf("connect %s: %w", info.ID, err)
	}

	if err := n.performHandshake(ctx, info.ID); err != nil {
		n.emit(Event{
			Kind:    "handshake_failed",
			PeerID:  info.ID.String(),
			Message: "handshake failed",
			Err:     err,
		})
		return err
	}

	_ = n.sendTopology(ctx, info.ID)
	_ = n.sendPing(ctx, info.ID)
	return nil
}

func (n *Node) performHandshake(ctx context.Context, pid peer.ID) error {
	streamCtx, cancel := context.WithTimeout(ctx, defaultHandshakeTimeout)
	defer cancel()

	stream, err := n.Host.NewStream(streamCtx, pid, handshakeProtocol(n.cfg.Namespace))
	if err != nil {
		return fmt.Errorf("open handshake stream: %w", err)
	}
	defer stream.Close()

	if err := writeEnvelope(stream, n.helloEnvelope("hello")); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	resp, err := readEnvelope(stream)
	if err != nil {
		return fmt.Errorf("read hello_ack: %w", err)
	}
	if resp.Type != "hello_ack" {
		return fmt.Errorf("unexpected handshake response: %s", resp.Type)
	}

	n.applyPeerEnvelope(pid, resp, "connected")
	n.emit(Event{
		Kind:     "handshake_ok",
		PeerID:   pid.String(),
		ConnType: connectionTypeForPeer(n.Host, pid),
		Message:  "mesh handshake completed",
	})
	return nil
}

func (n *Node) sendTopology(ctx context.Context, pid peer.ID) error {
	streamCtx, cancel := context.WithTimeout(ctx, defaultHandshakeTimeout)
	defer cancel()

	stream, err := n.Host.NewStream(streamCtx, pid, topologyProtocol(n.cfg.Namespace))
	if err != nil {
		return err
	}
	defer stream.Close()

	envelope := n.helloEnvelope("topology")
	envelope.Peers = n.Topology.PeerRefs(pid)
	return writeEnvelope(stream, envelope)
}

func (n *Node) sendPing(ctx context.Context, pid peer.ID) error {
	streamCtx, cancel := context.WithTimeout(ctx, defaultHandshakeTimeout)
	defer cancel()

	stream, err := n.Host.NewStream(streamCtx, pid, pingProtocol(n.cfg.Namespace))
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := writeEnvelope(stream, n.helloEnvelope("ping")); err != nil {
		return err
	}
	resp, err := readEnvelope(stream)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	if resp.Type == "pong" {
		n.applyPeerEnvelope(pid, resp, "connected")
	}
	return nil
}

func (n *Node) handleHandshakeStream(stream network.Stream) {
	defer stream.Close()

	envelope, err := readEnvelope(stream)
	if err != nil {
		n.emit(Event{Kind: "protocol_error", PeerID: stream.Conn().RemotePeer().String(), Message: "invalid handshake stream", Err: err})
		return
	}
	if envelope.Type != "hello" {
		return
	}

	pid := stream.Conn().RemotePeer()
	n.applyPeerEnvelope(pid, envelope, "connected")

	ack := n.helloEnvelope("hello_ack")
	if err := writeEnvelope(stream, ack); err != nil {
		n.emit(Event{Kind: "protocol_error", PeerID: pid.String(), Message: "failed to write hello_ack", Err: err})
		return
	}
}

func (n *Node) handleTopologyStream(stream network.Stream) {
	defer stream.Close()

	envelope, err := readEnvelope(stream)
	if err != nil {
		n.emit(Event{Kind: "protocol_error", PeerID: stream.Conn().RemotePeer().String(), Message: "invalid topology stream", Err: err})
		return
	}
	if envelope.Type != "topology" {
		return
	}

	pid := stream.Conn().RemotePeer()
	n.applyPeerEnvelope(pid, envelope, "connected")
	n.Topology.MergePeerRefs(envelope.Peers)
	n.emit(Event{
		Kind:    "topology_update",
		PeerID:  pid.String(),
		Message: fmt.Sprintf("received %d peer references", len(envelope.Peers)),
	})
}

func (n *Node) handlePingStream(stream network.Stream) {
	defer stream.Close()

	envelope, err := readEnvelope(stream)
	if err != nil {
		n.emit(Event{Kind: "protocol_error", PeerID: stream.Conn().RemotePeer().String(), Message: "invalid ping stream", Err: err})
		return
	}
	if envelope.Type != "ping" {
		return
	}

	pid := stream.Conn().RemotePeer()
	n.applyPeerEnvelope(pid, envelope, "connected")
	if err := writeEnvelope(stream, n.helloEnvelope("pong")); err != nil {
		n.emit(Event{Kind: "protocol_error", PeerID: pid.String(), Message: "failed to write pong", Err: err})
	}
}

func (n *Node) applyPeerEnvelope(pid peer.ID, envelope Envelope, state string) {
	addrs := envelope.Addrs
	if len(addrs) == 0 {
		addrs = peerstoreAddrStrings(n.Host, pid)
	}

	n.Topology.UpdatePeer(PeerSnapshot{
		PeerID:   pid.String(),
		Name:     firstNonEmpty(envelope.Name, envelope.From),
		State:    state,
		ConnType: connectionTypeForPeer(n.Host, pid),
		Addrs:    addrs,
		LastSeen: time.Now().UTC(),
	})
}

func (n *Node) helloEnvelope(kind string) Envelope {
	return Envelope{
		Type:   kind,
		From:   shortName(n.Host.ID()),
		Name:   n.PeerName,
		Addrs:  multiaddrsToStrings(n.FullAddrs()),
		SentAt: time.Now().UTC(),
	}
}

func (n *Node) emit(event Event) {
	event.At = time.Now().UTC()
	select {
	case n.events <- event:
	default:
		if n.logger != nil {
			n.logger.Printf("mesh event dropped: %s %s", event.Kind, event.PeerID)
		}
	}
}

func handshakeProtocol(namespace string) protocol.ID {
	return protocol.ID("/" + namespace + "/mesh/handshake/1.0.0")
}

func topologyProtocol(namespace string) protocol.ID {
	return protocol.ID("/" + namespace + "/mesh/topology/1.0.0")
}

func pingProtocol(namespace string) protocol.ID {
	return protocol.ID("/" + namespace + "/mesh/ping/1.0.0")
}

func parseAddrInfos(values []string) ([]peer.AddrInfo, error) {
	infos := make([]peer.AddrInfo, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		info, err := parseAddrInfo(value)
		if err != nil {
			return nil, err
		}
		infos = append(infos, *info)
	}
	return infos, nil
}

func parseAddrInfo(value string) (*peer.AddrInfo, error) {
	addr, err := ma.NewMultiaddr(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("invalid multiaddr %q: %w", value, err)
	}
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("multiaddr %q must include /p2p/<peer-id>: %w", value, err)
	}
	return info, nil
}

func readEnvelope(r io.Reader) (Envelope, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return Envelope{}, err
	}

	var envelope Envelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &envelope); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func writeEnvelope(w io.Writer, envelope Envelope) error {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func shortName(pid peer.ID) string {
	id := pid.String()
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func multiaddrsToStrings(addrs []ma.Multiaddr) []string {
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.String())
	}
	return dedupeStrings(out)
}

func peerstoreAddrStrings(h host.Host, pid peer.ID) []string {
	return multiaddrsToStrings(h.Peerstore().Addrs(pid))
}

func connectionTypeForPeer(h host.Host, pid peer.ID) string {
	conns := h.Network().ConnsToPeer(pid)
	if len(conns) == 0 {
		return "unknown"
	}
	for _, conn := range conns {
		if !isRelayAddr(conn.RemoteMultiaddr()) {
			return "direct"
		}
	}
	return "relay"
}

func isRelayAddr(addr ma.Multiaddr) bool {
	for _, protocol := range addr.Protocols() {
		if protocol.Code == ma.P_CIRCUIT {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
