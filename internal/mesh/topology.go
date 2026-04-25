package mesh

import (
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type PeerRef struct {
	PeerID string   `json:"peer_id"`
	Name   string   `json:"name,omitempty"`
	Addrs  []string `json:"addrs,omitempty"`
}

type PeerSnapshot struct {
	PeerID   string    `json:"peer_id"`
	Name     string    `json:"name,omitempty"`
	State    string    `json:"state"`
	ConnType string    `json:"conn_type"`
	Addrs    []string  `json:"addrs,omitempty"`
	LastSeen time.Time `json:"last_seen"`
}

type Topology struct {
	mu    sync.RWMutex
	peers map[peer.ID]*PeerSnapshot
}

func NewTopology() *Topology {
	return &Topology{peers: make(map[peer.ID]*PeerSnapshot)}
}

func (t *Topology) UpdatePeer(snapshot PeerSnapshot) {
	pid, err := peer.Decode(snapshot.PeerID)
	if err != nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	existing, ok := t.peers[pid]
	if !ok {
		cp := snapshotPeer(snapshot)
		if cp.LastSeen.IsZero() {
			cp.LastSeen = time.Now().UTC()
		}
		t.peers[pid] = &cp
		return
	}

	if snapshot.Name != "" {
		existing.Name = snapshot.Name
	}
	if snapshot.State != "" {
		existing.State = snapshot.State
	}
	if snapshot.ConnType != "" {
		existing.ConnType = snapshot.ConnType
	}
	if len(snapshot.Addrs) > 0 {
		existing.Addrs = dedupeStrings(snapshot.Addrs)
	}
	if snapshot.LastSeen.IsZero() {
		existing.LastSeen = time.Now().UTC()
	} else {
		existing.LastSeen = snapshot.LastSeen.UTC()
	}
}

func (t *Topology) MergePeerRefs(refs []PeerRef) {
	now := time.Now().UTC()
	for _, ref := range refs {
		t.UpdatePeer(PeerSnapshot{
			PeerID:   ref.PeerID,
			Name:     ref.Name,
			State:    "discovered",
			ConnType: "unknown",
			Addrs:    ref.Addrs,
			LastSeen: now,
		})
	}
}

func (t *Topology) MarkDisconnected(pid peer.ID) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if snap, ok := t.peers[pid]; ok {
		snap.State = "disconnected"
		snap.LastSeen = time.Now().UTC()
	}
}

func (t *Topology) Snapshot() []PeerSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]PeerSnapshot, 0, len(t.peers))
	for _, peerState := range t.peers {
		out = append(out, snapshotPeer(*peerState))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].PeerID < out[j].PeerID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *Topology) PeerRefs(exclude peer.ID) []PeerRef {
	t.mu.RLock()
	defer t.mu.RUnlock()

	refs := make([]PeerRef, 0, len(t.peers))
	for pid, peerState := range t.peers {
		if pid == exclude {
			continue
		}
		refs = append(refs, PeerRef{
			PeerID: peerState.PeerID,
			Name:   peerState.Name,
			Addrs:  append([]string(nil), peerState.Addrs...),
		})
	}
	return refs
}

func snapshotPeer(in PeerSnapshot) PeerSnapshot {
	in.Addrs = dedupeStrings(in.Addrs)
	return in
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
