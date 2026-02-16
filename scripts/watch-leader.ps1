param(
    [string[]]$Nodes = @(
        "http://localhost:8081",
        "http://localhost:8082",
        "http://localhost:8083"
    ),
    [int]$IntervalMs = 500
)

Write-Host "Watching cluster health every $IntervalMs ms. Press Ctrl+C to stop."

while ($true) {
    $rows = @()

    foreach ($node in $Nodes) {
        try {
            $h = Invoke-RestMethod -Method Get -Uri "$node/health" -TimeoutSec 1
            $rows += [pscustomobject]@{
                Node               = $node
                State              = $h.state
                Term               = $h.term
                LeaderID           = $h.leaderId
                LastHeartbeatAgoMs = $h.lastHeartbeatAgoMs
            }
        } catch {
            $rows += [pscustomobject]@{
                Node               = $node
                State              = "down"
                Term               = "-"
                LeaderID           = "-"
                LastHeartbeatAgoMs = "-"
            }
        }
    }

    $leaders = @($rows | Where-Object { $_.State -eq "leader" }).Count
    $ts = Get-Date -Format "HH:mm:ss.fff"
    Write-Host "`n[$ts] leaders=$leaders"
    $rows | Format-Table -AutoSize

    Start-Sleep -Milliseconds $IntervalMs
}
