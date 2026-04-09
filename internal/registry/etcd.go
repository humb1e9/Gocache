package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"

	etcd "go.etcd.io/etcd/client/v3"
)

// Node describes a cache node published to etcd.
type Node struct {
	ID       string `json:"id"`
	GRPCAddr string `json:"grpc_addr"`
}

// EtcdRegistry coordinates node registration and watch-based discovery.
type EtcdRegistry struct {
	client *etcd.Client
	prefix string
}

// New creates a registry bound to an etcd prefix.
func New(client *etcd.Client, prefix string) *EtcdRegistry {
	return &EtcdRegistry{
		client: client,
		prefix: prefix,
	}
}

// Register stores the node under a leased key and keeps the lease alive.
func (r *EtcdRegistry) Register(ctx context.Context, node Node, ttl int64) error {
	payload, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal node: %w", err)
	}

	leaseResp, err := r.client.Grant(ctx, ttl) //租约申请
	if err != nil {
		return fmt.Errorf("grant lease: %w", err)
	}

	key := path.Join(r.prefix, node.ID)
	if _, err := r.client.Put(ctx, key, string(payload), etcd.WithLease(leaseResp.ID)); err != nil {
		return fmt.Errorf("register node: %w", err)
	}

	keepAliveCh, err := r.client.KeepAlive(ctx, leaseResp.ID)
	if err != nil {
		return fmt.Errorf("keepalive lease: %w", err)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-keepAliveCh:
				if !ok {
					return
				}
			}
		}
	}()

	return nil
}

// List returns the currently registered nodes.
func (r *EtcdRegistry) List(ctx context.Context) ([]Node, error) {
	resp, err := r.client.Get(ctx, r.prefix, etcd.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	nodes := make([]Node, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var node Node
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			return nil, fmt.Errorf("decode node %s: %w", string(kv.Key), err)
		}
		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
	return nodes, nil
}

// Watch streams full node snapshots whenever the prefix changes.
func (r *EtcdRegistry) Watch(ctx context.Context) <-chan []Node {
	updates := make(chan []Node, 1)

	go func() {
		defer close(updates)

		sendSnapshot := func() {
			nodes, err := r.List(ctx)
			if err != nil {
				return
			}
			select {
			case updates <- nodes:
			case <-ctx.Done():
			}
		}

		sendSnapshot()
		watchCh := r.client.Watch(ctx, r.prefix, etcd.WithPrefix())
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-watchCh:
				if !ok {
					return
				}
				sendSnapshot()
			}
		}
	}()

	return updates
}
