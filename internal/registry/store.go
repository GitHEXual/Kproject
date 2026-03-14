package registry

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

type PeerRecord struct {
	ShortID string
	PeerID  peer.ID
	Addrs   []ma.Multiaddr
}

type Store struct {
	mu      sync.RWMutex
	byShort map[string]*PeerRecord
	byPeer  map[peer.ID]*PeerRecord
}

func NewStore() *Store {
	return &Store{
		byShort: make(map[string]*PeerRecord),
		byPeer:  make(map[peer.ID]*PeerRecord),
	}
}

func (s *Store) Register(rec *PeerRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old, ok := s.byPeer[rec.PeerID]; ok {
		delete(s.byShort, old.ShortID)
	}

	s.byShort[rec.ShortID] = rec
	s.byPeer[rec.PeerID] = rec
}

func (s *Store) Unregister(pid peer.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.byPeer[pid]; ok {
		delete(s.byShort, rec.ShortID)
		delete(s.byPeer, pid)
	}
}

func (s *Store) LookupByShortID(shortID string) (*PeerRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byShort[shortID]
	return rec, ok
}

func (s *Store) LookupByPeerID(pid peer.ID) (*PeerRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byPeer[pid]
	return rec, ok
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byShort)
}
