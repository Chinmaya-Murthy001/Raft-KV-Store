param(
    [Parameter(Mandatory = $true)]
    [string]$id,
    [Parameter(Mandatory = $true)]
    [int]$port,
    [Parameter(Mandatory = $true)]
    [int]$raftPort,
    [Parameter(Mandatory = $true)]
    [string]$peers
)

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

$env:NODE_ID = $id
$env:KVSTORE_PORT = "$port"
$env:RAFT_PORT = "$raftPort"
$env:PEERS = $peers

go run .
