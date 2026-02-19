# RAFT-KV-STORE
Go key-value store with Raft-based leader election, log replication, commit/apply, and fault-tolerant behavior for a 3-node cluster.

## Week 3 Status
- Raft and KV run on separate ports per node (client traffic and internal Raft RPC are split).
- Writes are leader-only and go through Raft `Propose` and commit.
- Followers reject writes with actionable leader KV hint (`409 not_leader`).
- Leader replicates using `nextIndex` and `matchIndex` with backtracking.
- Commit index advances only on majority and current-term safety rule.
- Committed entries are applied via apply loop on every node.
- Catch-up, failover, and no-majority safety are validated.

## Features
- Thread-safe in-memory store (`map[string]string` + `sync.RWMutex`).
- Store abstraction via `store.Store` interface.
- Client API:
  - `PUT /set`
  - `GET /get?key=...`
  - `DELETE /delete?key=...`
  - `GET /health`
- Internal Raft API:
  - `GET /raft/status`
  - `POST /raft/appendentries`
  - `POST /raft/requestvote`
- Leader election + periodic heartbeats.
- Log replication + follower catch-up.
- Majority commit + apply-on-commit only.
- Request IDs, request latency logging, panic recovery middleware.

## Configuration
Environment variables:
- `KVSTORE_PORT` (client API port, fallback `PORT`, default `8080`)
- `RAFT_PORT` (internal Raft port, default `KVSTORE_PORT + 1000`)
- `NODE_ID` (default `node-<port>`)
- `PEERS` (comma-separated Raft peer URLs)
- `PEER_IDS` (optional, comma-separated peer IDs aligned with `PEERS`)

Example:
```powershell
$env:NODE_ID="n1"
$env:KVSTORE_PORT="8081"
$env:RAFT_PORT="9081"
$env:PEERS="http://localhost:9082,http://localhost:9083"
$env:PEER_IDS="n2,n3"
go run .
```

## API Behavior
`PUT /set` body:
```json
{"key":"name","value":"luffy"}
```

Success (`200`):
```json
{"ok":true,"message":"stored","key":"name"}
```

Write to follower (`409`):
```json
{
  "ok": false,
  "error": "not_leader",
  "message": "not leader",
  "leaderId": "n3",
  "leader": "http://localhost:8082"
}
```

`GET /get?key=name` success (`200`):
```json
{"ok":true,"key":"name","value":"luffy"}
```

`GET /raft/status` includes:
- `state`, `term`, `leaderId`
- `logLen`, `lastLogIndex`, `lastLogTerm`
- `commitIndex`, `lastApplied`
- `peers`
- `nextIndex`, `matchIndex` (leader only)

## Project Structure
```text
.
|-- api/
|   |-- kv_handlers.go
|   |-- raft_handlers.go
|   |-- middleware.go
|   `-- router.go
|-- config/
|   `-- config.go
|-- raft/
|   |-- types.go
|   |-- rpc.go
|   |-- transport_http.go
|   |-- http.go
|   |-- node.go
|   |-- node_apply_test.go
|   |-- node_replication_test.go
|   `-- replication_scenarios_test.go
|-- scripts/
|   |-- run-node.ps1
|   |-- run-cluster.ps1
|   |-- watch-leader.ps1
|   |-- day6-kill-leader-test.ps1
|   |-- demo.ps1
|   `-- test-ah.ps1
|-- store/
|   |-- store.go
|   |-- provider.go
|   |-- memory.go
|   |-- errors.go
|   |-- memory_test.go
|   |-- concurrency_test.go
|   `-- bench_test.go
|-- utils/logger.go
`-- main.go
```

## Run Cluster
Start 3 nodes in separate terminals:
```powershell
.\scripts\run-cluster.ps1
```
Defaults used by that script:
- `n1`: KV `8081`, Raft `9081`
- `n2`: KV `8082`, Raft `9082`
- `n3`: KV `8083`, Raft `9083`

Check cluster health:
```powershell
Invoke-RestMethod http://localhost:8081/health
Invoke-RestMethod http://localhost:8082/health
Invoke-RestMethod http://localhost:8083/health
```

Check Raft internals:
```powershell
Invoke-RestMethod http://localhost:9081/raft/status
Invoke-RestMethod http://localhost:9082/raft/status
Invoke-RestMethod http://localhost:9083/raft/status
```

## Demo and Validation Scripts
- Week 3 demo flow:
```powershell
.\scripts\demo.ps1
```
- Leader kill/re-election validation:
```powershell
.\scripts\day6-kill-leader-test.ps1
```
- Full A-H checklist harness:
```powershell
.\scripts\test-ah.ps1
```

Latest A-H run status:
- `A1` PASS, `A2` PASS
- `B1` PASS, `B2` PASS
- `C1` PASS, `C2` PASS
- `D1` PASS
- `E1` PASS (optional conflict scenario)
- `F1` PASS, `F2` PASS
- `G1` PASS
- `H1` PASS

## Tests
Run all tests:
```powershell
go test ./...
```

Race detector:
```powershell
go test -race ./...
```

Store benchmarks:
```powershell
go test ./store -bench=. -benchmem -run ^$
```
