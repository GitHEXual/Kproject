package node

import (
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestShortIDFromPeerID(t *testing.T) {
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		t.Fatalf("Failed to get PeerID from key: %v", err)
	}

	short := shortIDFromPeerID(pid)

	if len(short) > 8 {
		t.Errorf("ShortID too long: %s (len=%d)", short, len(short))
	}

	if strings.Contains(short, "=") {
		t.Errorf("ShortID contains padding characters: %s", short)
	}
	
	t.Logf("Generated ShortID: %s from PeerID: %s", short, pid.String()[:20]+"...")
}