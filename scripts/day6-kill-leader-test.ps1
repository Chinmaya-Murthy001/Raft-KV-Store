param(
    [int]$HeartbeatFreshMs = 250,
    [int]$BootTimeoutSec = 20,
    [int]$ReelectionTimeoutSec = 10,
    [int]$RestartTimeoutSec = 15
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$nodeCfg = @{
    n1 = @{ Port = "8081"; Peers = "http://localhost:8082,http://localhost:8083" }
    n2 = @{ Port = "8082"; Peers = "http://localhost:8081,http://localhost:8083" }
    n3 = @{ Port = "8083"; Peers = "http://localhost:8081,http://localhost:8082" }
}

function Start-Node([string]$nodeId) {
    $cfg = $nodeCfg[$nodeId]
    return Start-Job -ScriptBlock {
        param($workdir, $id, $port, $peers)
        Set-Location $workdir
        $env:NODE_ID = $id
        $env:PORT = $port
        $env:PEERS = $peers
        go run .
    } -ArgumentList $repo, $nodeId, $cfg.Port, $cfg.Peers
}

function Stop-Node($job) {
    if ($null -eq $job) {
        return
    }
    Stop-Job $job -ErrorAction SilentlyContinue | Out-Null
    Receive-Job $job -ErrorAction SilentlyContinue | Out-Null
    Remove-Job $job -ErrorAction SilentlyContinue | Out-Null
}

function Get-Health([string]$nodeId) {
    $port = $nodeCfg[$nodeId].Port
    return Invoke-RestMethod -Method Get -Uri "http://localhost:$port/health" -TimeoutSec 1
}

function Get-ClusterSnapshot {
    $rows = @()
    foreach ($id in @("n1", "n2", "n3")) {
        try {
            $h = Get-Health $id
            $rows += [pscustomobject]@{
                Node               = $id
                Up                 = $true
                State              = $h.state
                Term               = [int]$h.term
                LeaderID           = [string]$h.leaderId
                LastHeartbeatAgoMs = [int64]$h.lastHeartbeatAgoMs
            }
        } catch {
            $rows += [pscustomobject]@{
                Node               = $id
                Up                 = $false
                State              = "down"
                Term               = -1
                LeaderID           = ""
                LastHeartbeatAgoMs = -1
            }
        }
    }
    return $rows
}

function Wait-Condition([scriptblock]$Condition, [int]$TimeoutSec, [int]$SleepMs = 200) {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        $result = & $Condition
        if ($result) {
            return $true
        }
        Start-Sleep -Milliseconds $SleepMs
    }
    return $false
}

$jobs = @{}

try {
    Write-Host "Starting 3 nodes..."
    $jobs["n1"] = Start-Node "n1"
    $jobs["n2"] = Start-Node "n2"
    $jobs["n3"] = Start-Node "n3"

    $bootOk = Wait-Condition {
        $s = Get-ClusterSnapshot
        $leaders = @($s | Where-Object { $_.Up -and $_.State -eq "leader" }).Count
        $upCount = @($s | Where-Object { $_.Up }).Count
        return ($upCount -eq 3 -and $leaders -eq 1)
    } -TimeoutSec $BootTimeoutSec

    if (-not $bootOk) {
        throw "Cluster did not stabilize to exactly one leader during boot."
    }

    $before = Get-ClusterSnapshot
    Write-Host "`nInitial cluster:"
    $before | Format-Table -AutoSize

    $leader = ($before | Where-Object { $_.State -eq "leader" }).Node
    $followers = @($before | Where-Object { $_.Node -ne $leader -and $_.Up })

    foreach ($f in $followers) {
        if ($f.LastHeartbeatAgoMs -lt 0 -or $f.LastHeartbeatAgoMs -gt $HeartbeatFreshMs) {
            throw "Follower $($f.Node) heartbeat age is not fresh: $($f.LastHeartbeatAgoMs)ms"
        }
    }

    Write-Host "`nKilling leader: $leader"
    Stop-Node $jobs[$leader]
    $jobs.Remove($leader)

    $reelectOk = Wait-Condition {
        $s = Get-ClusterSnapshot
        $leaders = @($s | Where-Object { $_.Up -and $_.State -eq "leader" })
        $upCount = @($s | Where-Object { $_.Up }).Count
        return ($upCount -eq 2 -and $leaders.Count -eq 1)
    } -TimeoutSec $ReelectionTimeoutSec

    if (-not $reelectOk) {
        throw "No new leader elected within timeout."
    }

    $afterKill = Get-ClusterSnapshot
    Write-Host "`nAfter leader kill:"
    $afterKill | Format-Table -AutoSize

    $newLeader = ($afterKill | Where-Object { $_.Up -and $_.State -eq "leader" }).Node
    if ($newLeader -eq $leader) {
        throw "Leader did not change after kill."
    }

    Write-Host "`nRestarting old leader: $leader"
    $jobs[$leader] = Start-Node $leader

    $restartOk = Wait-Condition {
        try {
            $h = Get-Health $leader
            return ($h.state -eq "follower" -and $h.leaderId -eq $newLeader -and [int64]$h.lastHeartbeatAgoMs -ge 0)
        } catch {
            return $false
        }
    } -TimeoutSec $RestartTimeoutSec

    if (-not $restartOk) {
        throw "Restarted node did not rejoin as follower with heartbeat."
    }

    $final = Get-ClusterSnapshot
    Write-Host "`nFinal cluster:"
    $final | Format-Table -AutoSize

    Write-Host "`nPASS: Day 6 kill-leader test completed."
}
finally {
    foreach ($id in @("n1", "n2", "n3")) {
        if ($jobs.ContainsKey($id)) {
            Stop-Node $jobs[$id]
        }
    }
}
