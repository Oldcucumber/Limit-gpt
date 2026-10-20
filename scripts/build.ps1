# SPDX-License-Identifier: GPL-3.0-only
param([string]$Go = 'go', [string]$Bun = 'bun')
$ErrorActionPreference = 'Stop'
function Invoke-BuildTool([string]$Tool, [string[]]$ToolArguments) {
    Get-Command $Tool -ErrorAction Stop | Out-Null
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $Tool @ToolArguments 2>&1 | ForEach-Object { Write-Output "$_" }
        $toolExitCode = $LASTEXITCODE
    } finally { $ErrorActionPreference = $previousPreference }
    if ($toolExitCode -ne 0) { throw "$Tool failed with exit code $toolExitCode" }
}
$project = Split-Path -Parent $PSScriptRoot
$output = Join-Path $project 'dist\windows-amd64'
New-Item -ItemType Directory -Force $output | Out-Null
$previousVersion = $env:VERSION
Push-Location (Join-Path $project 'plugin')
try {
    Invoke-BuildTool $Go @('test','./...')
    Invoke-BuildTool $Go @('build','-buildmode=c-shared','-trimpath','-ldflags=-s -w','-o',(Join-Path $output 'limit-gpt.dll'),'.')
} finally { Pop-Location }
Push-Location (Join-Path $project 'frontend')
try {
    Invoke-BuildTool $Bun @('install','--frozen-lockfile')
    $env:VERSION = 'limit-gpt-0.1.0'
    Invoke-BuildTool $Bun @('run','build')
    Copy-Item -LiteralPath 'dist\index.html' -Destination (Join-Path $output 'management.html')
} finally { $env:VERSION = $previousVersion; Pop-Location }
foreach ($name in @('README_CN.md','config.example.yaml','LICENSE','THIRD_PARTY.md')) {
    Copy-Item -LiteralPath (Join-Path $project $name) -Destination $output
}
Copy-Item -LiteralPath (Join-Path $project 'licenses') -Destination $output -Recurse -Force
Write-Output "Output: $output"
