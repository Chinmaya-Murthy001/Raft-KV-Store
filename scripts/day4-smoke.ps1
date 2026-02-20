param(
    [int]$StartupTimeoutSec = 25,
    [int]$ReelectionTimeoutSec = 20,
    [int]$CatchupTimeoutSec = 40
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$nodeCfg = [ordered]@{
    n1 = [ordered]@{
        KV      = 38081
        Raft    = 39081
        Peers   = "http://localhost:39082,http://localhost:39083"
        PeerIDs = "n2,n3"
    }
    n2 = [ordered]@{
        KV      = 38082
        Raft    = 39082
        Peers   = "http://localhost:39081,http://localhost:39083"
        PeerIDs = "n1,n3"
    }
    n3 = [ordered]@{
        KV      = 38083
        Raft    = 39083
        Peers   = "http://localhost:39081,http://localhost:39082"
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

function Wait-StableLeader(
    [string[]]$ids,
    [int]$TimeoutSec,
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

function Node-HasKeyValue([string]$nodeId, [string]$key, [string]$expected) {
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
    Write-Host "Starting 3-node cluster..."
    foreach ($id in $nodeIDs) {
        Start-Node $id
    }

    $leader1 = Wait-StableLeader -ids $nodeIDs -TimeoutSec $StartupTimeoutSec
    if ([string]::IsNullOrWhiteSpace($leader1)) {
        throw "Failed to elect initial stable leader."
    }
    Write-Host "Initial leader: $leader1"

    Write-Host "Writing x=10..."
    Write-WithRetry -startNodeId $leader1 -key "x" -value "10"

    Write-Host "Killing leader $leader1..."
    Stop-Node $leader1

    $remaining = @($nodeIDs | Where-Object { $_ -ne $leader1 })
    $leader2 = Wait-StableLeader -ids $remaining -TimeoutSec $ReelectionTimeoutSec
    if ([string]::IsNullOrWhiteSpace($leader2)) {
        throw "Failed to elect new stable leader after kill."
    }
    Write-Host "New leader: $leader2"

    Write-Host "Writing y=20 on new leader..."
    Write-WithRetry -startNodeId $leader2 -key "y" -value "20"

    Write-Host "Restarting old leader $leader1..."
    Start-Node $leader1

    $caughtUp = Wait-Until {
        foreach ($id in $nodeIDs) {
            if (-not $jobs.ContainsKey($id)) {
                return $false
            }
            if (-not (Node-HasKeyValue -nodeId $id -key "x" -expected "10")) {
                return $false
            }
            if (-not (Node-HasKeyValue -nodeId $id -key "y" -expected "20")) {
                return $false
            }
        }
        return $true
    } -TimeoutSec $CatchupTimeoutSec -SleepMs 300

    if (-not $caughtUp) {
        throw "Cluster did not converge x=10,y=20 on all nodes in time."
    }

    $oldLeaderHealth = Get-Health $leader1
    if ([string]$oldLeaderHealth.state -ne "follower") {
        throw "Restarted old leader state=$($oldLeaderHealth.state), expected follower."
    }
    if ([string]::IsNullOrWhiteSpace([string]$oldLeaderHealth.leaderId)) {
        throw "Restarted old leader does not report current leader."
    }

    Write-Host "PASS: Day 4 smoke flow succeeded. Old leader rejoined as follower and all nodes read x=10,y=20."
}
finally {
    Stop-All
}
