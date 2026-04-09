package cache

import (
	"gocache/internal/policy"
	"gocache/internal/value"
)

type ByteView = value.ByteView
type EvictionPolicy = policy.EvictionPolicy

const (
	PolicyLRU = policy.PolicyLRU
	PolicyLFU = policy.PolicyLFU
)
