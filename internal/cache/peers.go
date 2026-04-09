package cache

import "context"

// PeerPicker picks a remote peer for a specific key.
type PeerPicker interface {
	PickPeer(key string) (PeerGetter, bool)
}

// PeerGetter loads cached data from a remote peer.
type PeerGetter interface {
	Get(ctx context.Context, in *Request, out *Response) error
}

// Request describes a peer fetch request.
type Request struct {
	Group string
	Key   string
}

// Response describes a peer fetch response.
type Response struct {
	Value []byte
}
