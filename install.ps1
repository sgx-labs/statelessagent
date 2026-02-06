# SAME installer for Windows PowerShell
# Usage: irm https://statelessagent.com/install.ps1 | iex

$ErrorActionPreference = "Stop"

# Colors
$Red = "`e[91m"
$DarkRed = "`e[31m"
$Dim = "`e[2m"
$Bold = "`e[1m"
$Reset = "`e[0m"

# Banner
Write-Host ""
Write-Host "${Red}  STATELESS AGENT${Reset}"
Write-Host "${Dim}  Every AI session starts from zero.${Reset} ${Bold}${Red}Not anymore.${Reset}"
Write-Host ""

# Step 1: Detect system
Write-Host "[1/4] Detecting your system..."
Write-Host ""

$arch = [System.Environment]::Is64BitOperatingSystem
if (-not $arch) {
    Write-Host "  SAME requires 64-bit Windows."
    Write-Host "  Please ask for help: https://discord.gg/GZGHtrrKF2"
    exit 1
}

Write-Host "  Found: Windows (64-bit)"
Write-Host "  Perfect, I have a version for you."
Write-Host ""

# Step 2: Download
Write-Host "[2/4] Downloading SAME..."
Write-Host ""

$repo = "sgx-labs/statelessagent"

try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name
} catch {
    Write-Host "  Couldn't reach GitHub to get the latest version."
    Write-Host "  Check your internet connection and try again."
    Write-Host "  https://discord.gg/GZGHtrrKF2"
    exit 1
}

Write-Host "  Latest version: $version"

$binaryName = "same-windows-amd64.exe"
$url = "https://github.com/$repo/releases/download/$version/$binaryName"
$tempFile = Join-Path $env:TEMP "same-download.exe"

try {
    Invoke-WebRequest -Uri $url -OutFile $tempFile -UseBasicParsing
} catch {
    Write-Host ""
    Write-Host "  Download failed. This might mean:"
    Write-Host "  - Your internet connection dropped"
    Write-Host "  - GitHub is having issues"
    Write-Host ""
    Write-Host "  Try again in a minute. If it keeps failing:"
    Write-Host "  https://discord.gg/GZGHtrrKF2"
    exit 1
}

Write-Host "  Downloaded successfully."
Write-Host ""

# Step 3: Install
Write-Host "[3/4] Installing SAME..."
Write-Host ""

$installDir = Join-Path $env:LOCALAPPDATA "Programs\SAME"

Write-Host "  I'm going to put SAME in:"
Write-Host "  $installDir"
Write-Host ""

if (-not (Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    Write-Host "  Created that folder."
}

$output = Join-Path $installDir "same.exe"
Move-Item -Path $tempFile -Destination $output -Force

# Verify it works
try {
    $installedVersion = & $output version 2>$null
    Write-Host "  Installed: $installedVersion"
} catch {
    Write-Host "  Something went wrong - the program downloaded but won't run."
    Write-Host "  Please share this in Discord: https://discord.gg/GZGHtrrKF2"
    exit 1
}

Write-Host ""

# Step 4: Add to PATH
Write-Host "[4/4] Setting up your terminal..."
Write-Host ""

$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$installDir*") {
    $newPath = "$installDir;$currentPath"
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "  Added SAME to your PATH."
    Write-Host ""
    Write-Host "  IMPORTANT: Close this PowerShell window and open a new one"
    Write-Host "             for the changes to take effect."
} else {
    Write-Host "  SAME is already in your PATH."
}

Write-Host ""

# Check for Ollama
Write-Host "-----------------------------------------------------------"
Write-Host ""

$ollamaExists = Get-Command ollama -ErrorAction SilentlyContinue
if ($ollamaExists) {
    Write-Host "  [OK] Ollama is installed"
    Write-Host ""
    Write-Host "  SAME is ready to use!"
} else {
    Write-Host "  [!] Ollama is not installed yet"
    Write-Host ""
    Write-Host "  SAME needs Ollama to work. It's free and takes about 2 minutes:"
    Write-Host ""
    Write-Host "  1. Open: https://ollama.ai"
    Write-Host "  2. Click 'Download' and run the installer"
    Write-Host "  3. You'll see a llama icon in your system tray when it's running"
    Write-Host ""
    Write-Host "  Stuck? Join our Discord: https://discord.gg/GZGHtrrKF2"
}

Write-Host ""
Write-Host "-----------------------------------------------------------"
Write-Host ""
Write-Host "  WHAT'S NEXT?"
Write-Host ""
Write-Host "  1. Close this PowerShell window and open a new one"
Write-Host ""
Write-Host "  2. Navigate to your project folder:"
Write-Host "     cd C:\Users\YourName\Documents\my-project"
Write-Host ""
Write-Host "  3. Run the setup wizard:"
Write-Host "     same init"
Write-Host ""
Write-Host "  Questions? Join us: https://discord.gg/GZGHtrrKF2"
Write-Host ""
