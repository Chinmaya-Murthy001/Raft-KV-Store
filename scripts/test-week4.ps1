param(
    [switch]$SkipS1
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

function Invoke-Step([string]$name, [scriptblock]$run) {
    Write-Host ""
    Write-Host ("=== {0} ===" -f $name)
    $start = Get-Date
    & $run
    $elapsed = (Get-Date) - $start
    Write-Host ("PASS [{0}] ({1:n1}s)" -f $name, $elapsed.TotalSeconds)
}

try {
    Invoke-Step "Go Unit Tests" { go test ./... | Out-Host }

    Invoke-Step "Week 3 Regression (A-H)" {
        & (Join-Path $PSScriptRoot "test-ah.ps1") | Out-Host
    }

    Invoke-Step "Week 4 P1 Follower Restart Catch-up" {
        & (Join-Path $PSScriptRoot "persistence-smoke.ps1") | Out-Host
    }

    Invoke-Step "Week 4 P2 Leader Crash/Restart Rejoin" {
        & (Join-Path $PSScriptRoot "day4-smoke.ps1") | Out-Host
    }

    Invoke-Step "Week 4 P3 Uncommitted Entry Never Applies" {
        & (Join-Path $PSScriptRoot "test-week4-p3.ps1") | Out-Host
    }

    if (-not $SkipS1 -and (Test-Path (Join-Path $PSScriptRoot "test-day6-s1.ps1"))) {
        Invoke-Step "S1 Snapshot Survives Restart" {
            & (Join-Path $PSScriptRoot "test-day6-s1.ps1") | Out-Host
        }
    }

    Write-Host ""
    Write-Host "PASS: Full validation runner completed."
}
catch {
    Write-Error ("FAIL: test-week4 stopped on step failure: {0}" -f $_.Exception.Message)
    exit 1
}
