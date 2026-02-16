$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$runNode = Join-Path $scriptDir "run-node.ps1"
$repo = Split-Path -Parent $scriptDir

Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
    "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $runNode,
    "-id", "n1", "-port", "8081", "-peers", "http://localhost:8082,http://localhost:8083"
)

Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
    "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $runNode,
    "-id", "n2", "-port", "8082", "-peers", "http://localhost:8081,http://localhost:8083"
)

Start-Process powershell -WorkingDirectory $repo -ArgumentList @(
    "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $runNode,
    "-id", "n3", "-port", "8083", "-peers", "http://localhost:8081,http://localhost:8082"
)
