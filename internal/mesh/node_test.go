package mesh

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestConfigValidateRejectsInvalidPeerAddr(t *testing.T) {
	cfg := Config{
		TargetPeers: []string{"/ip4/127.0.0.1/tcp/4001"},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate to reject addr without peer id")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	original := Envelope{
		Type:   "topology",
		From:   "node-a",
		Name:   "alpha",
		Addrs:  []string{"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWabc"},
		Peers:  []PeerRef{{PeerID: "12D3KooWxyz", Name: "beta"}},
		SentAt: time.Now().UTC().Round(time.Second),
	}
	original.Payload = json.RawMessage(`{"ok":true}`)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if decoded.Type != original.Type || decoded.Name != original.Name || len(decoded.Peers) != 1 {
		t.Fatalf("unexpected decoded envelope: %#v", decoded)
	}
}

func TestTopologyMergeDeduplicates(t *testing.T) {
	topology := NewTopology()
	pidA := mustPeerID(t)
	pidB := mustPeerID(t)

	topology.UpdatePeer(PeerSnapshot{
		PeerID:   pidA.String(),
		Name:     "alpha",
		State:    "connected",
		ConnType: "direct",
		Addrs: []string{
			"/ip4/127.0.0.1/tcp/4001/p2p/" + pidA.String(),
			"/ip4/127.0.0.1/tcp/4001/p2p/" + pidA.String(),
		},
	})
	topology.MergePeerRefs([]PeerRef{{
		PeerID: pidB.String(),
		Name:   "beta",
		Addrs: []string{
			"/ip4/127.0.0.1/tcp/4002/p2p/" + pidB.String(),
			"/ip4/127.0.0.1/tcp/4002/p2p/" + pidB.String(),
		},
	}})

	snapshots := topology.Snapshot()
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 peers in topology, got %d", len(snapshots))
	}
	if len(snapshots[0].Addrs) != 1 || len(snapshots[1].Addrs) != 1 {
		t.Fatalf("expected duplicate addresses to collapse, got %#v", snapshots)
	}
}

func TestDirectHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nodeA, err := NewNode(ctx, Config{
		Name:        "alpha",
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0", "/ip4/127.0.0.1/udp/0/quic-v1"},
	})
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("sandbox does not allow opening sockets: %v", err)
		}
		t.Fatalf("new node A: %v", err)
	}
	defer nodeA.Close()

	nodeB, err := NewNode(ctx, Config{
		Name:        "beta",
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0", "/ip4/127.0.0.1/udp/0/quic-v1"},
	})
	if err != nil {
		t.Fatalf("new node B: %v", err)
	}
	defer nodeB.Close()

	nodeA.targets = nil
	for _, addr := range nodeB.FullAddrs() {
		nodeA.targets = append(nodeA.targets, mustParseAddrInfo(t, addr.String()))
	}

	if err := nodeA.Connect(ctx); err != nil {
		t.Fatalf("connect node A -> B: %v", err)
	}

	waitForPeer(t, nodeA, nodeB.Host.ID().String())
	waitForPeer(t, nodeB, nodeA.Host.ID().String())
}

func waitForPeer(t *testing.T, node *Node, target string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, snapshot := range node.PeerSnapshots() {
			if snapshot.PeerID == target && snapshot.State == "connected" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("peer %s did not become connected", target)
}

func mustParseAddrInfo(t *testing.T, value string) peer.AddrInfo {
	t.Helper()
	info, err := parseAddrInfo(value)
	if err != nil {
		t.Fatalf("parse addr info: %v", err)
	}
	return *info
}

func mustPeerID(t *testing.T) peer.ID {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("peer id from key: %v", err)
	}
	return pid
}
