package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/GitHEXual/Kproject/internal/node"
	"github.com/GitHEXual/Kproject/internal/registry"
)

func main() {
	port := flag.Int("port", 4001, "listen port")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := node.NewServerNode(ctx, *port)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	defer srv.Close()

	store := registry.NewStore()
	registry.AttachServerHandler(srv.Host, store)

	fmt.Println("=== Kproject Signaling/Relay Server ===")
	fmt.Printf("Peer ID: %s\n", srv.Host.ID())
	fmt.Println("Listening on:")
	for _, addr := range srv.FullAddrs() {
		fmt.Printf("  %s\n", addr)
	}
	fmt.Println()
	fmt.Println("Server is running. Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
}
