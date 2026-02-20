param(
    [int]$StartupTimeoutSec = 30,
    [int]$CatchupTimeoutSec = 60,
    [int]$Writes = 60,
    [int]$SnapshotThreshold = 20
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$runID = Get-Date -Format "yyyyMMdd-HHmmss"
$dataRoot = Join-Path $repo ("data\day6-s1-" + $runID)

$nodeCfg = [ordered]@{
    n1 = [ordered]@{
        KV      = 58081
        Raft    = 59081
        Peers   = "http://localhost:59082,http://localhost:59083"
        PeerIDs = "n2,n3"
    }
    n2 = [ordered]@{
        KV      = 58082
        Raft    = 59082
        Peers   = "http://localhost:59081,http://localhost:59083"
        PeerIDs = "n1,n3"
    }
    n3 = [ordered]@{
        KV      = 58083
        Raft    = 59083
        Peers   = "http://localhost:59081,http://localhost:59082"
        PeerIDs = "n1,n2"
    }
}
$nodeIDs = @("n1", "n2", "n3")
$jobs = @{}

function Wait-Until([scriptblock]$Condition, [int]$TimeoutSec, [int]$SleepMs = 250) {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) {
            return $true
        }
        Start-Sleep -Milliseconds $SleepMs
    }
    return $false
}

function Start-Node([string]$id) {
    if ($jobs.ContainsKey($id)) {
        return
    }
    $cfg = $nodeCfg[$id]
    $job = Start-Job -ScriptBlock {
        param($workdir, $nodeID, $kvPort, $raftPort, $peers, $peerIDs, $raftDataRoot, $snapThreshold)
        Set-Location $workdir
        $env:NODE_ID = $nodeID
        $env:KVSTORE_PORT = "$kvPort"
        $env:RAFT_PORT = "$raftPort"
        $env:PEERS = $peers
        $env:PEER_IDS = $peerIDs
        $env:RAFT_DATA_DIR = $raftDataRoot
        $env:RAFT_SNAPSHOT_THRESHOLD = "$snapThreshold"
        go run .
    } -ArgumentList $repo, $id, $cfg.KV, $cfg.Raft, $cfg.Peers, $cfg.PeerIDs, $dataRoot, $SnapshotThreshold
    $jobs[$id] = $job
}

function Stop-Node([string]$id) {
    if (-not $jobs.ContainsKey($id)) {
        return
    }
    $job = $jobs[$id]
    Stop-Job $job -ErrorAction SilentlyContinue | Out-Null
    Receive-Job $job -ErrorAction SilentlyContinue | Out-Null
    Remove-Job $job -ErrorAction SilentlyContinue | Out-Null
    $jobs.Remove($id)
}

function Stop-All {
    foreach ($id in @($jobs.Keys)) {
        Stop-Node $id
    }
}

function Get-Health([string]$id) {
    $kvPort = $nodeCfg[$id].KV
    return Invoke-RestMethod -Method Get -Uri "http://localhost:$kvPort/health" -TimeoutSec 1
}

function Get-RaftStatus([string]$id) {
    $raftPort = $nodeCfg[$id].Raft
    return Invoke-RestMethod -Method Get -Uri "http://localhost:$raftPort/raft/status" -TimeoutSec 2
}

function Wait-StableLeader(
    [string[]]$ids = $nodeIDs,
    [int]$TimeoutSec = 20,
    [int]$ConsecutiveChecks = 5,
    [int]$SampleMs = 250
) {
    $leaderRef = [ref] ""
    $stableCountRef = [ref] 0
    $ok = Wait-Until {
        $leaders = @()
        foreach ($id in $ids) {
            if (-not $jobs.ContainsKey($id)) {
                continue
            }
            try {
                $h = Get-Health $id
                if ([string]$h.state -eq "leader") {
                    $leaders += $id
                }
            }
            catch {
            }
        }
        if ($leaders.Count -ne 1) {
            $leaderRef.Value = ""
            $stableCountRef.Value = 0
            return $false
        }

        $leader = $leaders[0]
        if ($leaderRef.Value -eq $leader) {
            $stableCountRef.Value++
        }
        else {
            $leaderRef.Value = $leader
            $stableCountRef.Value = 1
        }
        return ($stableCountRef.Value -ge $ConsecutiveChecks)
    } -TimeoutSec $TimeoutSec -SleepMs $SampleMs

    if (-not $ok) {
        return $null
    }
    return $leaderRef.Value
}

function Write-WithRetry([string]$startNodeId, [string]$key, [string]$value) {
    $body = @{ key = $key; value = $value } | ConvertTo-Json -Compress
    $uri = "http://localhost:$($nodeCfg[$startNodeId].KV)/set"
    try {
        $null = Invoke-RestMethod -Method Put -Uri $uri -ContentType "application/json" -Body $body -TimeoutSec 5
        return
    }
    catch {
        $status = -1
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $status = [int]$_.Exception.Response.StatusCode
        }
        if ($status -ne 409) {
            throw
        }
        if ([string]::IsNullOrWhiteSpace($_.ErrorDetails.Message)) {
            throw
        }
        $err = $_.ErrorDetails.Message | ConvertFrom-Json
        if ($null -eq $err -or [string]::IsNullOrWhiteSpace([string]$err.leader)) {
            throw
        }
        $leaderURI = "$($err.leader.TrimEnd('/'))/set"
        $null = Invoke-RestMethod -Method Put -Uri $leaderURI -ContentType "application/json" -Body $body -TimeoutSec 5
    }
}

function Node-HasValue([string]$nodeId, [string]$key, [string]$expected) {
    $uri = "http://localhost:$($nodeCfg[$nodeId].KV)/get?key=$key"
    try {
        $r = Invoke-RestMethod -Method Get -Uri $uri -TimeoutSec 3
        return ($null -ne $r -and [string]$r.value -eq $expected)
    }
    catch {
        return $false
    }
}

try {
    Write-Host "S1: snapshot survives restart and KV is correct"
    Write-Host "RAFT_DATA_DIR root: $dataRoot"
    Write-Host "Snapshot threshold: $SnapshotThreshold"

    foreach ($id in $nodeIDs) {
        Start-Node $id
    }

    $leader = Wait-StableLeader -ids $nodeIDs -TimeoutSec $StartupTimeoutSec
    if ([string]::IsNullOrWhiteSpace($leader)) {
        throw "Failed to elect stable leader."
    }
    Write-Host "Leader: $leader"

    for ($i = 1; $i -le $Writes; $i++) {
        Write-WithRetry -startNodeId $leader -key ("s" + $i) -value ("v" + $i)
    }
    Write-Host "Wrote $Writes keys."

    $snapshotPath = Join-Path $dataRoot (Join-Path $leader "snapshot.json")
    $snapshotReady = Wait-Until { Test-Path $snapshotPath } -TimeoutSec $CatchupTimeoutSec -SleepMs 300
    if (-not $snapshotReady) {
        throw "Snapshot file not created on leader: $snapshotPath"
    }
    Write-Host "Snapshot created: $snapshotPath"

    $preRestartStatus = Get-RaftStatus $leader
    $preRestartLogLen = [int]$preRestartStatus.logLen

    Write-Host "Stopping snapshot node $leader..."
    Stop-Node $leader
    Start-Sleep -Milliseconds 800

    Write-Host "Restarting node $leader..."
    Start-Node $leader

    $null = Wait-StableLeader -ids $nodeIDs -TimeoutSec $StartupTimeoutSec

    $caughtUp = Wait-Until {
        for ($i = 1; $i -le $Writes; $i++) {
            if (-not (Node-HasValue -nodeId $leader -key ("s" + $i) -expected ("v" + $i))) {
                return $false
            }
        }
        return $true
    } -TimeoutSec $CatchupTimeoutSec -SleepMs 300

    if (-not $caughtUp) {
        throw "Restarted node $leader did not serve expected keys."
    }

    $postRestartStatus = Get-RaftStatus $leader
    $postRestartLogLen = [int]$postRestartStatus.logLen

    Write-Host ("PASS: node {0} restarted from snapshot and serves all keys." -f $leader)
    Write-Host ("Log length before restart: {0}; after restart: {1}" -f $preRestartLogLen, $postRestartLogLen)
}
finally {
    Stop-All
}
