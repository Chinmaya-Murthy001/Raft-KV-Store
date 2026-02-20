param(
    [int]$StartupTimeoutSec = 25,
    [int]$CatchupTimeoutSec = 40
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$nodeCfg = [ordered]@{
    n1 = [ordered]@{
        KV      = 28081
        Raft    = 29081
        Peers   = "http://localhost:29082,http://localhost:29083"
        PeerIDs = "n2,n3"
    }
    n2 = [ordered]@{
        KV      = 28082
        Raft    = 29082
        Peers   = "http://localhost:29081,http://localhost:29083"
        PeerIDs = "n1,n3"
    }
    n3 = [ordered]@{
        KV      = 28083
        Raft    = 29083
        Peers   = "http://localhost:29081,http://localhost:29082"
        PeerIDs = "n1,n2"
    }
}
$nodeIDs = @("n1", "n2", "n3")
$jobs = @{}

function Start-Node([string]$id) {
    if ($jobs.ContainsKey($id)) {
        return
    }

    $cfg = $nodeCfg[$id]
    $job = Start-Job -ScriptBlock {
        param($workdir, $nodeID, $kvPort, $raftPort, $peers, $peerIDs)
        Set-Location $workdir
        $env:NODE_ID = $nodeID
        $env:KVSTORE_PORT = "$kvPort"
        $env:RAFT_PORT = "$raftPort"
        $env:PEERS = $peers
        $env:PEER_IDS = $peerIDs
        go run .
    } -ArgumentList $repo, $id, $cfg.KV, $cfg.Raft, $cfg.Peers, $cfg.PeerIDs

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

function Get-Health([string]$id) {
    $kvPort = $nodeCfg[$id].KV
    return Invoke-RestMethod -Method Get -Uri "http://localhost:$kvPort/health" -TimeoutSec 1
}

function Find-Leader {
    $leaders = @()
    foreach ($id in $nodeIDs) {
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

    if ($leaders.Count -eq 1) {
        return $leaders[0]
    }
    return $null
}

function Wait-OneLeader([int]$TimeoutSec) {
    $leaderRef = [ref] ""
    $ok = Wait-Until {
        $leader = Find-Leader
        if ([string]::IsNullOrWhiteSpace($leader)) {
            return $false
        }
        $leaderRef.Value = $leader
        return $true
    } -TimeoutSec $TimeoutSec

    if (-not $ok) {
        throw "Failed to stabilize to exactly one leader."
    }
    return $leaderRef.Value
}

function Write-WithLeaderRetry([string]$startNodeId, [string]$key, [string]$value) {
    $body = @{ key = $key; value = $value } | ConvertTo-Json -Compress
    $uri = "http://localhost:$($nodeCfg[$startNodeId].KV)/set"
    try {
        $null = Invoke-RestMethod -Method Put -Uri $uri -ContentType "application/json" -Body $body -TimeoutSec 4
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
        if ($null -eq $err -or [string]$err.error -ne "not_leader" -or [string]::IsNullOrWhiteSpace([string]$err.leader)) {
            throw
        }

        $leaderURI = "$($err.leader.TrimEnd('/'))/set"
        $null = Invoke-RestMethod -Method Put -Uri $leaderURI -ContentType "application/json" -Body $body -TimeoutSec 4
    }
}

function Node-HasValue([string]$nodeId, [string]$key, [string]$expected) {
    $uri = "http://localhost:$($nodeCfg[$nodeId].KV)/get?key=$key"
    try {
        $r = Invoke-RestMethod -Method Get -Uri $uri -TimeoutSec 2
        return ($null -ne $r -and [string]$r.value -eq $expected)
    }
    catch {
        return $false
    }
}

try {
    Write-Host "Starting cluster (n1,n2,n3)..."
    foreach ($id in $nodeIDs) {
        Start-Node $id
    }

    $leader = Wait-OneLeader -TimeoutSec $StartupTimeoutSec
    Write-Host "Leader elected: $leader"

    $followerToKill = @($nodeIDs | Where-Object { $_ -ne $leader })[0]
    Write-Host "Follower selected for restart test: $followerToKill"

    Write-Host "Writing first 5 keys..."
    for ($i = 1; $i -le 5; $i++) {
        Write-WithLeaderRetry -startNodeId $leader -key ("k" + $i) -value ("v" + $i)
    }

    Write-Host "Stopping follower $followerToKill..."
    Stop-Node $followerToKill
    Start-Sleep -Milliseconds 800

    Write-Host "Writing next 5 keys while follower is down..."
    for ($i = 6; $i -le 10; $i++) {
        Write-WithLeaderRetry -startNodeId $leader -key ("k" + $i) -value ("v" + $i)
    }

    Write-Host "Restarting follower $followerToKill with same data dir..."
    Start-Node $followerToKill

    Write-Host "Waiting for restarted follower to catch up all 10 keys..."
    $caughtUp = Wait-Until {
        for ($i = 1; $i -le 10; $i++) {
            if (-not (Node-HasValue -nodeId $followerToKill -key ("k" + $i) -expected ("v" + $i))) {
                return $false
            }
        }
        return $true
    } -TimeoutSec $CatchupTimeoutSec -SleepMs 300

    if (-not $caughtUp) {
        throw "Follower $followerToKill did not catch up keys k1..k10 within timeout."
    }

    Write-Host "PASS: follower $followerToKill caught up and reads all 10 keys."
}
finally {
    Stop-All
}
