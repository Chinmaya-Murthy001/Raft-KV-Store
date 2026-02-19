RAFT-KV-STORE

1st week

1) Core Storage Engine
- Implemented `MemoryStore` in `store/memory.go`.
- Added in-memory data container: `map[string]string`.
- Added concurrency safety with `sync.RWMutex`:
  - `RLock` for concurrent readers.
  - `Lock` for exclusive writes/deletes.
- Added clean error handling:
  - `ErrKeyEmpty` when key is empty.
  - `ErrKeyNotFound` when key does not exist.
- Added constructor `NewMemoryStore()` to initialize map safely.

2) Storage Abstraction
- Defined `Store` interface in `store/store.go` with:
  - `Set(key, value) error`
  - `Get(key) (string, error)`
  - `Delete(key) error`
- This allows API and higher layers to depend on behavior, not concrete type.

3) HTTP Server Layer
- Implemented REST handlers:
  - `PUT /set`
  - `GET /get?key=...`
  - `DELETE /delete?key=...`
- Implemented JSON request parsing and JSON responses.
- Added status code handling:
  - `400` bad request/input
  - `404` key not found
  - `500` internal error
- Wired store into API through interface dependency.

4) Logging and Operational Improvements
- Added reusable logger in `utils/logger.go`.
- Added request logging with:
  - HTTP method
  - path
  - response status
  - request latency
- Added request ID middleware and response header `X-Request-ID`.
- Added panic recovery middleware so panics return JSON `500` instead of crashing server.

5) Scalability-Oriented Refactor
- Added `config` layer in `config/config.go`.
  - Reads `KVSTORE_PORT`, fallback `PORT`, default `8080`.
- Refactored route setup into `api.NewRouter(handler)`.
- Simplified `main.go` to wiring only (config + logger + store + router + server).
- Added `store.NewDefaultStore()` provider function for future store swap.
  - Current default: `MemoryStore`.
  - Future target: `RaftStore` / replicated store without changing API code.

6) Testing and Validation
- Added unit test for basic behavior (`Set/Get/Delete`).
- Added concurrency stress tests:
  - mixed concurrent Set/Get/Delete
  - heavy concurrent read tests
  - deadlock timeout guard
- Added benchmarks for write and read performance.
- Ran tests with race detector to validate synchronization behavior.

7) Tooling and Environment Fixes
- Resolved package compile issue in `utils/logger.go` by adding package declaration.
- Installed and configured MinGW-w64 compiler for race detection on Windows.
- Updated Go C toolchain config (`CC`, `CXX`) and validated `go test -race ./...`.

8) Documentation
- Wrote a complete README with:
  - project overview
  - features
  - architecture diagrams (current + future raft)
  - API usage examples
  - run/test instructions
  - integration test notes
  - roadmap and demo checklist

2nd week

1) Cluster Config + Node Identity
- Extended `config.Config` with:
  - `NodeID string`
  - `Peers []string`
- Added env loading:
  - `NODE_ID` (default: `node-<port>`)
  - `PEERS` (comma-separated with space trimming)
- Added self-peer filtering so node does not keep itself in peer list.
- Kept empty peers valid for single-node mode.

2) Health and Debug Endpoint
- Added `GET /health`.
- Health now reports cluster + raft state:
  - `id`, `addr`, `peers`, `peersCount`
  - `state`, `term`, `leaderId`
  - `lastHeartbeatAgoMs`
- Used this as the primary live debugger for leader election and failover checks.

3) Router + Main Wiring Refactor
- Changed wiring to pass config and raft node into router.
- Updated main flow to:
  - load config
  - create logger and store
  - create raft node
  - run raft loop with context cancellation
  - start HTTP server with unified router

4) Raft Package Bootstrapping
- Added `raft/types.go` with state enum:
  - `Follower`, `Candidate`, `Leader`
  - `String()` helper for readable state output
- Added `raft/node.go` with core node fields:
  - ids, peers, term, vote info, leader id
  - election timers and mutex protection
  - logger and transport wiring

5) Raft RPC Types + HTTP Transport
- Added `raft/rpc.go`:
  - `RequestVoteRequest/Response`
  - `AppendEntriesRequest/Response` (heartbeat-only for now)
- Added `raft/transport_http.go`:
  - `SendRequestVote(...)`
  - `SendAppendEntries(...)`
  - JSON POST to peer raft endpoints with timeout

6) Vote Endpoint + Election Logic
- Added internal endpoint: `POST /raft/requestvote`.
- Implemented vote handling rules:
  - reject stale terms
  - step down on higher terms
  - grant vote if not voted (or already voted same candidate)
  - reset election timer on granted vote
- Implemented outbound vote requests to peers during election.
- Added vote counting and majority logic:
  - tracks `votesReceived` and `electionTerm`
  - ignores stale vote replies
  - steps down on higher term replies
  - becomes leader on majority

7) AppendEntries Endpoint + Heartbeats
- Added internal endpoint: `POST /raft/appendentries`.
- Implemented heartbeat handling:
  - reject stale term
  - step down on higher term
  - accept leader on current term
  - reset election timer
  - track `lastHeartbeat` timestamp
- Added leader heartbeat loop:
  - periodic AppendEntries to peers
  - concurrent sends
  - step down if higher term is observed

8) Stability and Safety Improvements
- Tuned timeouts for Windows/local stability:
  - heartbeat: `100ms`
  - election timeout: random `600-1200ms`
- Added term-stamped heartbeat loops to prevent ghost heartbeats:
  - heartbeat loop exits if state changes or term changes.
- Ensured old election responses cannot corrupt new term/state.

9) API File Separation (Week 3 friendly)
- Split handlers for cleaner architecture:
  - `api/kv_handlers.go` -> client API (`/set`, `/get`, `/delete`)
  - `api/raft_handlers.go` -> raft internals + health
- Kept `api/router.go` focused on route wiring.

10) Scripts and Demo Automation
- Added cluster bootstrap scripts:
  - `scripts/run-node.ps1`
  - `scripts/run-cluster.ps1`
- Added live watcher:
  - `scripts/watch-leader.ps1`
- Added automated failover test:
  - `scripts/day6-kill-leader-test.ps1`
  - validates leader election, leader kill, re-election, and old leader rejoin behavior

11) Logging Readability Upgrade
- Standardized raft log prefixes for demos/debug:
  - `ELECTION start term=X`
  - `VOTE from peer votes=a/b term=X`
  - `LEADER term=X`
  - `HB from leader term=X`
  - stepdown logs with higher-term reason

12) Documentation Upgrade
- Added README Week 2 demo section with:
  - one-command cluster start
  - health checks
  - failover steps
  - Day 6 verification script
- Added `docs/demo.md` smoke runbook:
  - run cluster
  - kill leader
  - restart old leader
  - expected outputs

13) Validation Done
- Repeatedly validated with `go test ./...` after each major change.
- Ran live 3-node checks:
  - stable single leader
  - successful re-election on leader kill
  - restarted old leader rejoining as follower
  - healthy heartbeat visibility via `/health`

3rd week

1) Cluster-Aware Config Expansion
- Extended `config/config.go` for multi-node Raft wiring:
  - `NODE_ID`
  - `RAFT_PORT`
  - `PEERS` (comma-separated raft peer URLs)
- Added cluster address-book fields:
  - `NodeKV map[string]string`
  - `NodeRaft map[string]string`
- Added URL helpers:
  - `KVURL()`
  - `RaftURL()`
- Added optional `PEER_IDS` support to map peer IDs to addresses cleanly.

2) Raft Package Completion (Week 3 model)
- Added/expanded `raft/types.go` and `raft/node.go` with core Raft state:
  - persistent-ish: `currentTerm`, `votedFor`, `log []LogEntry`
  - volatile: `commitIndex`, `lastApplied`
  - leader-only: `nextIndex`, `matchIndex`
- Log model includes:
  - `Command { Op, Key, Value }`
  - `LogEntry { Term, Index, Command }`
- Added `Start()` and `Status()` behavior for runtime and observability.

3) Raft HTTP Endpoints
- Added `raft/http.go` routes:
  - `GET /raft/status`
  - `POST /raft/appendentries`
  - `POST /raft/requestvote`
- Status now reports:
  - `node_id`, `state`, `term`
  - `commitIndex`, `lastApplied`
  - `logLen`, `lastLogIndex`, `lastLogTerm`
  - `leaderId`, `leaderAddr`, `peers`
  - `nextIndex` and `matchIndex` for leader

4) Dual-Server Runtime (KV + Raft)
- Updated `main.go` to run:
  - KV client API on `KVSTORE_PORT`
  - Raft internal RPC on `RAFT_PORT` (goroutine)
- Bound raft node to store + client address + cluster address book:
  - `BindStore(...)`
  - `BindClientAddr(...)`
  - `BindAddressBook(...)`

5) Leader Heartbeats + Election Stability
- Implemented periodic leader heartbeats (~100ms) using AppendEntries.
- Followers reset election timers on valid AppendEntries.
- Reduced follower candidate spam while leader is alive.
- Implemented leader step-down on receiving higher term responses.

6) Replication Catch-Up Logic
- Leader now sends real log entries (not only empty heartbeats):
  - per-peer `start := nextIndex[peer]`
  - `PrevLogIndex = start-1`, `PrevLogTerm` from local log
- On AppendEntries response:
  - success: update `matchIndex` + `nextIndex`
  - fail: back off `nextIndex` and retry next cycle
- Implemented prev-log consistency rejection on followers.
- Supports lagging follower catch-up and conflict-tail overwrite behavior.

7) Commit Advancement (Majority + Safety Rule)
- Added leader commit advancement using `matchIndex[]` majority counting.
- Enforced Raft safety condition:
  - commit only if `log[N].Term == currentTerm`
- Leader propagates `LeaderCommit` in AppendEntries.
- Followers update `commitIndex = min(LeaderCommit, lastLogIndex)`.

8) Apply Pipeline (Committed Entries Only)
- Added apply channel and apply loop:
  - `applyCh chan LogEntry`
  - committed entries queued and applied outside long lock holds
- Added apply adapter:
  - `set` -> `store.Set(...)`
  - `delete` -> `store.Delete(...)`
- Added apply logs:
  - `[apply] index=... op=... key=... value=...`
- Ensured apply occurs only when `lastApplied < commitIndex`.

9) Write Path Through Raft (No Local Follower Writes)
- Added leader guard on write endpoints (`/set`, `/delete`):
  - followers return HTTP `409`
  - JSON includes actionable leader hint
- Implemented `raft.Node.Propose(cmd)`:
  - leader-only proposal acceptance
  - append entry to log
  - trigger immediate replication
  - wait for commit or timeout
- Added minimal waiter map for sync proposals:
  - `waitCh map[int]chan struct{}` keyed by log index
- KV mutations now happen via Raft apply loop, not direct write handler mutation.

10) Leader Hint Usability Fix
- Corrected not-leader response to return leader KV API URL (client-facing), not raft port.
- Added structured response fields:
  - `ok`, `error`, `message`, `leaderId`, `leader`
- Leader identity is learned from AppendEntries and resolved via address book.

11) Demo/Test Harness Automation
- Added/updated PowerShell automation:
  - `scripts/demo.ps1` for cluster demo flow
  - `scripts/test-ah.ps1` for full A-H checklist execution
- Included not-leader retry flow in harness behavior.

12) Validation in This Session
- Ran `go test ./...` during Week 3 changes.
- Executed full distributed checklist A-H on 3-node cluster (`KV 8080-8082`, `Raft 9080-9082`):
  - A1 exactly one leader: PASS
  - A2 no follower election spam: PASS
  - B1 follower write returns not leader + KV leader hint: PASS
  - B2 leader write commits/applies: PASS
  - C1 read-your-write on all nodes: PASS
  - C2 ordered sequential writes: PASS
  - D1 lagging follower catch-up: PASS
  - E1 conflicting uncommitted tail resolution (optional scenario): PASS
  - F1 commit with one follower down: PASS
  - F2 no majority -> no commit: PASS
  - G1 leader crash + recovery continuity: PASS
  - H1 exactly-once apply per index/node: PASS
