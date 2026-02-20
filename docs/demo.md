# Week 3 Demo Runbook

This runbook matches the current Week 3 Raft behavior:
- leader election + heartbeats
- leader-only writes
- replication and follower catch-up
- leader crash and re-election
- no-majority no-commit safety

## Option 1: Full Automated A-H Validation
Run:
```powershell
.\scripts\test-ah.ps1
```

What it covers:
- A: leader stability
- B: not-leader behavior + leader commit/apply
- C: replication/read-your-writes
- D: lagging follower catch-up
- E: conflicting uncommitted tail overwrite scenario
- F: majority rules and no-majority safety
- G: leader crash and recovery
- H: exactly-once apply per index per node

## Option 2: Week 3 Demo Script
Run:
```powershell
.\scripts\demo.ps1
```

This demonstrates:
- leader detection
- write with follower retry to leader
- follower down and catch-up after restart
- leader crash and re-election
- no-majority write timeout behavior

## Option 3: Manual Smoke Demo

### 1) Start cluster
```powershell
.\scripts\run-cluster.ps1
```

Defaults:
- n1: KV `8081`, Raft `9081`
- n2: KV `8082`, Raft `9082`
- n3: KV `8083`, Raft `9083`

### 2) Confirm single leader
```powershell
Invoke-RestMethod http://localhost:9081/raft/status
Invoke-RestMethod http://localhost:9082/raft/status
Invoke-RestMethod http://localhost:9083/raft/status
```
Expected:
- exactly one node has `"state": "leader"`
- others are `"follower"`
- terms are same/compatible

### 3) Verify not-leader write rejection
Pick a follower KV port and send write:
```powershell
Invoke-WebRequest -Method Put -Uri "http://localhost:8081/set" -ContentType "application/json" -Body '{"key":"t1","value":"111"}'
```
Expected:
- HTTP `409`
- JSON contains `error: "not_leader"`
- `leader` points to leader KV URL (not raft port)

### 4) Write to leader and read from all nodes
```powershell
Invoke-RestMethod -Method Put -Uri "http://localhost:8082/set" -ContentType "application/json" -Body '{"key":"t2","value":"222"}'
Invoke-RestMethod "http://localhost:8081/get?key=t2"
Invoke-RestMethod "http://localhost:8082/get?key=t2"
Invoke-RestMethod "http://localhost:8083/get?key=t2"
```
Expected:
- all nodes return `t2=222`

### 5) Follower catch-up
- Stop one follower process.
- Write a few keys to leader.
- Restart stopped follower.
- Read those keys from restarted follower.

Expected:
- follower catches up and applies committed entries.

### 6) Leader crash and re-election
- Stop leader process.
- Wait for new leader.
- Write new key to new leader.
- Read old and new keys from all nodes.

Expected:
- committed data preserved
- new writes continue under new leader

### 7) No-majority safety
- In a 3-node cluster, stop two nodes.
- Send write to remaining node.

Expected:
- write times out/fails to commit
- value is not visible as committed data

## Useful Watch Command
```powershell
.\scripts\watch-leader.ps1
```

# Week 4 Demo

## One-command validation
Run:
```powershell
.\scripts\test-week4.ps1
```

This runs:
- Week 3 regression (`A-H`)
- `P1` follower crash/restart catch-up
- `P2` leader crash/restart rejoin as follower
- `P3` uncommitted write never appears after crash/restart
- `S1` snapshot survives restart (if snapshot script is present)

## Manual Week 4 flow
### 1) Start cluster
```powershell
.\scripts\run-cluster.ps1
```

### 2) Write initial keys
```powershell
Invoke-RestMethod -Method Put -Uri "http://localhost:8081/set" -ContentType "application/json" -Body '{"key":"w4-a","value":"1"}'
Invoke-RestMethod -Method Put -Uri "http://localhost:8081/set" -ContentType "application/json" -Body '{"key":"w4-b","value":"2"}'
```

### 3) Follower restart catch-up
- Stop one follower.
- Write more keys to leader.
- Restart follower.
- Verify follower reads all keys.

### 4) Leader crash and old leader rejoin
- Stop current leader.
- Wait for new leader.
- Write another key to new leader.
- Restart old leader.
- Verify old leader rejoins as follower and all nodes read all keys.

### 5) Uncommitted write safety
- Stop both followers.
- Send write to isolated leader (expect timeout/failure).
- Stop isolated leader.
- Restart all nodes.
- Verify the uncommitted key does not exist on any node.

### 6) Snapshot restart check
- Write many keys (enough to cross `RAFT_SNAPSHOT_THRESHOLD`).
- Confirm `snapshot.json` exists in node data dir.
- Restart that node.
- Verify keys are still readable.
