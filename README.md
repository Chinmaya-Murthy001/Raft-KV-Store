# RAFT-KV-STORE

RAFT-KV-STORE is a Go-based key-value store built to evolve from a single-node in-memory engine into a distributed Raft-backed system.

## Features
- In-memory key-value storage with `map[string]string`
- Thread-safe operations using `sync.RWMutex`
- Clean store abstraction via `store.Store` interface
- REST API:
  - `PUT /set`
  - `GET /get?key=...`
  - `DELETE /delete?key=...`
- Consistent JSON responses
- HTTP status mapping: `400`, `404`, `500`
- Request logging with latency and request ID
- Panic recovery middleware (returns JSON `500` instead of crashing)
- Configurable server port through environment variables
- Concurrency tests, stress tests, and benchmarks

## Project Structure
```text
.
|-- api/
|   |-- handlers.go
|   |-- middleware.go
|   `-- router.go
|-- config/
|   `-- config.go
|-- store/
|   |-- errors.go
|   |-- memory.go
|   |-- provider.go
|   |-- store.go
|   |-- memory_test.go
|   |-- concurrency_test.go
|   `-- bench_test.go
|-- utils/
|   `-- logger.go
`-- main.go
```

## Architecture (Current)
```text
Client
  |
  v
HTTP API (Router + Middleware + Handlers)
  |
  v
Store Interface (store.Store)
  |
  v
MemoryStore (in-memory map + RWMutex)
```

## Architecture (Future - Raft)
```text
            +-------------------+
Client ---> | API Node A        |
            | Handler -> Store  |
            +---------+---------+
                      |
                      v
              +---------------+
              | RaftStore     |
              | (leader/fwd)  |
              +--+---------+--+
                 |         |
                 v         v
          +-----------+ +-----------+
          | Node B    | | Node C    |
          | Replicas  | | Replicas  |
          +-----------+ +-----------+
```

## API Endpoints

### `PUT /set`
Stores or updates a key.

Request body:
```json
{"key":"name","value":"luffy"}
```

Success response (`200`):
```json
{"ok":true,"message":"stored","key":"name"}
```

### `GET /get?key=name`
Fetches a value by key.

Success response (`200`):
```json
{"ok":true,"key":"name","value":"luffy"}
```

### `DELETE /delete?key=name`
Deletes a key.

Success response (`200`):
```json
{"ok":true,"message":"deleted","key":"name"}
```

### Error Response Shape
All errors use:
```json
{"ok":false,"message":"..."}
```

Status mapping:
- `400` bad input (invalid JSON, empty key)
- `404` key not found
- `500` internal server error

## Run
Default port is `8080`.

Environment options:
- `KVSTORE_PORT`
- fallback: `PORT`

Examples:
```powershell
go run .
```

```powershell
$env:KVSTORE_PORT="8090"
go run .
```

## Test
Run all tests:
```powershell
go test ./...
```

Run race detector:
```powershell
go test -race ./...
```

Run benchmarks:
```powershell
go test ./store -bench=. -benchmem -run ^$
```

## Integration Test Notes

### PowerShell (`Invoke-RestMethod`)
Set:
```powershell
Invoke-RestMethod -Method Put -Uri "http://localhost:8080/set" `
  -ContentType "application/json" `
  -Body '{"key":"name","value":"luffy"}'
```

Get:
```powershell
Invoke-RestMethod -Method Get -Uri "http://localhost:8080/get?key=name"
```

Delete:
```powershell
Invoke-RestMethod -Method Delete -Uri "http://localhost:8080/delete?key=name"
```

### `curl.exe`
Set:
```powershell
curl.exe -X PUT "http://localhost:8080/set" ^
  -H "Content-Type: application/json" ^
  -d "{\"key\":\"name\",\"value\":\"luffy\"}"
```

Get:
```powershell
curl.exe "http://localhost:8080/get?key=name"
```

Delete:
```powershell
curl.exe -X DELETE "http://localhost:8080/delete?key=name"
```

## 2-Min Demo Checklist
1. Start server: `go run .`
2. `PUT /set` for a key/value
3. `GET /get` the same key
4. `DELETE /delete` the key
5. Show logs with request IDs and latency for each call

## Roadmap
### Week 2 (Planned)
- Multi-node cluster bootstrapping
- Raft leader election
- Log replication for writes
- Fault tolerance tests (node stop/restart)
- Replace `MemoryStore` wiring with `RaftStore` using same `store.Store` interface
