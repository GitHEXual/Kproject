package registry

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const ProtocolID = protocol.ID("/kproject/registry/1.0.0")

type Request struct {
	Type    string   `json:"type"`
	ShortID string   `json:"short_id,omitempty"`
	PeerID  string   `json:"peer_id,omitempty"`
	Addrs   []string `json:"addrs,omitempty"`
}

type Response struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	ShortID string   `json:"short_id,omitempty"`
	PeerID  string   `json:"peer_id,omitempty"`
	Addrs   []string `json:"addrs,omitempty"`
}

// Server-side: attach the registry handler to the host.
func AttachServerHandler(h host.Host, store *Store) {
	h.SetStreamHandler(ProtocolID, func(s network.Stream) {
		defer s.Close()
		handleStream(s, store, h)
	})
}

func handleStream(s network.Stream, store *Store, h host.Host) {
	reader := bufio.NewReader(s)
	writer := bufio.NewWriter(s)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("registry: read error from %s: %v", s.Conn().RemotePeer(), err)
			}
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeResponse(writer, Response{OK: false, Error: "invalid json"})
			continue
		}

		switch req.Type {
		case "REGISTER":
			handleRegister(s, store, h, &req, writer)
		case "LOOKUP":
			handleLookup(store, h, &req, writer)
		case "UNREGISTER":
			store.Unregister(s.Conn().RemotePeer())
			writeResponse(writer, Response{OK: true})
			return
		default:
			writeResponse(writer, Response{OK: false, Error: "unknown command"})
		}
	}
}

func handleRegister(s network.Stream, store *Store, h host.Host, req *Request, w *bufio.Writer) {
	remotePeer := s.Conn().RemotePeer()

	var addrs []ma.Multiaddr
	for _, a := range req.Addrs {
		maddr, err := ma.NewMultiaddr(a)
		if err == nil {
			addrs = append(addrs, maddr)
		}
	}

	peerAddrs := h.Peerstore().Addrs(remotePeer)
	addrs = append(addrs, peerAddrs...)

	store.Register(&PeerRecord{
		ShortID: req.ShortID,
		PeerID:  remotePeer,
		Addrs:   addrs,
	})

	log.Printf("registry: registered %s (peer: %s, addrs: %d)", req.ShortID, remotePeer, len(addrs))
	writeResponse(w, Response{OK: true})
}

func handleLookup(store *Store, h host.Host, req *Request, w *bufio.Writer) {
	rec, ok := store.LookupByShortID(req.ShortID)
	if !ok {
		writeResponse(w, Response{OK: false, Error: "peer not found"})
		return
	}

	addrSet := make(map[string]bool)
	var addrStrs []string
	for _, a := range rec.Addrs {
		s := a.String()
		if !addrSet[s] {
			addrSet[s] = true
			addrStrs = append(addrStrs, s)
		}
	}

	// Inject relay circuit addresses through this server so the
	// requesting peer can reach the target even if both are behind NAT.
	for _, serverAddr := range h.Addrs() {
		relayAddr := fmt.Sprintf("%s/p2p/%s/p2p-circuit/p2p/%s",
			serverAddr, h.ID(), rec.PeerID)
		if !addrSet[relayAddr] {
			addrSet[relayAddr] = true
			addrStrs = append(addrStrs, relayAddr)
		}
	}

	writeResponse(w, Response{
		OK:      true,
		ShortID: rec.ShortID,
		PeerID:  rec.PeerID.String(),
		Addrs:   addrStrs,
	})
}

func writeResponse(w *bufio.Writer, resp Response) {
	data, _ := json.Marshal(resp)
	w.Write(data)
	w.WriteByte('\n')
	w.Flush()
}

// Client-side functions

type Client struct {
	host   host.Host
	server peer.ID
}

func NewClient(h host.Host, serverID peer.ID) *Client {
	return &Client{host: h, server: serverID}
}

func (c *Client) sendRequest(ctx context.Context, req *Request) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	s, err := c.host.NewStream(ctx, c.server, ProtocolID)
	if err != nil {
		return nil, fmt.Errorf("failed to open registry stream: %w", err)
	}
	defer s.Close()

	writer := bufio.NewWriter(s)
	data, _ := json.Marshal(req)
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()

	reader := bufio.NewReader(s)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &resp, nil
}

func (c *Client) Register(ctx context.Context, shortID string, addrs []ma.Multiaddr) error {
	var addrStrs []string
	for _, a := range addrs {
		addrStrs = append(addrStrs, a.String())
	}

	resp, err := c.sendRequest(ctx, &Request{
		Type:    "REGISTER",
		ShortID: shortID,
		Addrs:   addrStrs,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("register failed: %s", resp.Error)
	}
	return nil
}

func (c *Client) Lookup(ctx context.Context, shortID string) (*peer.AddrInfo, error) {
	resp, err := c.sendRequest(ctx, &Request{
		Type:    "LOOKUP",
		ShortID: shortID,
	})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	pid, err := peer.Decode(resp.PeerID)
	if err != nil {
		return nil, fmt.Errorf("invalid peer id in response: %w", err)
	}

	var addrs []ma.Multiaddr
	for _, a := range resp.Addrs {
		maddr, err := ma.NewMultiaddr(a)
		if err == nil {
			addrs = append(addrs, maddr)
		}
	}

	return &peer.AddrInfo{ID: pid, Addrs: addrs}, nil
}

func (c *Client) Unregister(ctx context.Context) error {
	_, err := c.sendRequest(ctx, &Request{Type: "UNREGISTER"})
	return err
}
