$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$runNode = Join-Path $scriptDir "run-node.ps1"
$repo = Split-Path -Parent $scriptDir

$nodes = @(
    @{
        Id       = "n1"
        Port     = "8081"
        RaftPort = "9081"
        Peers    = "http://localhost:9082,http://localhost:9083"
    },
    @{
        Id       = "n2"
        Port     = "8082"
        RaftPort = "9082"
        Peers    = "http://localhost:9081,http://localhost:9083"
    },
    @{
        Id       = "n3"
        Port     = "8083"
        RaftPort = "9083"
        Peers    = "http://localhost:9081,http://localhost:9082"
    }
)

foreach ($node in $nodes) {
    Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
        "-NoExit"
        "-ExecutionPolicy"
        "Bypass"
        "-File"
        $runNode
        "-id"
        $node.Id
        "-port"
        $node.Port
        "-raftPort"
        $node.RaftPort
        "-peers"
        $node.Peers
    )
}
