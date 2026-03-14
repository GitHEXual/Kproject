package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GitHEXual/Kproject/internal/chat"
	"github.com/GitHEXual/Kproject/internal/node"
	"github.com/GitHEXual/Kproject/internal/registry"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestFullChatFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start server
	srv, err := node.NewServerNode(ctx, 0)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer srv.Close()

	store := registry.NewStore()
	registry.AttachServerHandler(srv.Host, store)

	serverAddrs := srv.FullAddrs()
	if len(serverAddrs) == 0 {
		t.Fatal("Server has no addresses")
	}
	serverAddr := serverAddrs[0].String()
	t.Logf("Server running at: %s", serverAddr)

	// Start client A
	clientA, err := node.NewClientNode(ctx, serverAddr)
	if err != nil {
		t.Fatalf("Failed to create client A: %v", err)
	}
	defer clientA.Close()
	t.Logf("Client A: ShortID=%s, PeerID=%s", clientA.ShortID, clientA.Host.ID())

	// Start client B
	clientB, err := node.NewClientNode(ctx, serverAddr)
	if err != nil {
		t.Fatalf("Failed to create client B: %v", err)
	}
	defer clientB.Close()
	t.Logf("Client B: ShortID=%s, PeerID=%s", clientB.ShortID, clientB.Host.ID())

	// Register both
	serverPeerID := srv.Host.ID()
	regA := registry.NewClient(clientA.Host, serverPeerID)
	regB := registry.NewClient(clientB.Host, serverPeerID)

	if err := regA.Register(ctx, clientA.ShortID, clientA.Addrs()); err != nil {
		t.Fatalf("Failed to register A: %v", err)
	}
	if err := regB.Register(ctx, clientB.ShortID, clientB.Addrs()); err != nil {
		t.Fatalf("Failed to register B: %v", err)
	}

	if store.Count() != 2 {
		t.Fatalf("Expected 2 registered peers, got %d", store.Count())
	}

	// Lookup B from A
	infoB, err := regA.Lookup(ctx, clientB.ShortID)
	if err != nil {
		t.Fatalf("Failed to lookup B: %v", err)
	}
	if infoB.ID != clientB.Host.ID() {
		t.Fatalf("Lookup returned wrong peer ID")
	}
	t.Logf("A looked up B: found %d addresses", len(infoB.Addrs))

	// Create chat services
	msgFromA := make(chan chat.Message, 10)
	msgFromB := make(chan chat.Message, 10)

	chatA := chat.NewService(clientA.Host, clientA.ShortID)
	defer chatA.Close()
	chatA.OnIncomingRequest = func(peerID peer.ID) bool {
		t.Logf("A received incoming request from %s — auto-accepting", peerID)
		return true
	}
	chatA.OnMessage = func(msg chat.Message) {
		msgFromB <- msg
	}

	chatB := chat.NewService(clientB.Host, clientB.ShortID)
	defer chatB.Close()
	chatB.OnIncomingRequest = func(peerID peer.ID) bool {
		t.Logf("B received incoming request from %s — auto-accepting", peerID)
		return true
	}
	chatB.OnMessage = func(msg chat.Message) {
		msgFromA <- msg
	}

	// A connects to B
	statusCh := make(chan chat.ConnectStatus, 10)
	go func() {
		for s := range statusCh {
			t.Logf("Connect status: step=%d msg=%s done=%v err=%v", s.Step, s.Message, s.Done, s.Error)
		}
	}()

	if err := chatA.ConnectToPeer(ctx, infoB, statusCh); err != nil {
		t.Fatalf("A failed to connect to B: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// A sends to B
	if err := chatA.SendMessage(clientB.Host.ID(), "Hello from A!"); err != nil {
		t.Fatalf("A failed to send: %v", err)
	}

	select {
	case msg := <-msgFromA:
		if msg.Content != "Hello from A!" {
			t.Fatalf("B received wrong message: %s", msg.Content)
		}
		if msg.From != clientA.ShortID {
			t.Fatalf("B received wrong sender: %s", msg.From)
		}
		t.Logf("B received: [%s] %s", msg.From, msg.Content)
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for message from A to B")
	}

	// B sends to A (B should have a stream from incoming)
	time.Sleep(500 * time.Millisecond)
	if !chatB.HasStream(clientA.Host.ID()) {
		t.Log("B has no stream to A, opening one...")
		infoA, err := regB.Lookup(ctx, clientA.ShortID)
		if err != nil {
			t.Fatalf("Failed to lookup A: %v", err)
		}
		statusCh2 := make(chan chat.ConnectStatus, 10)
		go func() { for range statusCh2 {} }()
		if err := chatB.ConnectToPeer(ctx, infoA, statusCh2); err != nil {
			t.Fatalf("B failed to connect to A: %v", err)
		}
	}

	if err := chatB.SendMessage(clientA.Host.ID(), "Hello from B!"); err != nil {
		t.Fatalf("B failed to send: %v", err)
	}

	select {
	case msg := <-msgFromB:
		if msg.Content != "Hello from B!" {
			t.Fatalf("A received wrong message: %s", msg.Content)
		}
		t.Logf("A received: [%s] %s", msg.From, msg.Content)
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for message from B to A")
	}

	fmt.Println("=== All tests passed! ===")
}
