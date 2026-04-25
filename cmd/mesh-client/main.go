package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/GitHEXual/Kproject/internal/mesh"
)

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	*m = append(*m, value)
	return nil
}

func main() {
	var listenAddrs multiFlag
	var peers multiFlag
	var bootstrap multiFlag
	var name string
	var namespace string
	var forcePrivate bool

	flag.Var(&listenAddrs, "listen", "listen multiaddr, repeatable")
	flag.Var(&peers, "peer", "target peer multiaddr, repeatable")
	flag.Var(&bootstrap, "bootstrap", "bootstrap peer multiaddr, repeatable")
	flag.StringVar(&name, "name", hostLabel(), "human-readable mesh node name")
	flag.StringVar(&namespace, "namespace", "kproject-mesh", "mesh protocol namespace")
	flag.BoolVar(&forcePrivate, "force-private", false, "force private reachability mode")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	node, err := mesh.NewNode(ctx, mesh.Config{
		ListenAddrs:    listenAddrs,
		BootstrapPeers: bootstrap,
		TargetPeers:    peers,
		Name:           name,
		Namespace:      namespace,
		ForcePrivate:   forcePrivate,
	})
	if err != nil {
		log.Fatalf("mesh init failed: %v", err)
	}
	defer node.Close()

	fmt.Println("=== Kproject Mesh Client ===")
	fmt.Printf("Name: %s\n", node.PeerName)
	fmt.Printf("Peer ID: %s\n", node.Host.ID())
	fmt.Println("Listening on:")
	for _, addr := range node.FullAddrs() {
		fmt.Printf("  %s\n", addr)
	}
	fmt.Println()

	go printEvents(node.Events)

	if err := node.Connect(ctx); err != nil {
		log.Printf("initial mesh connect finished with errors: %v", err)
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopping mesh client...")
			return
		case <-ticker.C:
			printTopology(node)
		}
	}
}

func printEvents(events <-chan mesh.Event) {
	for event := range events {
		prefix := "[" + event.At.Format(time.RFC3339) + "]"
		if event.PeerID != "" {
			prefix += " " + event.PeerID
		}
		if event.Err != nil {
			fmt.Printf("%s %s: %s (%v)\n", prefix, event.Kind, event.Message, event.Err)
			continue
		}
		fmt.Printf("%s %s: %s", prefix, event.Kind, event.Message)
		if event.ConnType != "" {
			fmt.Printf(" [%s]", event.ConnType)
		}
		fmt.Println()
	}
}

func printTopology(node *mesh.Node) {
	fmt.Println("Topology snapshot:")
	for _, peer := range node.PeerSnapshots() {
		fmt.Printf("  %s name=%s state=%s conn=%s addrs=%d\n",
			peer.PeerID,
			peer.Name,
			peer.State,
			peer.ConnType,
			len(peer.Addrs),
		)
	}
	fmt.Println()
}

func hostLabel() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "mesh-node"
	}
	return name
}
