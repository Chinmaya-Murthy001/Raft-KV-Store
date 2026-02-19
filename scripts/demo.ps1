param(
    [int]$BootTimeoutSec = 20,
    [int]$ElectionTimeoutSec = 12,
    [int]$CatchupTimeoutSec = 20
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$nodeCfg = @{
    n1 = @{ KV = 8081; Raft = 9081; Peers = "http://localhost:9082,http://localhost:9083" }
    n2 = @{ KV = 8082; Raft = 9082; Peers = "http://localhost:9081,http://localhost:9083" }
    n3 = @{ KV = 8083; Raft = 9083; Peers = "http://localhost:9081,http://localhost:9082" }
}
$allNodes = @("n1", "n2", "n3")
$jobs = @{}

function Start-Node([string]$nodeId) {
    $cfg = $nodeCfg[$nodeId]
    $job = Start-Job -ScriptBlock {
        param($workdir, $id, $kvPort, $raftPort, $peers)
        Set-Location $workdir
        $env:NODE_ID = $id
        $env:KVSTORE_PORT = "$kvPort"
        $env:RAFT_PORT = "$raftPort"
        $env:PEERS = $peers
        go run .
    } -ArgumentList $repo, $nodeId, $cfg.KV, $cfg.Raft, $cfg.Peers

    $jobs[$nodeId] = $job
}

function Stop-Node([string]$nodeId) {
    if (-not $jobs.ContainsKey($nodeId)) {
        return
    }

    $job = $jobs[$nodeId]
    Stop-Job $job -ErrorAction SilentlyContinue | Out-Null
    Receive-Job $job -ErrorAction SilentlyContinue | Out-Null
    Remove-Job $job -ErrorAction SilentlyContinue | Out-Null
    $jobs.Remove($nodeId)
}

function Stop-AllNodes {
    foreach ($id in @($jobs.Keys)) {
        Stop-Node $id
    }
}

function Get-Health([string]$nodeId) {
    $port = $nodeCfg[$nodeId].KV
    return Invoke-RestMethod -Method Get -Uri "http://localhost:$port/health" -TimeoutSec 1
}

function Wait-Condition([scriptblock]$Condition, [int]$TimeoutSec, [int]$SleepMs = 200) {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) {
            return $true
        }
        Start-Sleep -Milliseconds $SleepMs
    }
    return $false
}

function Wait-OneLeader([int]$TimeoutSec = 10) {
    $leaderRef = [ref] ""
    $ok = Wait-Condition {
        $leaders = @()
        foreach ($id in $allNodes) {
            if (-not $jobs.ContainsKey($id)) {
                continue
            }
            try {
                $h = Get-Health $id
                if ($h.state -eq "leader") {
                    $leaders += $id
                }
            } catch {
            }
        }
        if ($leaders.Count -eq 1) {
            $leaderRef.Value = $leaders[0]
            return $true
        }
        return $false
    } -TimeoutSec $TimeoutSec

    if (-not $ok) {
        throw "Expected exactly one leader, but cluster did not stabilize."
    }

    return $leaderRef.Value
}

function Invoke-KVWriteWithLeaderRetry([string]$startNodeId, [string]$key, [string]$value) {
    $startUrl = "http://localhost:$($nodeCfg[$startNodeId].KV)/set"
    $body = @{ key = $key; value = $value } | ConvertTo-Json -Compress

    try {
        return Invoke-RestMethod -Method Put -Uri $startUrl -ContentType "application/json" -Body $body -TimeoutSec 3
    } catch {
        $statusCode = -1
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $statusCode = [int]$_.Exception.Response.StatusCode
        }
        if ($statusCode -ne 409) {
            throw
        }

        $detail = $_.ErrorDetails.Message
        if (-not $detail) {
            throw
        }
        $errObj = $detail | ConvertFrom-Json
        if ($errObj.error -ne "not_leader" -or [string]::IsNullOrWhiteSpace($errObj.leader)) {
            throw
        }

        $leaderUrl = "$($errObj.leader.TrimEnd('/'))/set"
        return Invoke-RestMethod -Method Put -Uri $leaderUrl -ContentType "application/json" -Body $body -TimeoutSec 3
    }
}

function Get-Key([string]$nodeId, [string]$key) {
    $port = $nodeCfg[$nodeId].KV
    try {
        $r = Invoke-RestMethod -Method Get -Uri "http://localhost:$port/get?key=$key" -TimeoutSec 2
        return [pscustomobject]@{ Found = $true; Value = [string]$r.value }
    } catch {
        return [pscustomobject]@{ Found = $false; Value = "" }
    }
}

function Assert-KeyOnNode([string]$nodeId, [string]$key, [string]$expected, [int]$timeoutSec) {
    $ok = Wait-Condition {
        if (-not $jobs.ContainsKey($nodeId)) {
            return $false
        }
        $res = Get-Key $nodeId $key
        return ($res.Found -and $res.Value -eq $expected)
    } -TimeoutSec $timeoutSec -SleepMs 200

    if (-not $ok) {
        throw "Node $nodeId does not have $key=$expected within timeout."
    }
}

function Assert-KeyOnAllRunningNodes([string]$key, [string]$expected, [int]$timeoutSec) {
    foreach ($id in $allNodes) {
        if (-not $jobs.ContainsKey($id)) {
            continue
        }
        Assert-KeyOnNode $id $key $expected $timeoutSec
    }
}

try {
    Write-Host "Starting 3-node cluster..."
    foreach ($id in $allNodes) {
        Start-Node $id
    }

    $leader = Wait-OneLeader -TimeoutSec $BootTimeoutSec
    Write-Host "Leader elected: $leader"

    Write-Host "`nTest A: consistency after commit"
    $seedNode = ($allNodes | Where-Object { $_ -ne $leader })[0]
    $null = Invoke-KVWriteWithLeaderRetry -startNodeId $seedNode -key "x" -value "10"
    Assert-KeyOnAllRunningNodes -key "x" -expected "10" -timeoutSec 8
    Write-Host "PASS: all running nodes return x=10"

    Write-Host "`nTest 1: follower catch-up"
    $followerToKill = ($allNodes | Where-Object { $_ -ne $leader })[0]
    Stop-Node $followerToKill
    $null = Invoke-KVWriteWithLeaderRetry -startNodeId $leader -key "a" -value "1"
    $null = Invoke-KVWriteWithLeaderRetry -startNodeId $leader -key "b" -value "2"
    $null = Invoke-KVWriteWithLeaderRetry -startNodeId $leader -key "c" -value "3"
    Start-Node $followerToKill
    Assert-KeyOnNode -nodeId $followerToKill -key "a" -expected "1" -timeoutSec $CatchupTimeoutSec
    Assert-KeyOnNode -nodeId $followerToKill -key "b" -expected "2" -timeoutSec $CatchupTimeoutSec
    Assert-KeyOnNode -nodeId $followerToKill -key "c" -expected "3" -timeoutSec $CatchupTimeoutSec
    Write-Host "PASS: restarted follower caught up and applied a,b,c"

    Write-Host "`nTest 2: leader crash and re-election"
    $null = Invoke-KVWriteWithLeaderRetry -startNodeId $leader -key "x2" -value "10"
    Stop-Node $leader
    $newLeader = Wait-OneLeader -TimeoutSec $ElectionTimeoutSec
    Write-Host "New leader: $newLeader"
    $null = Invoke-KVWriteWithLeaderRetry -startNodeId $newLeader -key "y" -value "20"
    Assert-KeyOnAllRunningNodes -key "x2" -expected "10" -timeoutSec 10
    Assert-KeyOnAllRunningNodes -key "y" -expected "20" -timeoutSec 10
    Write-Host "PASS: cluster continued after leader failover"

    Write-Host "`nTest 3: no majority means no commit"
    $currentLeader = Wait-OneLeader -TimeoutSec $ElectionTimeoutSec
    $others = @($allNodes | Where-Object { $_ -ne $currentLeader -and $jobs.ContainsKey($_) })
    foreach ($id in $others) {
        Stop-Node $id
    }

    $timedOut = $false
    try {
        $null = Invoke-KVWriteWithLeaderRetry -startNodeId $currentLeader -key "no_majority" -value "z"
    } catch {
        $statusCode = -1
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $statusCode = [int]$_.Exception.Response.StatusCode
        }
        if ($statusCode -eq 504) {
            $timedOut = $true
        } else {
            throw
        }
    }

    if (-not $timedOut) {
        throw "Expected write timeout without majority, but write succeeded."
    }

    $localRead = Get-Key $currentLeader "no_majority"
    if ($localRead.Found) {
        throw "Uncommitted value appeared locally without majority."
    }
    Write-Host "PASS: write not committed without majority"

    Write-Host "`nRecovering cluster..."
    foreach ($id in $others) {
        Start-Node $id
    }
    $finalLeader = Wait-OneLeader -TimeoutSec $ElectionTimeoutSec
    Write-Host "Cluster recovered with leader: $finalLeader"

    Write-Host "`nDemo complete."
}
finally {
    Stop-AllNodes
}
