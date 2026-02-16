# Demo Smoke Test

## 1) Run Cluster

```powershell
.\scripts\run-cluster.ps1
```

This opens 3 PowerShell windows and starts:
- `n1` on `8081`
- `n2` on `8082`
- `n3` on `8083`

## 2) Check Health

```powershell
Invoke-RestMethod http://localhost:8081/health
Invoke-RestMethod http://localhost:8082/health
Invoke-RestMethod http://localhost:8083/health
```

Expected:
- Exactly one node has `"state": "leader"`.
- Leader has `"leaderId"` equal to `"id"`.
- Followers usually have `"lastHeartbeatAgoMs"` under `250`.

## 3) Kill Leader

Press `Ctrl+C` in the current leader terminal.

Expected in `~1-2s`:
- One remaining node becomes `"leader"`.
- Other running node stays `"follower"`.

## 4) Restart Old Leader

Run old leader again:

```powershell
.\scripts\run-node.ps1 -id nX -port 808Y -peers "..."
```

Use the same values it had before kill.

Expected:
- Restarted node becomes `"follower"`.
- `"leaderId"` points to the current leader.
- `"lastHeartbeatAgoMs"` drops to low values as heartbeats are received.

## 5) Optional Continuous Watch

```powershell
.\scripts\watch-leader.ps1
```
