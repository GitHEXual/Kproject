package node

import (
	"context"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

type Node struct {
	Host    host.Host
	ShortID string
	Ctx     context.Context
	Cancel  context.CancelFunc
}

func shortIDFromPeerID(pid peer.ID) string {
	raw := []byte(pid)
	encoded := base32.StdEncoding.EncodeToString(raw)
	encoded = strings.ToLower(encoded)
	encoded = strings.TrimRight(encoded, "=")
	if len(encoded) > 8 {
		return encoded[len(encoded)-8:]
	}
	return encoded
}

func NewClientNode(ctx context.Context, serverAddr string) (*Node, error) {
	ctx, cancel := context.WithCancel(ctx)

	serverMA, err := ma.NewMultiaddr(serverAddr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("invalid server address: %w", err)
	}

	serverInfo, err := peer.AddrInfoFromP2pAddr(serverMA)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("invalid server peer address: %w", err)
	}

	h, err := libp2p.New(
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip4/0.0.0.0/tcp/0",
		),
		libp2p.NATPortMap(),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{*serverInfo}),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	connectCtx, connectCancel := context.WithTimeout(ctx, 15*time.Second)
	defer connectCancel()

	if err := h.Connect(connectCtx, *serverInfo); err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	return &Node{
		Host:    h,
		ShortID: shortIDFromPeerID(h.ID()),
		Ctx:     ctx,
		Cancel:  cancel,
	}, nil
}

func NewServerNode(ctx context.Context, listenPort int) (*Node, error) {
	ctx, cancel := context.WithCancel(ctx)

	h, err := libp2p.New(
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", listenPort),
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", listenPort),
		),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create server host: %w", err)
	}

	if _, err := relay.New(h); err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("failed to start relay service: %w", err)
	}

	return &Node{
		Host:    h,
		ShortID: shortIDFromPeerID(h.ID()),
		Ctx:     ctx,
		Cancel:  cancel,
	}, nil
}

func (n *Node) Close() {
	n.Cancel()
	n.Host.Close()
}

func (n *Node) Addrs() []ma.Multiaddr {
	return n.Host.Addrs()
}

func (n *Node) FullAddrs() []ma.Multiaddr {
	hostAddr, _ := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s", n.Host.ID()))
	var full []ma.Multiaddr
	for _, addr := range n.Host.Addrs() {
		full = append(full, addr.Encapsulate(hostAddr))
	}
	return full
}
