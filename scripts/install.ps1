[CmdletBinding()]
param(
    [Alias('Directory')][string]$InstallDirectory,
    [switch]$Update,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'
if ($Help) {
    Write-Output 'Usage: .\install.ps1 [-Directory PATH] [-Update]'
    Write-Output 'Requires Git, Node.js 22.13+, npm and Go 1.27.3+.'
    Write-Output 'Builds and installs locally. Does not start a server or modify PATH.'
    return
}

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command exited with status $LASTEXITCODE" }
}

foreach ($tool in @('git', 'node', 'npm', 'go')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool must be installed and on PATH"
    }
}
$nodeVersion = & node --version
if ($LASTEXITCODE -ne 0 -or [version]($nodeVersion.TrimStart('v')) -lt [version]'22.13.0') {
    throw 'Node.js 22.13+ is required'
}
if ([string]::IsNullOrWhiteSpace($InstallDirectory)) {
    $base = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { Join-Path $HOME '.local/share' }
    $InstallDirectory = Join-Path $base 'vgalary'
}
$installRoot = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($InstallDirectory)
[void][IO.Directory]::CreateDirectory($installRoot)
$installRoot = (Resolve-Path -LiteralPath $installRoot).ProviderPath
$source = Join-Path $installRoot 'source'
$repository = 'https://github.com/snowmerak/vgalary.git'

function Read-GitText {
    param([string[]]$Arguments)
    $output = & git -C $source @Arguments
    if ($LASTEXITCODE -ne 0) { throw "git exited with status $LASTEXITCODE" }
    return ($output -join "`n").Trim()
}

$previousGitPrompt = $env:GIT_TERMINAL_PROMPT
try {
    $env:GIT_TERMINAL_PROMPT = '0'
    if (Test-Path -LiteralPath $source) {
        if (-not (Test-Path -LiteralPath (Join-Path $source '.git'))) {
            throw 'Existing source is not a Git checkout; choose another -Directory'
        }
        if (Read-GitText @('rev-parse', '--show-prefix')) { throw 'Existing source must be its own Git checkout' }
        if ((Read-GitText @('config', '--get', 'remote.origin.url')) -ne $repository) {
            throw 'Existing checkout has a different repository'
        }
        if (Read-GitText @('status', '--porcelain')) {
            throw 'Existing checkout contains local changes; preserve them before updating'
        }
        if ($Update) { Invoke-Checked git @('-C', $source, 'pull', '--ff-only') }
    } else {
        Invoke-Checked git @('clone', '--depth', '1', '--', $repository, $source)
    }
    $builder = Join-Path $source 'scripts/deploy.mjs'
    if (-not (Test-Path -LiteralPath $builder)) { throw 'Checkout has no scripts/deploy.mjs' }
    Invoke-Checked node @($builder, '--build-only')
} finally {
    $env:GIT_TERMINAL_PROMPT = $previousGitPrompt
}

$name = 'vgalary.exe'
$built = Join-Path $source ".deploy/$name"
if (-not (Test-Path -LiteralPath $built -PathType Leaf)) {
    $name = 'vgalary'
    $built = Join-Path $source ".deploy/$name"
}
if (-not (Test-Path -LiteralPath $built -PathType Leaf)) { throw 'Build did not produce a vgalary executable' }
$bin = Join-Path $installRoot 'bin'
[void][IO.Directory]::CreateDirectory($bin)
$binary = Join-Path $bin $name
$temporary = Join-Path $bin ('.vgalary-' + [Guid]::NewGuid().ToString('N') + '.tmp')
try {
    Copy-Item -LiteralPath $built -Destination $temporary
    if (Test-Path -LiteralPath $binary) {
        [IO.File]::Replace($temporary, $binary, [NullString]::Value)
    } else {
        [IO.File]::Move($temporary, $binary)
    }
} finally {
    if (Test-Path -LiteralPath $temporary) { [IO.File]::Delete($temporary) }
}
Write-Output "`nInstalled executable: $binary"
Write-Output ("Start: & '" + $binary.Replace("'", "''") + "'")
Write-Output 'Then open http://127.0.0.1:8181'
Write-Output 'Indexing additionally needs ffmpeg/ffprobe, llama.cpp and Qdrant.'
