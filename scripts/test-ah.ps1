param(
    [int]$StartupTimeoutSec = 25,
    [int]$ElectionTimeoutSec = 20
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$logRoot = Join-Path $repo "tmp-test-logs"
New-Item -ItemType Directory -Path $logRoot -Force | Out-Null

$nodeCfg = [ordered]@{
    n1 = [ordered]@{ KV = 8080; Raft = 9080; Peers = "http://localhost:9081,http://localhost:9082"; PeerIDs = "n2,n3" }
    n2 = [ordered]@{ KV = 8081; Raft = 9081; Peers = "http://localhost:9080,http://localhost:9082"; PeerIDs = "n1,n3" }
    n3 = [ordered]@{ KV = 8082; Raft = 9082; Peers = "http://localhost:9080,http://localhost:9081"; PeerIDs = "n1,n2" }
}
$nodeIDs = @("n1", "n2", "n3")
$kvPorts = @($nodeCfg.n1.KV, $nodeCfg.n2.KV, $nodeCfg.n3.KV)
$jobs = @{}
$activeLogFiles = @{}
$clusterRun = 0
$results = [ordered]@{}

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

function Start-Node([string]$id) {
    if ($jobs.ContainsKey($id)) {
        return
    }

    $cfg = $nodeCfg[$id]
    $logFile = Join-Path $logRoot ("run{0}-{1}.log" -f $clusterRun, $id)
    if (Test-Path $logFile) {
        Remove-Item $logFile -Force -ErrorAction SilentlyContinue
    }

    $job = Start-Job -Name ("raft-" + $id) -ScriptBlock {
        param($workdir, $nodeID, $kvPort, $raftPort, $peers, $peerIDs, $logPath)
        Set-Location $workdir
        $env:NODE_ID = $nodeID
        $env:KVSTORE_PORT = "$kvPort"
        $env:RAFT_PORT = "$raftPort"
        $env:PEERS = $peers
        $env:PEER_IDS = $peerIDs
        go run . *>&1 | Tee-Object -FilePath $logPath -Append
    } -ArgumentList $repo, $id, $cfg.KV, $cfg.Raft, $cfg.Peers, $cfg.PeerIDs, $logFile

    $jobs[$id] = $job
    $activeLogFiles[$id] = $logFile
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

function Stop-Cluster {
    foreach ($id in @($jobs.Keys)) {
        Stop-Node $id
    }
    Start-Sleep -Milliseconds 700
}

function Get-RaftStatus([string]$id) {
    $raftPort = $nodeCfg[$id].Raft
    try {
        return Invoke-RestMethod -Method Get -Uri "http://localhost:$raftPort/raft/status" -TimeoutSec 2
    }
    catch {
        return $null
    }
}

function Get-AllRaftStatuses([string[]]$ids = $nodeIDs) {
    $statuses = @{}
    foreach ($id in $ids) {
        $statuses[$id] = Get-RaftStatus $id
    }
    return $statuses
}

function Find-Leader([hashtable]$statuses, [string[]]$ids) {
    $leaders = @()
    foreach ($id in $ids) {
        $s = $statuses[$id]
        if ($null -ne $s -and [string]$s.state -eq "leader") {
            $leaders += $id
        }
    }

    if ($leaders.Count -eq 1) {
        return $leaders[0]
    }
    return $null
}

function Wait-OneLeader([string[]]$ids = $nodeIDs, [int]$TimeoutSec = 15) {
    $leaderRef = [ref] ""
    $ok = Wait-Until {
        $statuses = Get-AllRaftStatuses -ids $ids
        $leader = Find-Leader -statuses $statuses -ids $ids
        if ([string]::IsNullOrWhiteSpace($leader)) {
            return $false
        }
        $leaderRef.Value = $leader
        return $true
    } -TimeoutSec $TimeoutSec -SleepMs 300

    if (-not $ok) {
        return $null
    }
    return $leaderRef.Value
}

function Start-Cluster {
    $script:clusterRun++
    foreach ($id in $nodeIDs) {
        Start-Node $id
    }

    $leader = Wait-OneLeader -ids $nodeIDs -TimeoutSec $StartupTimeoutSec
    if ([string]::IsNullOrWhiteSpace($leader)) {
        throw "Cluster did not elect a leader in time."
    }

    return $leader
}

function Invoke-Set([int]$kvPort, [string]$key, [string]$value) {
    $uri = "http://localhost:$kvPort/set"
    $body = @{ key = $key; value = $value } | ConvertTo-Json -Compress
    try {
        $resp = Invoke-WebRequest -Method Put -Uri $uri -ContentType "application/json" -Body $body -TimeoutSec 5 -UseBasicParsing
        $statusCode = To-StatusCode $resp.StatusCode
        if ($statusCode -lt 0 -and $resp.BaseResponse) {
            $statusCode = To-StatusCode $resp.BaseResponse.StatusCode
        }
        $json = $null
        try {
            $json = $resp.Content | ConvertFrom-Json
        }
        catch {
        }

        return [pscustomobject]@{
            Status = $statusCode
            Body   = $resp.Content
            Json   = $json
        }
    }
    catch {
        $status = -1
        $content = $_.ErrorDetails.Message
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $status = To-StatusCode $_.Exception.Response.StatusCode
        }
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
            Body   = $content
            Json   = $json
        }
    }
}

function Invoke-GetKey([int]$kvPort, [string]$key) {
    $uri = "http://localhost:$kvPort/get?key=$key"
    try {
        $resp = Invoke-WebRequest -Method Get -Uri $uri -TimeoutSec 3 -UseBasicParsing
        $statusCode = To-StatusCode $resp.StatusCode
        if ($statusCode -lt 0 -and $resp.BaseResponse) {
            $statusCode = To-StatusCode $resp.BaseResponse.StatusCode
        }
        $json = $null
        try {
            $json = $resp.Content | ConvertFrom-Json
        }
        catch {
        }

        return [pscustomobject]@{
            Status = $statusCode
            Body   = $resp.Content
            Json   = $json
        }
    }
    catch {
        $status = -1
        $content = $_.ErrorDetails.Message
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            $status = To-StatusCode $_.Exception.Response.StatusCode
        }
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
            Body   = $content
            Json   = $json
        }
    }
}

function Wait-KeyValueOnNode([string]$id, [string]$key, [string]$expected, [int]$TimeoutSec = 10) {
    $kvPort = $nodeCfg[$id].KV
    return Wait-Until {
        $r = Invoke-GetKey -kvPort $kvPort -key $key
        return ($r.Status -eq 200 -and $null -ne $r.Json -and [string]$r.Json.value -eq $expected)
    } -TimeoutSec $TimeoutSec -SleepMs 250
}

function Wait-KeyValueOnAll([string]$key, [string]$expected, [string[]]$ids = $nodeIDs, [int]$TimeoutSec = 12) {
    return Wait-Until {
        foreach ($id in $ids) {
            $ok = Wait-KeyValueOnNode -id $id -key $key -expected $expected -TimeoutSec 1
            if (-not $ok) {
                return $false
            }
        }
        return $true
    } -TimeoutSec $TimeoutSec -SleepMs 250
}

function Add-Result([string]$name, [bool]$pass, $details) {
    $results[$name] = [ordered]@{
        pass    = $pass
        details = $details
    }
}

try {
    # A/B/C
    Stop-Cluster
    $leader = Start-Cluster
    $statuses = Get-AllRaftStatuses
    $leaders = @($nodeIDs | Where-Object { $null -ne $statuses[$_] -and [string]$statuses[$_].state -eq "leader" })
    $followers = @($nodeIDs | Where-Object { $null -ne $statuses[$_] -and [string]$statuses[$_].state -eq "follower" })
    $terms = @($nodeIDs | ForEach-Object { if ($null -ne $statuses[$_]) { [int]$statuses[$_].term } })
    $a1Pass = ($leaders.Count -eq 1 -and $followers.Count -eq 2 -and (($terms | Sort-Object -Unique).Count -le 1))
    Add-Result "A1_exactly_one_leader" $a1Pass @{
        leaders = $leaders
        states  = @{
            n1 = if ($null -ne $statuses.n1) { $statuses.n1.state } else { "down" }
            n2 = if ($null -ne $statuses.n2) { $statuses.n2.state } else { "down" }
            n3 = if ($null -ne $statuses.n3) { $statuses.n3.state } else { "down" }
        }
        terms   = @{
            n1 = if ($null -ne $statuses.n1) { $statuses.n1.term } else { -1 }
            n2 = if ($null -ne $statuses.n2) { $statuses.n2.term } else { -1 }
            n3 = if ($null -ne $statuses.n3) { $statuses.n3.term } else { -1 }
        }
    }

    $baseLeader = $leader
    $candidateSeen = $false
    $leaderChanged = $false
    for ($i = 0; $i -lt 15; $i++) {
        Start-Sleep -Seconds 1
        $sample = Get-AllRaftStatuses
        $sampleLeader = Find-Leader -statuses $sample -ids $nodeIDs
        if ([string]::IsNullOrWhiteSpace($sampleLeader) -or $sampleLeader -ne $baseLeader) {
            $leaderChanged = $true
            break
        }
        foreach ($id in $nodeIDs) {
            if ($id -eq $baseLeader) {
                continue
            }
            if ($null -ne $sample[$id] -and [string]$sample[$id].state -eq "candidate") {
                $candidateSeen = $true
            }
        }
    }
    Add-Result "A2_no_follower_election_spam" (-not $leaderChanged -and -not $candidateSeen) @{
        watchedSeconds = 15
        leader         = $baseLeader
        leaderChanged  = $leaderChanged
        candidateSeen  = $candidateSeen
    }

    $follower = @($nodeIDs | Where-Object { $_ -ne $leader })[0]
    $followerWrite = Invoke-Set -kvPort $nodeCfg[$follower].KV -key "t1" -value "111"
    $leaderHintPort = -1
    if ($null -ne $followerWrite.Json -and -not [string]::IsNullOrWhiteSpace([string]$followerWrite.Json.leader)) {
        try {
            $leaderHintPort = ([uri]$followerWrite.Json.leader).Port
        }
        catch {
            $leaderHintPort = -1
        }
    }
    $b1Pass = (
        $followerWrite.Status -eq 409 -and
        $null -ne $followerWrite.Json -and
        [string]$followerWrite.Json.error -eq "not_leader" -and
        ($kvPorts -contains $leaderHintPort)
    )
    Add-Result "B1_write_to_follower_not_leader" $b1Pass @{
        follower       = $follower
        status         = $followerWrite.Status
        response       = $followerWrite.Json
        leaderHintPort = $leaderHintPort
    }

    $preLeaderStatus = Get-RaftStatus $leader
    $leaderWrite = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "t2" -value "222"
    $commitAdvanced = Wait-Until {
        $now = Get-RaftStatus $leader
        if ($null -eq $now -or $null -eq $preLeaderStatus) {
            return $false
        }
        return ([int]$now.commitIndex -gt [int]$preLeaderStatus.commitIndex -and [int]$now.lastApplied -ge [int]$now.commitIndex -and [int]$now.logLen -gt [int]$preLeaderStatus.logLen)
    } -TimeoutSec 10 -SleepMs 250
    $postLeaderStatus = Get-RaftStatus $leader
    $b2Pass = ($leaderWrite.Status -eq 200 -and $commitAdvanced)
    Add-Result "B2_write_to_leader_commit_apply" $b2Pass @{
        leader    = $leader
        writeCode = $leaderWrite.Status
        before    = $preLeaderStatus
        after     = $postLeaderStatus
    }

    $c1Pass = Wait-KeyValueOnAll -key "t2" -expected "222" -ids $nodeIDs -TimeoutSec 12
    Add-Result "C1_read_on_all_nodes_after_commit" $c1Pass @{
        key = "t2"
    }

    $preC2 = Get-RaftStatus $leader
    $w1 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "o1" -value "1"
    $w2 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "o2" -value "2"
    $w3 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "o3" -value "3"
    $readsOrdered = (
        (Wait-KeyValueOnAll -key "o1" -expected "1" -ids $nodeIDs -TimeoutSec 12) -and
        (Wait-KeyValueOnAll -key "o2" -expected "2" -ids $nodeIDs -TimeoutSec 12) -and
        (Wait-KeyValueOnAll -key "o3" -expected "3" -ids $nodeIDs -TimeoutSec 12)
    )
    $postC2 = Get-RaftStatus $leader
    $commitMonotonic = ($null -ne $preC2 -and $null -ne $postC2 -and [int]$postC2.commitIndex -ge ([int]$preC2.commitIndex + 3))
    $c2Pass = ($w1.Status -eq 200 -and $w2.Status -eq 200 -and $w3.Status -eq 200 -and $readsOrdered -and $commitMonotonic)
    Add-Result "C2_multiple_writes_preserve_order" $c2Pass @{
        writeCodes = @($w1.Status, $w2.Status, $w3.Status)
        before     = if ($null -ne $preC2) { $preC2.commitIndex } else { -1 }
        after      = if ($null -ne $postC2) { $postC2.commitIndex } else { -1 }
    }

    # D
    Stop-Cluster
    $leader = Start-Cluster
    $followers = @($nodeIDs | Where-Object { $_ -ne $leader })
    $offlineFollower = $followers[0]
    Stop-Node $offlineFollower
    Start-Sleep -Milliseconds 500

    $dW1 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "cf1" -value "A"
    $dW2 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "cf2" -value "B"
    $dW3 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "cf3" -value "C"
    Start-Node $offlineFollower
    $catchupRead = (
        (Wait-KeyValueOnNode -id $offlineFollower -key "cf1" -expected "A" -TimeoutSec 20) -and
        (Wait-KeyValueOnNode -id $offlineFollower -key "cf2" -expected "B" -TimeoutSec 20) -and
        (Wait-KeyValueOnNode -id $offlineFollower -key "cf3" -expected "C" -TimeoutSec 20)
    )
    $catchupStatus = Wait-Until {
        $leaderNow = Wait-OneLeader -ids $nodeIDs -TimeoutSec 3
        if ([string]::IsNullOrWhiteSpace($leaderNow)) {
            return $false
        }
        $ls = Get-RaftStatus $leaderNow
        $fs = Get-RaftStatus $offlineFollower
        if ($null -eq $ls -or $null -eq $fs) {
            return $false
        }
        return ([int]$fs.lastApplied -ge [int]$ls.commitIndex)
    } -TimeoutSec 20 -SleepMs 300
    $d1Pass = ($dW1.Status -eq 200 -and $dW2.Status -eq 200 -and $dW3.Status -eq 200 -and $catchupRead -and $catchupStatus)
    Add-Result "D1_lagging_follower_catchup" $d1Pass @{
        offlineFollower = $offlineFollower
        writeCodes      = @($dW1.Status, $dW2.Status, $dW3.Status)
        catchupRead     = $catchupRead
        catchupStatus   = $catchupStatus
    }

    # E (optional)
    Stop-Cluster
    $leader = Start-Cluster
    $followers = @($nodeIDs | Where-Object { $_ -ne $leader })
    $isoA = $followers[0]
    $isoB = $followers[1]

    Stop-Node $isoA
    $eCommitted = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "e_commit" -value "1"
    Stop-Node $isoB
    $eUncommitted = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "e_uncommitted" -value "999"
    Stop-Node $leader

    Start-Node $isoA
    Start-Node $isoB
    $twoNodeLeader = Wait-OneLeader -ids @($isoA, $isoB) -TimeoutSec $ElectionTimeoutSec

    $eNewWrite = $null
    if (-not [string]::IsNullOrWhiteSpace($twoNodeLeader)) {
        $eNewWrite = Invoke-Set -kvPort $nodeCfg[$twoNodeLeader].KV -key "e_new" -value "3"
    }

    Start-Node $leader
    $allLeader = Wait-OneLeader -ids $nodeIDs -TimeoutSec $ElectionTimeoutSec
    $eNewPresent = $false
    if (-not [string]::IsNullOrWhiteSpace($allLeader)) {
        $eNewPresent = Wait-KeyValueOnAll -key "e_new" -expected "3" -ids $nodeIDs -TimeoutSec 15
    }

    $eUncommittedAbsent = $true
    foreach ($id in $nodeIDs) {
        $r = Invoke-GetKey -kvPort $nodeCfg[$id].KV -key "e_uncommitted"
        if ($r.Status -eq 200) {
            $eUncommittedAbsent = $false
        }
    }

    $e1Pass = (
        $eCommitted.Status -eq 200 -and
        $null -ne $eNewWrite -and $eNewWrite.Status -eq 200 -and
        $eNewPresent -and
        $eUncommittedAbsent
    )
    Add-Result "E1_conflicting_uncommitted_tail_resolution_optional" $e1Pass @{
        oldLeader         = $leader
        isolatedFollowers = @($isoA, $isoB)
        committedWrite    = $eCommitted.Status
        uncommittedWrite  = $eUncommitted.Status
        twoNodeLeader     = $twoNodeLeader
        resumedLeader     = $allLeader
        eNewWriteCode     = if ($null -ne $eNewWrite) { $eNewWrite.Status } else { -1 }
        eNewPresent       = $eNewPresent
        eUncommittedGone  = $eUncommittedAbsent
    }

    # F
    Stop-Cluster
    $leader = Start-Cluster
    $followers = @($nodeIDs | Where-Object { $_ -ne $leader })
    $downFollower = $followers[0]
    $aliveFollower = $followers[1]
    Stop-Node $downFollower

    $fWrite = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "f_majority" -value "ok"
    $fLeaderRead = Wait-KeyValueOnNode -id $leader -key "f_majority" -expected "ok" -TimeoutSec 10
    $fOtherRead = Wait-KeyValueOnNode -id $aliveFollower -key "f_majority" -expected "ok" -TimeoutSec 10
    $f1Pass = ($fWrite.Status -eq 200 -and $fLeaderRead -and $fOtherRead)
    Add-Result "F1_commit_with_one_follower_down" $f1Pass @{
        leader        = $leader
        downFollower  = $downFollower
        aliveFollower = $aliveFollower
        writeCode     = $fWrite.Status
        leaderRead    = $fLeaderRead
        otherRead     = $fOtherRead
    }

    Stop-Node $aliveFollower
    $preF2 = Get-RaftStatus $leader
    $f2Write = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "f_no_majority" -value "zzz"
    $postF2 = Get-RaftStatus $leader
    $localReadF2 = Invoke-GetKey -kvPort $nodeCfg[$leader].KV -key "f_no_majority"
    $f2Pass = (
        $f2Write.Status -ne 200 -and
        $null -ne $preF2 -and $null -ne $postF2 -and
        [int]$postF2.commitIndex -eq [int]$preF2.commitIndex -and
        $localReadF2.Status -ne 200
    )
    Add-Result "F2_no_majority_no_commit" $f2Pass @{
        writeCode         = $f2Write.Status
        commitIndexBefore = if ($null -ne $preF2) { $preF2.commitIndex } else { -1 }
        commitIndexAfter  = if ($null -ne $postF2) { $postF2.commitIndex } else { -1 }
        localReadStatus   = $localReadF2.Status
    }

    Start-Node $downFollower
    Start-Node $aliveFollower
    $null = Wait-OneLeader -ids $nodeIDs -TimeoutSec $ElectionTimeoutSec

    # G
    Stop-Cluster
    $leader = Start-Cluster
    $gWrite1 = Invoke-Set -kvPort $nodeCfg[$leader].KV -key "lc1" -value "10"
    $gFirstReplicated = Wait-KeyValueOnAll -key "lc1" -expected "10" -ids $nodeIDs -TimeoutSec 12
    Stop-Node $leader
    $remaining = @($nodeIDs | Where-Object { $_ -ne $leader })
    $newLeader = Wait-OneLeader -ids $remaining -TimeoutSec $ElectionTimeoutSec
    $gWrite2 = $null
    if (-not [string]::IsNullOrWhiteSpace($newLeader)) {
        $gWrite2 = Invoke-Set -kvPort $nodeCfg[$newLeader].KV -key "lc2" -value "20"
    }
    Start-Node $leader
    $finalLeader = Wait-OneLeader -ids $nodeIDs -TimeoutSec $ElectionTimeoutSec
    $gAllKeys = $false
    if (-not [string]::IsNullOrWhiteSpace($finalLeader)) {
        $gAllKeys = (
            (Wait-KeyValueOnAll -key "lc1" -expected "10" -ids $nodeIDs -TimeoutSec 15) -and
            (Wait-KeyValueOnAll -key "lc2" -expected "20" -ids $nodeIDs -TimeoutSec 15)
        )
    }
    $g1Pass = (
        $gWrite1.Status -eq 200 -and
        $gFirstReplicated -and
        -not [string]::IsNullOrWhiteSpace($newLeader) -and
        $null -ne $gWrite2 -and $gWrite2.Status -eq 200 -and
        $gAllKeys
    )
    Add-Result "G1_leader_crash_recovery" $g1Pass @{
        oldLeader      = $leader
        newLeader      = $newLeader
        finalLeader    = $finalLeader
        write1Code     = $gWrite1.Status
        write2Code     = if ($null -ne $gWrite2) { $gWrite2.Status } else { -1 }
        lc1Replicated  = $gFirstReplicated
        allKeysOnAll   = $gAllKeys
    }

    # H
    Stop-Cluster
    $leader = Start-Cluster
    $hWrites = @()
    for ($i = 1; $i -le 5; $i++) {
        $hWrites += Invoke-Set -kvPort $nodeCfg[$leader].KV -key ("h" + $i) -value "$i"
    }
    $hValuesPresent = $true
    for ($i = 1; $i -le 5; $i++) {
        if (-not (Wait-KeyValueOnAll -key ("h" + $i) -expected "$i" -ids $nodeIDs -TimeoutSec 12)) {
            $hValuesPresent = $false
            break
        }
    }
    Start-Sleep -Seconds 1

    $hNode = @{}
    $hAllPass = $true
    foreach ($id in $nodeIDs) {
        $logFile = $activeLogFiles[$id]
        $text = ""
        if (Test-Path $logFile) {
            $text = Get-Content $logFile -Raw
        }
        $matches = [regex]::Matches($text, "\[apply\] index=(\d+)")
        $indices = @($matches | ForEach-Object { [int]$_.Groups[1].Value })
        $dups = @($indices | Group-Object | Where-Object { $_.Count -gt 1 } | ForEach-Object { [int]$_.Name })
        $increasing = $true
        for ($i = 1; $i -lt $indices.Count; $i++) {
            if ($indices[$i] -le $indices[$i - 1]) {
                $increasing = $false
                break
            }
        }

        $nodePass = ($dups.Count -eq 0 -and $increasing -and $indices.Count -ge 5)
        if (-not $nodePass) {
            $hAllPass = $false
        }
        $hNode[$id] = @{
            applyCount   = $indices.Count
            duplicateIdx = $dups
            increasing   = $increasing
        }
    }

    $hWriteCodes = @($hWrites | ForEach-Object { $_.Status })
    $h1Pass = ($hValuesPresent -and $hAllPass -and (@($hWriteCodes | Where-Object { $_ -ne 200 }).Count -eq 0))
    Add-Result "H1_exactly_once_apply_per_node" $h1Pass @{
        leader       = $leader
        writeCodes   = $hWriteCodes
        valuesOnAll  = $hValuesPresent
        nodeEvidence = $hNode
    }
}
finally {
    Stop-Cluster
}

$results | ConvertTo-Json -Depth 8
