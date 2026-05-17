param(
    [switch]$BuildOnly,
    [switch]$SkipDockerBuild,
    [switch]$SyncDependencies,
    [string]$GoProxy = "https://goproxy.cn,direct",
    [string]$BuilderImage = "golang:1.26.3-alpine"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$RepoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $RepoRoot

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Fail {
    param([string]$Message)
    throw $Message
}

function Add-UserGoToPath {
    $goBin = Join-Path $env:LOCALAPPDATA "Programs\Go\go1.26.3\go\bin"
    if (Test-Path (Join-Path $goBin "go.exe")) {
        $env:Path = "$goBin;$env:Path"
    }
}

function Require-Command {
    param(
        [string]$Name,
        [string]$Hint
    )
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue
    if (-not $cmd) {
        Fail "$Name not found. $Hint"
    }
    return $cmd
}

function Invoke-Checked {
    param(
        [string]$Label,
        [scriptblock]$Command
    )
    Write-Step $Label
    & $Command
    if ($LASTEXITCODE -ne 0) {
        Fail "$Label failed with exit code $LASTEXITCODE"
    }
}

function Invoke-LzcProjectBuild {
    $buildConfig = Join-Path $RepoRoot "lzc-build.yml"
    $backupConfig = Join-Path $RepoRoot ".lzc-build.yml.deploy-backup"

    Copy-Item -LiteralPath $buildConfig -Destination $backupConfig -Force
    try {
        $content = Get-Content -Encoding UTF8 $backupConfig
        $content = $content | ForEach-Object {
            if ($_ -match "^\s*buildscript\s*:") {
                "buildscript: powershell -NoProfile -Command `"Write-Host 'dist already built by deploy.ps1'`""
            } else {
                $_
            }
        }
        Set-Content -Encoding UTF8 -LiteralPath $buildConfig -Value $content
        & $lzc.Source project build
    } finally {
        Move-Item -LiteralPath $backupConfig -Destination $buildConfig -Force
    }
}

function Read-PackageValue {
    param([string]$Key)
    $line = Get-Content -Encoding UTF8 package.yml |
        Where-Object { $_ -match "^\s*$([regex]::Escape($Key))\s*:" } |
        Select-Object -First 1
    if (-not $line) {
        Fail "package.yml missing $Key"
    }
    return (($line -replace "^\s*$([regex]::Escape($Key))\s*:\s*", "") -replace '["'']', "").Trim()
}

Add-UserGoToPath

$lzc = Require-Command "lzc-cli" "Install lzc-cli and make sure it is available in PowerShell PATH."
$package = Read-PackageValue "package"
$version = Read-PackageValue "version"
$lpk = "$package-v$version.lpk"

Write-Step "Preparing $lpk"

if ($SyncDependencies) {
    Require-Command "go" "Install Go 1.26.3 or run this script from a shell where go is available." | Out-Null
    $env:GOPROXY = $GoProxy
    Invoke-Checked "Sync Go dependencies" {
        go get gitee.com/linakesi/lzc-sdk/lang/go@latest
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        go mod tidy
    }
}

if (-not $SkipDockerBuild) {
    Require-Command "docker" "Windows deployment uses Docker to run build.sh in Linux because it packages mtr and Linux shared libraries." | Out-Null
    $repo = (Resolve-Path $RepoRoot).Path
    $mount = "${repo}:/src"
    $buildCommand = "apk add --no-cache ca-certificates mtr binutils git && sh build.sh"
    Invoke-Checked "Build dist/ in Linux container" {
        docker run --rm `
            -e "GOPROXY=$GoProxy" `
            -v $mount `
            -w /src `
            $BuilderImage `
            sh -lc $buildCommand
    }
} else {
    if (-not (Test-Path "dist\netwatch") -or -not (Test-Path "dist\netwatch-proxy") -or -not (Test-Path "dist\web")) {
        Fail "SkipDockerBuild was set, but dist/ is incomplete."
    }
}

if (Test-Path $lpk) {
    Remove-Item -LiteralPath $lpk -Force
}

Invoke-Checked "Build LPK with lzc-cli" {
    Invoke-LzcProjectBuild
}

if (-not (Test-Path $lpk)) {
    Fail "LPK not found: $lpk"
}

if ($BuildOnly) {
    Write-Host ""
    Write-Host "Build complete: $lpk" -ForegroundColor Green
    exit 0
}

Invoke-Checked "Install LPK with lzc-cli" {
    & $lzc.Source app install ".\$lpk"
}

Write-Host ""
Write-Host "Deploy complete: $lpk" -ForegroundColor Green
