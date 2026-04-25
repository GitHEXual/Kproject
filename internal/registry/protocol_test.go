package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2p "github.com/libp2p/go-libp2p"
	ma "github.com/multiformats/go-multiaddr"
)

func createMockHost(t *testing.T) host.Host {
	h, err := libp2p.New()
	if err != nil {
		t.Fatalf("Failed to create mock host: %v", err)
	}
	return h
}

func TestProtocol_RegisterAndLookupFlow(t *testing.T) {
	store := NewStore()
	mockHost := createMockHost(t)
	defer mockHost.Close()

	clientPrivKey, _, _ := crypto.GenerateEd25519Key(nil)
	clientPID, _ := peer.IDFromPrivateKey(clientPrivKey)
	
	regReq := Request{
		Type:    "REGISTER",
		ShortID: "alice",
		Addrs:   []string{"/ip4/127.0.0.1/tcp/4001"},
	}
	
	addr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/4001")
	store.Register(&PeerRecord{
		ShortID: regReq.ShortID,
		PeerID:  clientPID,
		Addrs:   []ma.Multiaddr{addr},
	})

	rec, ok := store.LookupByShortID("alice")
	if !ok {
		t.Fatal("User not found after registration")
	}
	if rec.PeerID != clientPID {
		t.Error("PeerID mismatch")
	}

	lookupReq := Request{
		Type:    "LOOKUP",
		ShortID: "alice",
	}
	
	rec2, ok := store.LookupByShortID(lookupReq.ShortID)
	if !ok {
		t.Fatal("Lookup failed")
	}
	
	if len(rec2.Addrs) == 0 {
		t.Error("No addresses returned in lookup")
	}
	
	_ = mockHost 
	_ = regReq
	_ = lookupReq
}

func TestProtocol_JsonSerialization(t *testing.T) {
	req := Request{
		Type:    "REGISTER",
		ShortID: "bob",
		Addrs:   []string{"/ip4/1.1.1.1/tcp/80"},
	}
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	
	var decodedReq Request
	err = json.Unmarshal(data, &decodedReq)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	
	if decodedReq.Type != req.Type || decodedReq.ShortID != req.ShortID {
		t.Error("Request serialization mismatch")
	}

	resp := Response{
		OK:      true,
		ShortID: "bob",
		PeerID:  "12D3KooW...",
		Addrs:   []string{"/ip4/1.1.1.1/tcp/80"},
	}
	
	dataResp, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Response marshal failed: %v", err)
	}
	
	if !strings.Contains(string(dataResp), "\"ok\":true") {
		t.Error("Response JSON format unexpected")
	}
}