# SAME installer for Windows PowerShell
# Usage: irm https://statelessagent.com/install.ps1 | iex
#
# If this fails with "execution policy" error, run first:
#   Set-ExecutionPolicy RemoteSigned -Scope CurrentUser

$ErrorActionPreference = "Stop"

# Force TLS 1.2 (required for older Windows)
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# Colors - use [char]27 for PowerShell 5.1 compatibility
$ESC = [char]27
$B1 = "$ESC[38;5;117m"
$B2 = "$ESC[38;5;75m"
$B3 = "$ESC[38;5;69m"
$B4 = "$ESC[38;5;33m"
$Blue = "$ESC[38;5;75m"
$Red = "$ESC[91m"
$DarkRed = "$ESC[31m"
$Green = "$ESC[32m"
$Yellow = "$ESC[33m"
$Dim = "$ESC[2m"
$Bold = "$ESC[1m"
$Reset = "$ESC[0m"

# Detect if terminal supports ANSI (Windows Terminal, PS7, etc)
$supportsANSI = $env:WT_SESSION -or $PSVersionTable.PSVersion.Major -ge 7 -or $env:TERM_PROGRAM
if (-not $supportsANSI) {
    $B1 = ""; $B2 = ""; $B3 = ""; $B4 = ""; $Blue = ""; $Red = ""; $DarkRed = ""; $Green = ""; $Yellow = ""; $Dim = ""; $Bold = ""; $Reset = ""
}

# Banner
Write-Host ""
Write-Host "${B1}  ███████╗ █████╗ ███╗   ███╗███████╗${Reset}"
Write-Host "${B2}  ██╔════╝██╔══██╗████╗ ████║██╔════╝${Reset}"
Write-Host "${B2}  ███████╗███████║██╔████╔██║█████╗  ${Reset}"
Write-Host "${B3}  ╚════██║██╔══██║██║╚██╔╝██║██╔══╝  ${Reset}"
Write-Host "${B4}  ███████║██║  ██║██║ ╚═╝ ██║███████╗${Reset}"
Write-Host "${B4}  ╚══════╝╚═╝  ╚═╝╚═╝     ╚═╝╚══════╝${Reset}"
Write-Host "    ${Dim}Stateless Agent Memory Engine${Reset}"
Write-Host ""
Write-Host "${Dim}  Every AI session starts from zero.${Reset} ${Bold}${Red}Not anymore.${Reset}"
Write-Host ""

# ─────────────────────────────────────────────────────────────
# Step 1: Detect system
# ─────────────────────────────────────────────────────────────

Write-Host "[1/5] Detecting your system..."
Write-Host ""

$arch = [System.Environment]::Is64BitOperatingSystem
if (-not $arch) {
    Write-Host "  SAME requires 64-bit Windows."
    Write-Host "  Please ask for help: https://discord.gg/9KfTkcGs7g"
    exit 1
}

# Check for ARM64
$isArm = $false
try {
    $procArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    if ($procArch -eq "Arm64") { $isArm = $true }
} catch {
    # Older PowerShell, assume x64
}

if ($isArm) {
    $Suffix = "windows-arm64.exe"
    $ArchName = "ARM 64-bit"
    Write-Host "  ${Yellow}!${Reset} ARM Windows detected. No pre-built binary available."
    Write-Host "    Will try to build from source."
    Write-Host ""
} else {
    $Suffix = "windows-amd64.exe"
    $ArchName = "64-bit"
}

Write-Host "  Found: Windows ($ArchName)"
Write-Host "  PowerShell: $($PSVersionTable.PSVersion)"
Write-Host ""

# ─────────────────────────────────────────────────────────────
# Step 2: Get SAME (download or build from source)
# ─────────────────────────────────────────────────────────────

Write-Host "[2/5] Getting SAME..."
Write-Host ""

$Repo = "sgx-labs/statelessagent"
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\SAME"
$OutputFile = Join-Path $InstallDir "same.exe"

# Create install directory
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$BinaryAcquired = $false
$SkipDownload = $isArm

if (-not $SkipDownload) {
    # Strategy 1: Download pre-built binary from GitHub Releases
    Write-Host "  Checking for latest release..."

    try {
        $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
        $Latest = $Release.tag_name
        Write-Host "  Latest version: $Latest"

        $BinaryName = "same-$Suffix"
        $Url = "https://github.com/$Repo/releases/download/$Latest/$BinaryName"
        $TempFile = Join-Path $env:TEMP "same-download.exe"

        try {
            Invoke-WebRequest -Uri $Url -OutFile $TempFile -UseBasicParsing
            Move-Item -Path $TempFile -Destination $OutputFile -Force
            $BinaryAcquired = $true
            Write-Host "  Downloaded successfully."
        } catch {
            Remove-Item -Path $TempFile -ErrorAction SilentlyContinue
            Write-Host "  Download failed."
        }
    } catch {
        # Check if we can reach GitHub at all
        try {
            Invoke-WebRequest -Uri "https://api.github.com" -UseBasicParsing -TimeoutSec 5 | Out-Null
            Write-Host "  No release found on GitHub."
        } catch {
            Write-Host "  ${Yellow}!${Reset} Can't reach GitHub."
            Write-Host "    Behind a proxy? Set HTTPS_PROXY environment variable."
        }
    }
}

# Strategy 2: Build from source
if (-not $BinaryAcquired) {
    Write-Host ""

    $HasGo = $null -ne (Get-Command go -ErrorAction SilentlyContinue)
    $HasGit = $null -ne (Get-Command git -ErrorAction SilentlyContinue)
    $HasCC = ($null -ne (Get-Command gcc -ErrorAction SilentlyContinue)) -or
             ($null -ne (Get-Command cl -ErrorAction SilentlyContinue))

    if ($HasGo -and $HasGit -and $HasCC) {
        # Check Go version
        $GoVersionRaw = (go version) -replace '.*go(\d+\.\d+).*', '$1'
        $GoMinor = [int]($GoVersionRaw.Split('.')[1])

        if ($GoMinor -ge 25) {
            Write-Host "  Go $GoVersionRaw found - building from source..."
            $TempDir = Join-Path $env:TEMP "same-build-$PID"
            try {
                git clone --depth 1 "https://github.com/$Repo.git" $TempDir 2>$null
                Push-Location $TempDir
                $env:CGO_ENABLED = "1"
                go build -o same.exe ./cmd/same/
                Pop-Location
                Move-Item -Path (Join-Path $TempDir "same.exe") -Destination $OutputFile -Force
                Remove-Item -Path $TempDir -Recurse -Force -ErrorAction SilentlyContinue
                $BinaryAcquired = $true
                Write-Host "  Built successfully."
            } catch {
                Pop-Location -ErrorAction SilentlyContinue
                Remove-Item -Path $TempDir -Recurse -Force -ErrorAction SilentlyContinue
                Write-Host "  Build failed: $_"
            }
        } else {
            Write-Host "  Go $GoVersionRaw found but SAME needs Go 1.25+."
            Write-Host "  Upgrade: https://go.dev/dl/"
        }
    } else {
        if (-not $HasGo) {
            Write-Host "  Go not installed (needed to build from source)."
        } elseif (-not $HasGit) {
            Write-Host "  git not installed (needed to clone source)."
        } elseif (-not $HasCC) {
            Write-Host "  No C compiler found (needed for CGO/SQLite)."
            Write-Host "    Install MinGW-w64 or Visual Studio Build Tools."
        }
    }
}

# Strategy 3: Clear error with options
if (-not $BinaryAcquired) {
    Write-Host ""
    Write-Host "  I couldn't download SAME and can't build from source."
    Write-Host ""
    Write-Host "  You have three options:"
    Write-Host ""
    Write-Host "  1. Try again later (GitHub may be temporarily down)"
    Write-Host "     irm statelessagent.com/install.ps1 | iex"
    Write-Host ""
    Write-Host "  2. Install Go and build from source"
    Write-Host "     https://go.dev/dl/"
    Write-Host "     Then: git clone https://github.com/$Repo.git"
    Write-Host "           cd statelessagent; make install"
    Write-Host ""
    Write-Host "  3. Ask for help"
    Write-Host "     https://discord.gg/9KfTkcGs7g"
    exit 1
}

Write-Host ""

# ─────────────────────────────────────────────────────────────
# Step 3: Install
# ─────────────────────────────────────────────────────────────

Write-Host "[3/5] Installing SAME..."
Write-Host ""

Write-Host "  Installing to: $InstallDir"
Write-Host ""

# Unblock the file (removes "downloaded from internet" flag)
try {
    Unblock-File -Path $OutputFile -ErrorAction SilentlyContinue
} catch {
    # Ignore - not critical
}

# Verify it works
$installedVersion = $null
try {
    $installedVersion = & $OutputFile version 2>&1
    if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
    Write-Host "  ${Green}OK${Reset} Installed: $installedVersion"
} catch {
    Write-Host ""
    Write-Host "  ${Red}The program downloaded but won't run.${Reset}"
    Write-Host ""
    Write-Host "  This usually means Windows Defender or antivirus blocked it."
    Write-Host "  Try these steps:"
    Write-Host ""
    Write-Host "  1. Open Windows Security"
    Write-Host "  2. Go to Virus & threat protection > Protection history"
    Write-Host "  3. Look for 'same.exe' and click 'Allow'"
    Write-Host ""
    Write-Host "  Or manually unblock: Right-click same.exe > Properties > Unblock"
    Write-Host "  File location: $OutputFile"
    Write-Host ""
    Write-Host "  Still stuck? Discord: https://discord.gg/9KfTkcGs7g"
    exit 1
}

Write-Host ""

# ─────────────────────────────────────────────────────────────
# Step 4: Add to PATH
# ─────────────────────────────────────────────────────────────

Write-Host "[4/5] Setting up your terminal..."
Write-Host ""

$CurrentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($CurrentPath -notlike "*$InstallDir*") {
    $NewPath = "$InstallDir;$CurrentPath"
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    Write-Host "  Added SAME to your PATH (permanent)."
}

# Also add to current session so user can use it immediately
$env:Path = "$InstallDir;$env:Path"
Write-Host "  SAME is available in this terminal session."

Write-Host ""

# ─────────────────────────────────────────────────────────────
# Step 5: Check dependencies
# ─────────────────────────────────────────────────────────────

Write-Host "[5/5] Checking dependencies..."
Write-Host ""

$MissingOllama = $false
$MissingNode = $false

# Ollama - try multiple detection methods
$ollamaFound = $false

# Method 1: Check if ollama command exists
if (Get-Command ollama -ErrorAction SilentlyContinue) {
    $ollamaFound = $true
}

# Method 2: Check if Ollama process is running
if (-not $ollamaFound) {
    $ollamaProcess = Get-Process -Name "ollama*" -ErrorAction SilentlyContinue
    if ($ollamaProcess) { $ollamaFound = $true }
}

# Method 3: Check if Ollama API is responding
if (-not $ollamaFound) {
    try {
        $response = Invoke-WebRequest -Uri "http://localhost:11434/api/tags" -TimeoutSec 2 -UseBasicParsing -ErrorAction SilentlyContinue
        if ($response.StatusCode -eq 200) { $ollamaFound = $true }
    } catch { }
}

if ($ollamaFound) {
    Write-Host "  ${Green}OK${Reset} Ollama installed"
} else {
    $MissingOllama = $true
    Write-Host "  ${Yellow}!!${Reset} Ollama not installed"
    Write-Host "    1. Open: https://ollama.com"
    Write-Host "       ${Dim}(Ctrl+click the link to open in browser)${Reset}"
    Write-Host "    2. Click 'Download for Windows' and run the installer"
    Write-Host "    3. Look for the llama icon in your system tray"
    Write-Host "       ${Dim}(bottom-right corner, may be in hidden icons)${Reset}"
}

# Node.js
if (Get-Command node -ErrorAction SilentlyContinue) {
    Write-Host "  ${Green}OK${Reset} Node.js installed"
} else {
    $MissingNode = $true
    Write-Host "  ${Yellow}!!${Reset} Node.js not installed"
    Write-Host "    Download from: https://nodejs.org"
    Write-Host "    ${Dim}(Get the LTS version, run the installer)${Reset}"
}

Write-Host ""

# ─────────────────────────────────────────────────────────────
# What's Next
# ─────────────────────────────────────────────────────────────

Write-Host "-----------------------------------------------------------"
Write-Host ""
Write-Host "  ${Bold}WHAT'S NEXT?${Reset}"
Write-Host ""

if (-not $MissingOllama -and -not $MissingNode) {
    Write-Host "  Everything's ready! Run:"
    Write-Host ""
    Write-Host "    ${Bold}same init${Reset}"
    Write-Host ""
    Write-Host "  This walks you through setup step by step."
} elseif ($MissingOllama -and $MissingNode) {
    Write-Host "  SAME is installed! Before running 'same init', you'll need:"
    Write-Host ""
    Write-Host "    - Ollama  - https://ollama.com"
    Write-Host "    - Node.js - https://nodejs.org"
    Write-Host ""
    Write-Host "  Install those, then run:"
    Write-Host ""
    Write-Host "    ${Bold}same init${Reset}"
} elseif ($MissingOllama) {
    Write-Host "  Almost there! Install Ollama first:"
    Write-Host "    https://ollama.com"
    Write-Host ""
    Write-Host "  Then run:"
    Write-Host ""
    Write-Host "    ${Bold}same init${Reset}"
} elseif ($MissingNode) {
    Write-Host "  SAME is installed! Install Node.js for full AI tool integration:"
    Write-Host "    https://nodejs.org"
    Write-Host ""
    Write-Host "  Then run:"
    Write-Host ""
    Write-Host "    ${Bold}same init${Reset}"
}

Write-Host ""
Write-Host "  ${Dim}You can run 'same init' right now - no need to restart the terminal!${Reset}"
Write-Host ""
Write-Host "  Questions? https://discord.gg/9KfTkcGs7g"
Write-Host ""
