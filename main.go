package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gocache/api/pb"
	cache "gocache/internal/cache"
	"gocache/internal/config"
	"gocache/internal/registry"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var db = map[string]string{
	"Tom":  "630",
	"Jack": "589",
	"Sam":  "567",
}

type getRequest struct {
	Group string `json:"group"`
	Key   string `json:"key"`
}

type getResponse struct {
	Group string `json:"group"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func createGroupWithPolicy(groupName string, cacheBytes int64, policy cache.EvictionPolicy) *cache.Group {
	return cache.NewGroupWithPolicy(groupName, cacheBytes, cache.GetterFunc(func(key string) ([]byte, error) {
		log.Printf("[SlowDB] search key=%s", key)
		if v, ok := db[key]; ok {
			return []byte(v), nil
		}
		return nil, fmt.Errorf("%s not found", key)
	}), policy)
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runNode(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func runNode(ctx context.Context, cfg config.Config) error {
	evictionPolicy := cache.EvictionPolicy(cfg.Policy)
	if evictionPolicy != cache.PolicyLRU && evictionPolicy != cache.PolicyLFU {
		return fmt.Errorf("unsupported policy %s", cfg.Policy)
	}

	group := createGroupWithPolicy(cfg.Group, cfg.CacheBytes, evictionPolicy)
	manager := cache.NewManager(cfg.GRPCAddr)
	group.RegisterPeers(manager)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cache/get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, getResponse{Error: "method not allowed"})
			return
		}

		var req getRequest
		if err := decodeGetRequest(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, getResponse{Error: err.Error()})
			return
		}

		groupName := req.Group
		if groupName == "" {
			groupName = cfg.Group
		}
		if groupName != cfg.Group {
			writeJSON(w, http.StatusBadRequest, getResponse{Error: "unknown group"})
			return
		}
		if req.Key == "" {
			writeJSON(w, http.StatusBadRequest, getResponse{Error: "key is required"})
			return
		}

		view, err := group.Get(r.Context(), req.Key)
		if err != nil {
			writeJSON(w, http.StatusNotFound, getResponse{
				Group: groupName,
				Key:   req.Key,
				Error: err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, getResponse{
			Group: groupName,
			Key:   req.Key,
			Value: view.String(),
		})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

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
	if err := reg.Register(ctx, registry.Node{
		ID:       cfg.NodeID,
		GRPCAddr: cfg.GRPCAddr,
	}, cfg.LeaseTTL); err != nil {
		return fmt.Errorf("register node: %w", err)
	}

	applyNodes := func(nodes []registry.Node) {
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

	initialNodes, err := reg.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	applyNodes(initialNodes)

	go func() {
		for nodes := range reg.Watch(ctx) {
			if len(nodes) == 0 {
				continue
			}
			applyNodes(nodes)
		}
	}()

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.GRPCAddr, err)
	}
	defer lis.Close()

	grpcServer := grpc.NewServer()
	pb.RegisterCacheServiceServer(grpcServer, manager)
	reflection.Register(grpcServer)

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
		return nil
	case err := <-serverErrCh:
		return err
	case err := <-httpErrCh:
		return err
	}
}

func decodeGetRequest(r *http.Request, req *getRequest) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			return fmt.Errorf("invalid json body")
		}
		return nil
	}

	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("invalid form body")
	}
	req.Group = r.FormValue("group")
	req.Key = r.FormValue("key")
	return nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json response failed: %v", err)
	}
}
