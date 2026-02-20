param(
    [int]$StartupTimeoutSec = 25,
    [int]$ReelectionTimeoutSec = 30
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$runID = Get-Date -Format "yyyyMMdd-HHmmss"
$dataRoot = Join-Path $repo ("data\week4-p3-" + $runID)

$nodeCfg = [ordered]@{
    n1 = [ordered]@{
        KV      = 48081
        Raft    = 49081
        Peers   = "http://localhost:49082,http://localhost:49083"
        PeerIDs = "n2,n3"
    }
    n2 = [ordered]@{
        KV      = 48082
        Raft    = 49082
        Peers   = "http://localhost:49081,http://localhost:49083"
        PeerIDs = "n1,n3"
    }
    n3 = [ordered]@{
        KV      = 48083
        Raft    = 49083
        Peers   = "http://localhost:49081,http://localhost:49082"
        PeerIDs = "n1,n2"
    }
}
$nodeIDs = @("n1", "n2", "n3")
$jobs = @{}

function To-StatusCode($value) {
    if ($null -eq $value) {
        return -1
    }
    if ($value -is [int]) {
        return $value
    }
    if ($value -is [System.Net.HttpStatusCode]) {
        return [int]$value
    }
    $n = 0
    if ([int]::TryParse([string]$value, [ref]$n)) {
        return $n
    }
    return -1
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

function Start-Node([string]$id) {
    if ($jobs.ContainsKey($id)) {
        return
    }

    $cfg = $nodeCfg[$id]
    $job = Start-Job -ScriptBlock {
        param($workdir, $nodeID, $kvPort, $raftPort, $peers, $peerIDs, $raftDataRoot)
        Set-Location $workdir
        $env:NODE_ID = $nodeID
        $env:KVSTORE_PORT = "$kvPort"
        $env:RAFT_PORT = "$raftPort"
        $env:PEERS = $peers
        $env:PEER_IDS = $peerIDs
        $env:RAFT_DATA_DIR = $raftDataRoot
        go run .
    } -ArgumentList $repo, $id, $cfg.KV, $cfg.Raft, $cfg.Peers, $cfg.PeerIDs, $dataRoot

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

function Invoke-Set([string]$nodeId, [string]$key, [string]$value) {
    $uri = "http://localhost:$($nodeCfg[$nodeId].KV)/set"
    $body = @{ key = $key; value = $value } | ConvertTo-Json -Compress
    try {
        $resp = Invoke-WebRequest -Method Put -Uri $uri -ContentType "application/json" -Body $body -TimeoutSec 5 -UseBasicParsing
        $statusCode = To-StatusCode $resp.StatusCode
        $json = $null
        try {
            $json = $resp.Content | ConvertFrom-Json
        }
        catch {
        }
        return [pscustomobject]@{
            Status = $statusCode
            Json   = $json
            Body   = $resp.Content
        }
    }
    catch {
        $status = -1
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $status = To-StatusCode $_.Exception.Response.StatusCode
        }
        $content = $_.ErrorDetails.Message
        $json = $null
        if (-not [string]::IsNullOrWhiteSpace($content)) {
            try {
                $json = $content | ConvertFrom-Json
            }
            catch {
            }
        }
        return [pscustomobject]@{
            Status = $status
            Json   = $json
            Body   = $content
        }
    }
}

function Invoke-Get([string]$nodeId, [string]$key) {
    $uri = "http://localhost:$($nodeCfg[$nodeId].KV)/get?key=$key"
    try {
        $resp = Invoke-WebRequest -Method Get -Uri $uri -TimeoutSec 3 -UseBasicParsing
        return [pscustomobject]@{
            Status = To-StatusCode $resp.StatusCode
            Body   = $resp.Content
        }
    }
    catch {
        $status = -1
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $status = To-StatusCode $_.Exception.Response.StatusCode
        }
        return [pscustomobject]@{
            Status = $status
            Body   = $_.ErrorDetails.Message
        }
    }
}

try {
    Write-Host "P3: crash after append before commit -> must never apply"
    Write-Host "Using RAFT_DATA_DIR root: $dataRoot"

    foreach ($id in $nodeIDs) {
        Start-Node $id
    }

    $leader = Wait-StableLeader -ids $nodeIDs -TimeoutSec $StartupTimeoutSec
    if ([string]::IsNullOrWhiteSpace($leader)) {
        throw "Failed to elect initial stable leader."
    }
    Write-Host "Leader: $leader"

    $followers = @($nodeIDs | Where-Object { $_ -ne $leader })
    $f1 = $followers[0]
    $f2 = $followers[1]

    Write-Host "Stopping followers: $f1, $f2"
    Stop-Node $f1
    Stop-Node $f2
    Start-Sleep -Milliseconds 700

    Write-Host "Sending uncommittable write z=999 to isolated leader..."
    $writeResp = Invoke-Set -nodeId $leader -key "z" -value "999"
    Write-Host ("Leader write status: {0}" -f $writeResp.Status)
    if ($writeResp.Status -eq 200) {
        throw "Unexpected 200 for isolated leader write; expected timeout/failure."
    }

    Write-Host "Crashing leader: $leader"
    Stop-Node $leader

    Write-Host "Restarting all nodes with same persistent data dirs..."
    foreach ($id in $nodeIDs) {
        Start-Node $id
    }

    $newLeader = Wait-StableLeader -ids $nodeIDs -TimeoutSec $ReelectionTimeoutSec
    if ([string]::IsNullOrWhiteSpace($newLeader)) {
        throw "Failed to elect stable leader after restart."
    }
    Write-Host "Post-restart leader: $newLeader"

    $absentEverywhere = Wait-Until {
        foreach ($id in $nodeIDs) {
            $r = Invoke-Get -nodeId $id -key "z"
            if ($r.Status -eq 200) {
                return $false
            }
        }
        return $true
    } -TimeoutSec 15 -SleepMs 300

    if (-not $absentEverywhere) {
        throw "Key z appeared on at least one node; uncommitted entry was applied."
    }

    Write-Host "PASS: z is absent on all nodes after restart."
}
finally {
    Stop-All
}
