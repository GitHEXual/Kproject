package registry

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func generateTestPeerID(t *testing.T) peer.ID {
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		t.Fatalf("Failed to get PeerID from key: %v", err)
	}
	return pid
}

func TestStore_RegisterAndLookup(t *testing.T) {
	store := NewStore()
	pid := generateTestPeerID(t)
	shortID := "testuser1"
	
	addr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/4001")
	addrs := []ma.Multiaddr{addr}

	store.Register(&PeerRecord{
		ShortID: shortID,
		PeerID:  pid,
		Addrs:   addrs,
	})

	if store.Count() != 1 {
		t.Errorf("Expected count 1, got %d", store.Count())
	}

	rec, ok := store.LookupByShortID(shortID)
	if !ok {
		t.Fatal("Failed to lookup user by ShortID")
	}
	if rec.PeerID != pid {
		t.Error("Retrieved PeerID does not match registered one")
	}
	if len(rec.Addrs) != 1 || rec.Addrs[0].String() != addr.String() {
		t.Error("Retrieved addresses do not match")
	}

	rec2, ok := store.LookupByPeerID(pid)
	if !ok {
		t.Fatal("Failed to lookup user by PeerID")
	}
	if rec2.ShortID != shortID {
		t.Error("Retrieved ShortID does not match")
	}
}

func TestStore_UpdateRegistration(t *testing.T) {
	store := NewStore()
	pid := generateTestPeerID(t)
	
	addr1, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/4001")
	addr2, _ := ma.NewMultiaddr("/ip4/192.168.1.1/tcp/4002")

	store.Register(&PeerRecord{
		ShortID: "user_v1",
		PeerID:  pid,
		Addrs:   []ma.Multiaddr{addr1},
	})

	store.Register(&PeerRecord{
		ShortID: "user_v2",
		PeerID:  pid,
		Addrs:   []ma.Multiaddr{addr2},
	})

	if _, ok := store.LookupByShortID("user_v1"); ok {
		t.Error("Old ShortID should have been removed after update")
	}

	rec, ok := store.LookupByShortID("user_v2")
	if !ok {
		t.Fatal("New ShortID not found")
	}
	if len(rec.Addrs) != 1 || rec.Addrs[0].String() != addr2.String() {
		t.Error("Addresses were not updated correctly")
	}
}

func TestStore_Unregister(t *testing.T) {
	store := NewStore()
	pid := generateTestPeerID(t)
	shortID := "todelete"

	store.Register(&PeerRecord{
		ShortID: shortID,
		PeerID:  pid,
		Addrs:   []ma.Multiaddr{},
	})

	if store.Count() != 1 {
		t.Fatal("User not registered")
	}

	store.Unregister(pid)

	if store.Count() != 0 {
		t.Error("User was not unregistered")
	}

	if _, ok := store.LookupByShortID(shortID); ok {
		t.Error("User still found by ShortID after unregister")
	}
	if _, ok := store.LookupByPeerID(pid); ok {
		t.Error("User still found by PeerID after unregister")
	}
}