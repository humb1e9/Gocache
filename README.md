# GoCache 分布式缓存服务

`GoCache` 是一个基于 Go 实现的轻量级分布式缓存服务，提供：

- 本地缓存命中与回源加载
- `LRU` / `LFU` 两种淘汰策略
- 基于一致性哈希的节点路由
- 基于 etcd 的服务注册与节点发现
- 基于 gRPC + Protobuf 的节点间通信
- 对外暴露简单的 HTTP JSON 查询接口

当前项目更偏向“分布式缓存原型 / 课程设计 / 面试项目”形态，代码结构清晰，适合演示缓存命中、节点发现和分布式路由流程。

## 1. 项目特点

- 双层本地缓存：`main cache + hot cache`
- 分片缓存：默认 32 个 shard，降低高并发下的锁竞争
- SingleFlight：同一时刻相同 key 只触发一次真实加载
- 一致性哈希：节点变化时尽量减少 key 重映射
- 自动发现：节点启动后注册到 etcd，并通过 Watch 刷新 peer 列表
- 对外接口简单：通过 HTTP 直接按 `group + key` 查询缓存

## 2. 运行依赖

- Go `1.25+`
- etcd，可访问默认地址 `127.0.0.1:2379`

## 3. 目录结构

```text
.
|-- api
|   |-- pb                 # protobuf 生成代码
|   `-- proto              # protobuf 定义
|-- config
|   `-- config.json        # 默认运行配置
|-- docs
|   |-- ARCHITECTURE.md
|   `-- INTERVIEW_NOTES.md
|-- internal
|   |-- cache              # 缓存编排、peer 管理、singleflight
|   |-- config             # 配置加载与校验
|   |-- hash               # 一致性哈希
|   |-- policy             # LRU / LFU
|   |-- registry           # etcd 注册与发现
|   `-- value              # 不可变缓存值封装
|-- main.go                # 服务入口
`-- README.md
```

## 4. 快速启动

### 4.1 准备 etcd

确保本地 etcd 已启动，并监听：

```bash
127.0.0.1:2379
```

### 4.2 使用默认配置启动

项目默认读取：

```bash
config/config.json
```

直接运行：

```bash
go run .
```

### 4.3 使用自定义配置启动

也可以通过环境变量指定配置文件：

```bash
GOCACHE_CONFIG=config/node1.json go run .
```

Windows PowerShell 示例：

```powershell
$env:GOCACHE_CONFIG = "config/node1.json"
go run .
```

## 5. 配置说明

默认配置文件示例：

```json
{
  "node_id": "node1",
  "grpc_addr": "127.0.0.1:50051",
  "http_addr": "127.0.0.1:8080",
  "group": "scores",
  "policy": "lru",
  "cache_bytes": 67108864,
  "etcd_endpoints": ["127.0.0.1:2379"],
  "service_prefix": "/services/gocache/nodes",
  "lease_ttl": 10
}
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `node_id` | 节点唯一标识 |
| `grpc_addr` | 节点间通信地址 |
| `http_addr` | 对外 HTTP 接口地址 |
| `group` | 默认缓存分组 |
| `policy` | 淘汰策略，支持 `lru` / `lfu` |
| `cache_bytes` | 本地缓存总容量，单位字节 |
| `etcd_endpoints` | etcd 地址列表 |
| `service_prefix` | 节点注册前缀 |
| `lease_ttl` | etcd 租约 TTL，单位秒 |

部分字段支持默认值：

- `group` 默认为 `scores`
- `policy` 默认为 `lru`
- `cache_bytes` 默认为 `64MB`
- `etcd_endpoints` 默认为 `127.0.0.1:2379`
- `service_prefix` 默认为 `/services/gocache/nodes`
- `lease_ttl` 默认为 `10`
- `node_id` 为空时，会根据 `grpc_addr` 自动推导

必须提供：

- `grpc_addr`
- `http_addr`

## 6. 对外接口

### 6.1 查询缓存

接口：

```http
POST /api/cache/get
Content-Type: application/json
```

请求体：

```json
{
  "group": "scores",
  "key": "Tom"
}
```

返回示例：

```json
{
  "group": "scores",
  "key": "Tom",
  "value": "630"
}
```

命令示例：

```bash
curl -X POST http://127.0.0.1:8080/api/cache/get \
  -H "Content-Type: application/json" \
  -d "{\"group\":\"scores\",\"key\":\"Tom\"}"
```

说明：

- `group` 为空时，会使用配置中的默认分组
- `key` 必填
- 如果 `group` 与当前节点配置不一致，会返回 `unknown group`
- 如果 key 不存在，会返回 `404`
- 接口也兼容表单请求

### 6.2 健康检查

接口：

```http
GET /healthz
```

返回示例：

```json
{
  "status": "ok"
}
```

## 7. 节点间通信

节点之间使用 gRPC 通信，协议定义位于：

```text
api/proto/cache.proto
```

当前包含两个 RPC：

- `Get`：节点间拉取缓存值
- `Ping`：轻量健康探测

## 8. 缓存访问流程

以查询 `scores/Tom` 为例：

1. 客户端请求某个节点的 HTTP 接口
2. 节点先查本地 `hot cache`
3. 未命中时再查本地 `main cache`
4. 仍未命中时进入 `SingleFlight`
5. 通过一致性哈希选择负责该 key 的节点
6. 如果目标节点不是自己，则通过 gRPC 向远端拉取
7. 如果远端失败或当前节点就是负责节点，则回源加载
8. 成功后写入本地缓存并返回结果

当前回源数据源写在 `main.go` 中，是一个内置示例 map：

```go
var db = map[string]string{
    "Tom":  "630",
    "Jack": "589",
    "Sam":  "567",
}
```

如果你后续要接真实数据库，通常从这里替换成数据库查询逻辑即可。

## 9. 多节点示例

可以准备三份配置，例如：

- `config/node1.json`
- `config/node2.json`
- `config/node3.json`

区别主要在于：

- `node_id`
- `grpc_addr`
- `http_addr`

例如：

```json
{
  "node_id": "node2",
  "grpc_addr": "127.0.0.1:50052",
  "http_addr": "127.0.0.1:8081",
  "group": "scores",
  "policy": "lfu",
  "cache_bytes": 67108864,
  "etcd_endpoints": ["127.0.0.1:2379"],
  "service_prefix": "/services/gocache/nodes",
  "lease_ttl": 10
}
```

分别启动：

```powershell
$env:GOCACHE_CONFIG = "config/node1.json"
go run .
```

```powershell
$env:GOCACHE_CONFIG = "config/node2.json"
go run .
```

```powershell
$env:GOCACHE_CONFIG = "config/node3.json"
go run .
```

节点会自动注册到 etcd，并在 peer 变更时刷新本地路由信息。

## 10. 核心实现

- `internal/cache/group.go`
  统一处理缓存命中、远程拉取和本地回源
- `internal/cache/cache.go`
  实现双层缓存和分片缓存
- `internal/cache/singleflight.go`
  合并同 key 并发加载请求
- `internal/cache/manager.go`
  维护 peer 连接、哈希环，并提供 gRPC 服务
- `internal/hash/consistenthash.go`
  提供一致性哈希路由能力
- `internal/registry/etcd.go`
  封装 etcd 注册、续租、节点列表与 Watch
- `internal/policy/lru.go`
  LRU 淘汰策略
- `internal/policy/lfu.go`
  LFU 淘汰策略

## 11. 当前限制

当前版本已经具备分布式缓存主链路，但还没有实现：

- TTL / 过期时间控制
- 主动写入、删除、更新接口
- 数据持久化
- 副本同步
- 指标监控与 tracing
- 压测脚本和容器化编排

## 12. 开发与验证

已执行：

```bash
go test ./...
```

当前结果：

```text
全部通过，无失败用例
```

## 13. 相关文档

- [架构说明](./docs/ARCHITECTURE.md)
- [面试要点](./docs/INTERVIEW_NOTES.md)
