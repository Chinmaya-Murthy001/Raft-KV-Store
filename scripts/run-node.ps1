param(
    [Parameter(Mandatory = $true)]
    [string]$id,
    [Parameter(Mandatory = $true)]
    [int]$port,
    [Parameter(Mandatory = $true)]
    [string]$peers
)

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

$env:NODE_ID = $id
$env:PORT = "$port"
$env:PEERS = $peers

go run .
