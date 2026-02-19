$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$runNode = Join-Path $scriptDir "run-node.ps1"
$repo = Split-Path -Parent $scriptDir

Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
    "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $runNode,
    "-id", "n1", "-port", "8081", "-raftPort", "9081", "-peers", "http://localhost:9082,http://localhost:9083"
)

Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
    "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $runNode,
    "-id", "n2", "-port", "8082", "-raftPort", "9082", "-peers", "http://localhost:9081,http://localhost:9083"
)

Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
    "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $runNode,
    "-id", "n3", "-port", "8083", "-raftPort", "9083", "-peers", "http://localhost:9081,http://localhost:9082"
)
