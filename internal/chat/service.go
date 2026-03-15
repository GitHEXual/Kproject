package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	ma "github.com/multiformats/go-multiaddr"
)

const ChatProtocol = protocol.ID("/kproject/chat/1.0.0")

type ConnectStatus struct {
	Step    int
	Message string
	Done    bool
	Error   error
}

type Service struct {
	host    host.Host
	shortID string

	mu      sync.RWMutex
	streams map[peer.ID]network.Stream

	// OnIncomingRequest is called when a remote peer opens a chat stream.
	// It blocks the stream handler goroutine until it returns.
	// Return true to accept, false to reject (stream will be closed).
	OnIncomingRequest func(peerID peer.ID) bool

	OnMessage        func(msg Message)
	OnPeerDisconnect func(peerID peer.ID)
}

func NewService(h host.Host, shortID string) *Service {
	svc := &Service{
		host:    h,
		shortID: shortID,
		streams: make(map[peer.ID]network.Stream),
	}
	h.SetStreamHandler(ChatProtocol, svc.handleIncoming)
	return svc
}

func (s *Service) handleIncoming(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()

	if s.OnIncomingRequest != nil {
		accepted := s.OnIncomingRequest(remotePeer)
		if !accepted {
			stream.Reset()
			return
		}
	}

	s.mu.Lock()
	if old, exists := s.streams[remotePeer]; exists {
		old.Close()
	}
	s.streams[remotePeer] = stream
	s.mu.Unlock()

	s.readLoop(stream, remotePeer)
}

func (s *Service) readLoop(stream network.Stream, remotePeer peer.ID) {
	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("chat: read error from %s: %v", remotePeer, err)
			}
			s.mu.Lock()
			if current, ok := s.streams[remotePeer]; ok && current == stream {
				delete(s.streams, remotePeer)
			}
			s.mu.Unlock()
			if s.OnPeerDisconnect != nil {
				s.OnPeerDisconnect(remotePeer)
			}
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if s.OnMessage != nil {
			s.OnMessage(msg)
		}
	}
}

func (s *Service) clearBackoff(pid peer.ID) {
	if sw, ok := s.host.Network().(*swarm.Swarm); ok {
		sw.Backoff().Clear(pid)
	}
}

func (s *Service) ConnectToPeer(ctx context.Context, info *peer.AddrInfo, statusCh chan<- ConnectStatus) error {
	defer close(statusCh)

	// Separate direct and relay addresses from the lookup result.
	var directAddrs, relayAddrs []ma.Multiaddr
	for _, addr := range info.Addrs {
		isRelay := false
		for _, p := range addr.Protocols() {
			if p.Code == ma.P_CIRCUIT {
				isRelay = true
				break
			}
		}
		if isRelay {
			relayAddrs = append(relayAddrs, addr)
		} else {
			directAddrs = append(directAddrs, addr)
		}
	}

	// Step 1: Try direct addresses only
	var err error
	if len(directAddrs) > 0 {
		statusCh <- ConnectStatus{Step: 1, Message: "Прямое подключение..."}
		s.host.Peerstore().AddAddrs(info.ID, directAddrs, time.Hour)
		directInfo := peer.AddrInfo{ID: info.ID, Addrs: directAddrs}
		directCtx, directCancel := context.WithTimeout(ctx, 8*time.Second)
		err = s.host.Connect(directCtx, directInfo)
		directCancel()
	} else {
		err = fmt.Errorf("no direct addresses")
	}

	// Step 2: NAT port mapping + retry direct
	if err != nil {
		s.clearBackoff(info.ID)
		statusCh <- ConnectStatus{Step: 2, Message: "NAT port mapping (UPnP/NAT-PMP)..."}
		time.Sleep(3 * time.Second)

		if len(directAddrs) > 0 {
			directInfo := peer.AddrInfo{ID: info.ID, Addrs: directAddrs}
			retryCtx, retryCancel := context.WithTimeout(ctx, 8*time.Second)
			err = s.host.Connect(retryCtx, directInfo)
			retryCancel()
		}
	}

	// Step 3: Connect via relay (enables hole punching coordination via DCUtR)
	if err != nil {
		s.clearBackoff(info.ID)
		statusCh <- ConnectStatus{Step: 3, Message: "Подключение через relay..."}

		allRelayAddrs := append(relayAddrs, s.buildRelayAddrs(info.ID)...)
		s.host.Peerstore().AddAddrs(info.ID, allRelayAddrs, time.Hour)
		if len(allRelayAddrs) > 0 {
			relayInfo := peer.AddrInfo{ID: info.ID, Addrs: allRelayAddrs}
			relayCtx, relayCancel := context.WithTimeout(ctx, 15*time.Second)
			err = s.host.Connect(relayCtx, relayInfo)
			relayCancel()
		}
	}

	// Step 4: If relay connected, wait for hole punch to upgrade to direct
	if err == nil && !s.hasDirectConn(info.ID) {
		statusCh <- ConnectStatus{Step: 4, Message: "Hole punching (DCUtR)..."}
		time.Sleep(5 * time.Second)
		if s.hasDirectConn(info.ID) {
			log.Printf("hole punch succeeded, upgraded to direct connection")
		}
	}

	// Step 5: Final fallback — retry everything with clean backoff
	if err != nil {
		s.clearBackoff(info.ID)
		statusCh <- ConnectStatus{Step: 5, Message: "Финальная попытка (все адреса)..."}

		allAddrs := append(directAddrs, relayAddrs...)
		allAddrs = append(allAddrs, s.buildRelayAddrs(info.ID)...)
		s.host.Peerstore().AddAddrs(info.ID, allAddrs, time.Hour)
		if len(allAddrs) > 0 {
			finalInfo := peer.AddrInfo{ID: info.ID, Addrs: allAddrs}
			finalCtx, finalCancel := context.WithTimeout(ctx, 20*time.Second)
			err = s.host.Connect(finalCtx, finalInfo)
			finalCancel()
		}

		if err != nil {
			statusCh <- ConnectStatus{Step: 5, Message: "Не удалось подключиться", Done: true, Error: err}
			return fmt.Errorf("all connection attempts failed: %w", err)
		}
	}

	// Open chat stream
	connType := s.connectionType(info.ID)
	statusCh <- ConnectStatus{Step: 6, Message: fmt.Sprintf("Открытие чат-канала (%s)...", connType)}
	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	if connType == "relay" {
		streamCtx = network.WithAllowLimitedConn(streamCtx, "chat over relay")
	}
	stream, err := s.host.NewStream(streamCtx, info.ID, ChatProtocol)
	streamCancel()
	if err != nil {
		statusCh <- ConnectStatus{Done: true, Error: fmt.Errorf("stream open failed: %w", err)}
		return err
	}

	s.mu.Lock()
	if old, exists := s.streams[info.ID]; exists {
		old.Close()
	}
	s.streams[info.ID] = stream
	s.mu.Unlock()

	go s.readLoop(stream, info.ID)

	statusCh <- ConnectStatus{Step: 6, Message: fmt.Sprintf("Подключено (%s)", connType), Done: true}
	return nil
}

func (s *Service) buildRelayAddrs(target peer.ID) []ma.Multiaddr {
	var relayAddrs []ma.Multiaddr
	for _, conn := range s.host.Network().Conns() {
		remotePeer := conn.RemotePeer()
		if remotePeer == target {
			continue
		}
		relayAddr, err := ma.NewMultiaddr(
			fmt.Sprintf("%s/p2p/%s/p2p-circuit/p2p/%s",
				conn.RemoteMultiaddr(), remotePeer, target))
		if err == nil {
			relayAddrs = append(relayAddrs, relayAddr)
		}
	}
	return relayAddrs
}

func (s *Service) hasDirectConn(pid peer.ID) bool {
	conns := s.host.Network().ConnsToPeer(pid)
	for _, c := range conns {
		isRelay := false
		for _, p := range c.RemoteMultiaddr().Protocols() {
			if p.Code == ma.P_CIRCUIT {
				isRelay = true
				break
			}
		}
		if !isRelay {
			return true
		}
	}
	return false
}

func (s *Service) connectionType(pid peer.ID) string {
	if s.hasDirectConn(pid) {
		return "direct"
	}
	conns := s.host.Network().ConnsToPeer(pid)
	if len(conns) > 0 {
		return "relay"
	}
	return "none"
}

func (s *Service) SendMessage(peerID peer.ID, content string) error {
	s.mu.RLock()
	stream, ok := s.streams[peerID]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no active stream to peer")
	}

	msg := Message{
		From:      s.shortID,
		Content:   content,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	writer := bufio.NewWriter(stream)
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.WriteByte('\n'); err != nil {
		return err
	}
	return writer.Flush()
}

func (s *Service) Disconnect(peerID peer.ID) {
	s.mu.Lock()
	if stream, ok := s.streams[peerID]; ok {
		stream.Reset()
		delete(s.streams, peerID)
	}
	s.mu.Unlock()
}

func (s *Service) HasStream(peerID peer.ID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.streams[peerID]
	return ok
}

func (s *Service) ConnectionType(peerID peer.ID) string {
	return s.connectionType(peerID)
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stream := range s.streams {
		stream.Close()
	}
	s.streams = make(map[peer.ID]network.Stream)
}
