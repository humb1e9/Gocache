package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"gocache/api/pb"
	cache "gocache/internal/cache"
	"gocache/internal/config"
	"gocache/internal/registry"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Run starts the cache node and blocks until shutdown or error.
func Run(ctx context.Context, cfg config.Config) error {
	evictionPolicy, err := parseEvictionPolicy(cfg.Policy)
	if err != nil {
		return err
	}

	group := createGroupWithPolicy(cfg.Group, cfg.CacheBytes, evictionPolicy)
	manager := cache.NewManager(cfg.GRPCAddr)
	group.RegisterPeers(manager)

	httpServer := newHTTPServer(cfg.HTTPAddr, cfg.Group, group)

	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.EtcdEndpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("connect etcd: %w", err)
	}
	defer etcdClient.Close()
	defer manager.Close()

	reg := registry.New(etcdClient, cfg.ServicePrefix)
	if err := registerAndSyncPeers(ctx, reg, manager, cfg); err != nil {
		return err
	}

	lis, grpcServer, err := newGRPCServer(cfg.GRPCAddr, manager)
	if err != nil {
		return err
	}
	defer lis.Close()

	return serveNode(ctx, cfg, httpServer, grpcServer, lis)
}

func parseEvictionPolicy(raw string) (cache.EvictionPolicy, error) {
	policy := cache.EvictionPolicy(raw)
	if policy != cache.PolicyLRU && policy != cache.PolicyLFU {
		return "", fmt.Errorf("unsupported policy %s", raw)
	}
	return policy, nil
}

func registerAndSyncPeers(ctx context.Context, reg *registry.EtcdRegistry, manager *cache.Manager, cfg config.Config) error {
	if err := reg.Register(ctx, registry.Node{
		ID:       cfg.NodeID,
		GRPCAddr: cfg.GRPCAddr,
	}, cfg.LeaseTTL); err != nil {
		return fmt.Errorf("register node: %w", err)
	}

	initialNodes, err := reg.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	applyNodes(manager, initialNodes)

	go func() {
		for nodes := range reg.Watch(ctx) {
			if len(nodes) == 0 {
				continue
			}
			applyNodes(manager, nodes)
		}
	}()

	return nil
}

func applyNodes(manager *cache.Manager, nodes []registry.Node) {
	addrs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		addrs = append(addrs, node.GRPCAddr)
	}
	if err := manager.Set(addrs...); err != nil {
		log.Printf("[discovery] refresh peers failed: %v", err)
		return
	}
	log.Printf("[discovery] active peers: %v", addrs)
}

func newGRPCServer(addr string, manager *cache.Manager) (net.Listener, *grpc.Server, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterCacheServiceServer(grpcServer, manager)
	reflection.Register(grpcServer)
	return lis, grpcServer, nil
}

func serveNode(ctx context.Context, cfg config.Config, httpServer *http.Server, grpcServer *grpc.Server, lis net.Listener) error {
	serverErrCh := make(chan error, 1)
	go func() {
		log.Printf("cache node %s listening on grpc=%s http=%s", cfg.NodeID, cfg.GRPCAddr, cfg.HTTPAddr)
		serverErrCh <- grpcServer.Serve(lis)
	}()

	httpErrCh := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			httpErrCh <- nil
			return
		}
		httpErrCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownServers(httpServer, grpcServer)
		return nil
	case err := <-serverErrCh:
		return err
	case err := <-httpErrCh:
		return err
	}
}

func shutdownServers(httpServer *http.Server, grpcServer *grpc.Server) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_ = httpServer.Shutdown(shutdownCtx)

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		grpcServer.Stop()
	}
}
