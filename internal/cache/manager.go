package cache

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"gocache/api/pb"
	cachehash "gocache/internal/hash"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultReplicas   = 50
	defaultRPCTimeout = 2 * time.Second
)

// Manager tracks peers, maintains the consistent-hash ring, and serves the cache gRPC API.
type Manager struct {
	pb.UnimplementedCacheServiceServer

	self       string
	replicas   int
	rpcTimeout time.Duration

	mu      sync.RWMutex
	peers   *cachehash.HashRing
	conns   map[string]*grpc.ClientConn
	clients map[string]*grpcGetter
}

// NewManager creates a gRPC peer manager for the current node.
func NewManager(self string) *Manager {
	return &Manager{
		self:       self,
		replicas:   defaultReplicas,
		rpcTimeout: defaultRPCTimeout,
		peers:      cachehash.NewHashRing(defaultReplicas, nil),
		conns:      make(map[string]*grpc.ClientConn),
		clients:    make(map[string]*grpcGetter),
	}
}

// Set refreshes the peer list and hash ring from discovery results.
func (m *Manager) Set(peers ...string) error {
	uniquePeers := uniqueStrings(peers)
	newRing := cachehash.NewHashRing(m.replicas, nil)
	newRing.Set(uniquePeers...)

	m.mu.Lock()
	defer m.mu.Unlock()

	nextConns := make(map[string]*grpc.ClientConn, len(uniquePeers))
	nextClients := make(map[string]*grpcGetter, len(uniquePeers))

	for _, peerAddr := range uniquePeers {
		if peerAddr == m.self {
			continue
		}

		if conn, ok := m.conns[peerAddr]; ok {
			nextConns[peerAddr] = conn
			nextClients[peerAddr] = m.clients[peerAddr]
			continue
		}

		conn, err := grpc.NewClient(peerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial peer %s: %w", peerAddr, err)
		}
		nextConns[peerAddr] = conn
		nextClients[peerAddr] = &grpcGetter{
			client:     pb.NewCacheServiceClient(conn),
			rpcTimeout: m.rpcTimeout,
		}
	}

	for addr, conn := range m.conns {
		if _, ok := nextConns[addr]; !ok {
			_ = conn.Close()
		}
	}

	m.peers = newRing
	m.conns = nextConns
	m.clients = nextClients
	return nil
}

// Close releases remote peer connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var closeErr error
	for addr, conn := range m.conns {
		if err := conn.Close(); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("close %s: %w", addr, err)
		}
	}
	m.conns = make(map[string]*grpc.ClientConn)
	m.clients = make(map[string]*grpcGetter)
	return closeErr
}

// PickPeer chooses a remote owner for the key if the selected node is not self.
func (m *Manager) PickPeer(key string) (PeerGetter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.peers == nil {
		return nil, false
	}

	peerAddr := m.peers.Get(key)
	if peerAddr == "" || peerAddr == m.self {
		return nil, false
	}

	getter, ok := m.clients[peerAddr]
	return getter, ok
}

// Get serves node-to-node cache fetches over gRPC.
func (m *Manager) Get(ctx context.Context, req *pb.CacheGetRequest) (*pb.CacheGetResponse, error) {
	if req.GetGroup() == "" || req.GetKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "group and key are required")
	}

	group := GetGroup(req.GetGroup())
	if group == nil {
		return nil, status.Errorf(codes.NotFound, "group %s not found", req.GetGroup())
	}

	view, err := group.Get(ctx, req.GetKey())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return &pb.CacheGetResponse{
		Value: view.ByteSlice(),
	}, nil
}

// Ping is a lightweight health probe for gRPC clients and operational checks.
func (m *Manager) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	return &pb.PingResponse{
		Node:      m.self,
		Timestamp: time.Now().Unix(),
		Message:   "ok",
	}, nil
}

type grpcGetter struct {
	client     pb.CacheServiceClient
	rpcTimeout time.Duration
}

func (g *grpcGetter) Get(ctx context.Context, in *Request, out *Response) error {
	callCtx, cancel := context.WithTimeout(ctx, g.rpcTimeout)
	defer cancel()

	res, err := g.client.Get(callCtx, &pb.CacheGetRequest{
		Group: in.Group,
		Key:   in.Key,
	})
	if err != nil {
		return err
	}
	out.Value = res.GetValue()
	return nil
}

func uniqueStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(items))
	unique := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		unique = append(unique, item)
	}
	slices.Sort(unique)
	log.Printf("[peer] refreshed peers: %v", unique)
	return unique
}
