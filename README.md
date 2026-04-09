# GoCache 分布式缓存数据库

`GoCache` 是一个基于 `Go + gRPC + Protobuf + Etcd + Gin` 实现的轻量级分布式缓存系统。

它提供：

- 本地内存 KV 缓存
- `LRU` / `LFU` 可插拔淘汰策略
- `SingleFlight` 同 Key 回源合并
- 一致性哈希分片路由与虚拟节点
- etcd 注册发现与节点动态感知
- gRPC 节点间通信
- 面向前端的 HTTP JSON 查询接口
- 两级缓存与分段缓存，降低热点冲突和锁竞争

当前项目更偏向“分布式缓存原型 / 课程设计 / 面试项目”形态，代码结构清晰，适合用于学习缓存淘汰、节点路由、服务发现和节点间通信流程。

## 项目特点

- 两级本地缓存：`main cache + hot cache`
- 分段缓存：默认 `32` 个 shard，降低并发争用
- `SingleFlight`：相同 key 的并发 miss 只回源一次
- 一致性哈希：节点变更时尽量减少 key 迁移
- 动态节点发现：节点启动后注册到 etcd，并通过 `Watch` 刷新 peer
- gRPC + Protobuf：节点之间高效通信
- Gin HTTP 接口：前端通过 JSON 传入 `group` 和 `key`

## 运行依赖

- Go `1.25+`
- etcd，默认地址 `127.0.0.1:2379`

## 目录结构

```text
.
|-- api
|   |-- pb                    # protobuf 生成代码
|   `-- proto                 # protobuf 定义
|-- app
|   |-- http.go               # Gin HTTP 接口
|   |-- run.go                # 节点启动、gRPC、etcd、优雅退出
|   `-- source.go             # 演示数据源与 Group 初始化
|-- config
|   `-- config.json           # 默认运行配置
|-- docs
|   |-- ARCHITECTURE.md
|   `-- INTERVIEW_NOTES.md
|-- internal
|   |-- cache                 # 缓存编排、peer 管理、singleflight
|   |-- config                # 配置加载与校验
|   |-- hash                  # 一致性哈希 HashRing
|   |-- policy                # LRU / LFU 淘汰策略
|   |-- registry              # etcd 注册与发现
|   `-- value                 # ByteView 等值对象
|-- main.go                   # 程序入口
|-- go.mod
|-- go.sum
`-- README.md
```

## 快速启动

### 1. 准备 etcd

确保本地 etcd 已启动，并监听：

```text
127.0.0.1:2379
```

### 2. 使用默认配置启动

项目默认读取：

```text
config/config.json
```

直接运行：

```bash
go run .
```

### 3. 使用自定义配置启动

也可以通过环境变量指定配置文件：

```bash
GOCACHE_CONFIG=config/node1.json go run .
```

Windows PowerShell 示例：

```powershell
$env:GOCACHE_CONFIG = "config/node1.json"
go run .
```

## 配置说明

默认配置示例：

```json
{
  "node_id": "node1",
  "grpc_addr": "127.0.0.1:50051",
  "http_addr": "127.0.0.1:8080",
  "group": "scores",
  "policy": "lru",
  "cache_bytes": 67108864,
  "etcd_endpoints": [
    "127.0.0.1:2379"
  ],
  "service_prefix": "/services/gocache/nodes",
  "lease_ttl": 10
}
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `node_id` | 节点唯一标识 |
| `grpc_addr` | 节点间 gRPC 通信地址 |
| `http_addr` | 对外 HTTP 接口地址 |
| `group` | 默认缓存分组 |
| `policy` | 淘汰策略，支持 `lru` / `lfu` |
| `cache_bytes` | 本地缓存总容量，单位字节 |
| `etcd_endpoints` | etcd 地址列表 |
| `service_prefix` | 节点注册前缀 |
| `lease_ttl` | etcd 租约 TTL，单位秒 |

默认值：

- `group` 默认 `scores`
- `policy` 默认 `lru`
- `cache_bytes` 默认 `64MB`
- `etcd_endpoints` 默认 `127.0.0.1:2379`
- `service_prefix` 默认 `/services/gocache/nodes`
- `lease_ttl` 默认 `10`
- `node_id` 为空时，会根据 `grpc_addr` 自动推导

必填项：

- `grpc_addr`
- `http_addr`

## HTTP 接口

### 1. 查询缓存

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

`curl` 示例：

```bash
curl -X POST http://127.0.0.1:8080/api/cache/get \
  -H "Content-Type: application/json" \
  -d "{\"group\":\"scores\",\"key\":\"Tom\"}"
```

说明：

- `group` 为空时，使用配置中的默认分组
- `key` 为必填
- 如果 `group` 与当前节点配置不一致，返回 `unknown group`
- 如果 `key` 不存在，返回 `404`
- 接口同时兼容表单参数

### 2. 健康检查

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

## 节点间通信

节点之间使用 gRPC 通信，协议定义位于：

```text
api/proto/cache.proto
```

当前包含两个 RPC：

- `Get`：节点间拉取缓存值
- `Ping`：轻量健康探测

## 缓存访问流程

以查询 `scores/Tom` 为例：

1. 客户端请求某个节点的 HTTP 接口 `/api/cache/get`
2. Gin handler 解析 `group` 和 `key`
3. 进入 `Group.Get()`
4. 先查本地 `hot cache`
5. 未命中再查本地 `main cache`
6. 仍未命中则进入 `SingleFlight`
7. 通过一致性哈希选择负责该 key 的节点
8. 如果目标节点不是自己，则通过 gRPC 向远端节点发起 `Get`
9. 如果远端失败，或当前节点就是负责节点，则执行本地回源
10. 回源成功后写入本地缓存，再返回结果

当前演示回源数据位于：

```text
app/source.go
```

里面是一个简单的内置 map：

```go
var db = map[string]string{
    "Tom":  "630",
    "Jack": "589",
    "Sam":  "567",
}
```

如果后续要接真实数据库，通常直接把这里替换成数据库查询逻辑即可。

## 多节点示例

可以准备多份配置，例如：

- `config/node1.json`
- `config/node2.json`
- `config/node3.json`

它们主要区别在于：

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
  "etcd_endpoints": [
    "127.0.0.1:2379"
  ],
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

## 核心实现

- `app/run.go`
  负责节点启动、etcd 注册发现、gRPC 服务和优雅退出
- `app/http.go`
  负责 Gin 路由与 HTTP 查询接口
- `app/source.go`
  负责演示数据源与 `Group` 初始化
- `internal/cache/group.go`
  统一处理缓存命中、远程拉取和本地回源
- `internal/cache/cache.go`
  实现两级缓存与分段缓存
- `internal/cache/singleflight.go`
  合并相同 key 并发回源请求
- `internal/cache/manager.go`
  维护 peer 连接、哈希环，并提供 gRPC 服务
- `internal/hash/consistenthash.go`
  实现一致性哈希 `HashRing`
- `internal/registry/etcd.go`
  封装 etcd 注册、续租、节点列表和 watch
- `internal/policy/lru.go`
  LRU 淘汰策略
- `internal/policy/lfu.go`
  LFU 淘汰策略

## 当前限制

当前版本已经具备分布式缓存主链路，但还没有实现：

- TTL / 过期时间控制
- 主动写入、删除、更新接口
- 数据持久化
- 副本同步
- 指标监控与 tracing
- 压测脚本和容器化编排
