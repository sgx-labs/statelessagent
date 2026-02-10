#!/bin/bash
# SAME installer — friendly edition for humans
# Usage: curl -fsSL statelessagent.com/install.sh | bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────
# Colors
# ─────────────────────────────────────────────────────────────

# Red gradient using 256-color mode (brightest to darkest)
R1='\033[38;5;196m'  # bright red
R2='\033[38;5;160m'
R3='\033[38;5;124m'
R4='\033[38;5;088m'  # dark red
DIM='\033[2m'
BOLD='\033[1m'
GREEN='\033[32m'
YELLOW='\033[33m'
RESET='\033[0m'

# ─────────────────────────────────────────────────────────────
# Interactive detection
# ─────────────────────────────────────────────────────────────
# When piped (curl | bash), stdin is the pipe, not the terminal.
# All read prompts would silently fail. Detect this early.
INTERACTIVE=false
if [ -t 0 ]; then
  INTERACTIVE=true
fi

# Helper: prompt only in interactive mode, auto-yes when piped
confirm_install() {
  local question="$1"
  local action_msg="$2"
  if [ "$INTERACTIVE" = true ]; then
    printf "%b" "$question [Y/n] "
    read -r yn </dev/tty 2>/dev/null || yn=""
    case "$yn" in [Nn]*) return 1 ;; esac
  else
    echo "$action_msg"
  fi
  return 0
}

# ─────────────────────────────────────────────────────────────
# Banner
# ─────────────────────────────────────────────────────────────

echo ""
echo -e "${R1}  ███████╗████████╗ █████╗ ████████╗███████╗██╗     ███████╗███████╗███████╗${RESET}"
echo -e "${R1}  ██╔════╝╚══██╔══╝██╔══██╗╚══██╔══╝██╔════╝██║     ██╔════╝██╔════╝██╔════╝${RESET}"
echo -e "${R2}  ███████╗   ██║   ███████║   ██║   █████╗  ██║     █████╗  ███████╗███████╗${RESET}"
echo -e "${R2}  ╚════██║   ██║   ██╔══██║   ██║   ██╔══╝  ██║     ██╔══╝  ╚════██║╚════██║${RESET}"
echo -e "${R3}  ███████║   ██║   ██║  ██║   ██║   ███████╗███████╗███████╗███████║███████║${RESET}"
echo -e "${R3}  ╚══════╝   ╚═╝   ╚═╝  ╚═╝   ╚═╝   ╚══════╝╚══════╝╚══════╝╚══════╝╚══════╝${RESET}"
echo -e "${R3}           █████╗  ██████╗ ███████╗███╗   ██╗████████╗${RESET}"
echo -e "${R4}          ██╔══██╗██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝${RESET}"
echo -e "${R4}          ███████║██║  ███╗█████╗  ██╔██╗ ██║   ██║${RESET}"
echo -e "${R4}          ██╔══██║██║   ██║██╔══╝  ██║╚██╗██║   ██║${RESET}"
echo -e "${R4}          ██║  ██║╚██████╔╝███████╗██║ ╚████║   ██║${RESET}"
echo -e "${R4}          ╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝${RESET}"
echo ""
echo -e "${DIM}  Every AI session starts from zero.${RESET} ${BOLD}${R1}Not anymore.${RESET}"
echo ""

# ─────────────────────────────────────────────────────────────
# Step 1: Figure out what kind of computer you have
# ─────────────────────────────────────────────────────────────

echo "[1/5] Detecting your system..."
echo ""

OS="$(uname -s)"
ARCH="$(uname -m)"
MUSL=false

case "$OS" in
  Darwin)
    OS_NAME="macOS"
    case "$ARCH" in
      arm64)
        SUFFIX="darwin-arm64"
        ARCH_NAME="Apple Silicon (M1/M2/M3)"
        ;;
      x86_64)
        SUFFIX="darwin-arm64"
        ARCH_NAME="Intel (via Rosetta)"
        echo -e "  ${YELLOW}!${RESET} Intel Mac detected. Installing ARM binary (runs via Rosetta)."
        echo "    For a native build: git clone and 'make install'."
        echo ""
        ;;
      *)
        echo "  Hmm, I don't recognize your Mac's processor: $ARCH"
        echo "  This is unusual. Please ask for help in our Discord:"
        echo "  https://discord.gg/GZGHtrrKF2"
        exit 1
        ;;
    esac
    ;;
  Linux)
    OS_NAME="Linux"
    # Check for musl libc (Alpine, etc.)
    if ldd --version 2>&1 | grep -qi musl; then
      MUSL=true
    fi
    case "$ARCH" in
      x86_64)
        SUFFIX="linux-amd64"
        ARCH_NAME="64-bit"
        if [ "$MUSL" = true ]; then
          echo -e "  ${YELLOW}!${RESET} musl libc detected (Alpine/lightweight distro)."
          echo "    Pre-built binary requires glibc. Will try build from source."
          echo ""
        fi
        ;;
      aarch64|arm64)
        SUFFIX="linux-arm64"
        ARCH_NAME="ARM 64-bit"
        echo -e "  ${YELLOW}!${RESET} ARM Linux detected. No pre-built binary available."
        echo "    Will try to build from source."
        echo ""
        ;;
      *)
        echo "  I only support 64-bit Linux (x86_64 and ARM64) right now."
        echo "  Your system reports: $ARCH"
        echo "  Please ask for help in our Discord:"
        echo "  https://discord.gg/GZGHtrrKF2"
        exit 1
        ;;
    esac
    ;;
  MINGW*|MSYS*|CYGWIN*|Windows*)
    OS_NAME="Windows"
    SUFFIX="windows-amd64.exe"
    ARCH_NAME="64-bit"
    ;;
  *)
    echo "  I don't recognize your operating system: $OS"
    echo "  SAME works on macOS, Linux, and Windows."
    echo "  Please ask for help in our Discord:"
    echo "  https://discord.gg/GZGHtrrKF2"
    exit 1
    ;;
esac

echo "  Found: $OS_NAME ($ARCH_NAME)"
echo ""

# ─────────────────────────────────────────────────────────────
# Step 2: Download SAME (with build-from-source fallback)
# ─────────────────────────────────────────────────────────────

echo "[2/5] Getting SAME..."
echo ""

REPO="sgx-labs/statelessagent"
INSTALL_DIR="$HOME/.local/bin"
OUTPUT="$INSTALL_DIR/same"
if [[ "$SUFFIX" == *".exe" ]]; then
  OUTPUT="$INSTALL_DIR/same.exe"
fi

# Create install dir early
mkdir -p "$INSTALL_DIR"

# ── Build from source ──────────────────────────────────────
build_prereqs_ok() {
  command -v git >/dev/null 2>&1 || return 1
  command -v go >/dev/null 2>&1 || return 1

  # Check Go version >= 1.23
  local go_ver
  go_ver=$(go version | grep -oE '[0-9]+\.[0-9]+' | head -1)
  local go_minor
  go_minor=$(echo "$go_ver" | cut -d. -f2)
  [ "$go_minor" -ge 23 ] 2>/dev/null || return 1

  # Check for C compiler (needed for CGO/SQLite)
  command -v gcc >/dev/null 2>&1 || command -v cc >/dev/null 2>&1 || return 1
  return 0
}

build_from_source() {
  local temp_dir
  temp_dir=$(mktemp -d)
  echo "  Cloning repository..."
  if ! git clone --depth 1 "https://github.com/$REPO.git" "$temp_dir/same" 2>/dev/null; then
    echo "  Clone failed. Check your internet connection."
    rm -rf "$temp_dir"
    return 1
  fi
  echo "  Building (this may take a minute)..."
  if ! (cd "$temp_dir/same" && CGO_ENABLED=1 go build -o same ./cmd/same/); then
    echo "  Build failed."
    rm -rf "$temp_dir"
    return 1
  fi
  mv "$temp_dir/same/same" "$OUTPUT"
  rm -rf "$temp_dir"
  return 0
}

no_binary_error() {
  echo ""
  echo "  I couldn't download SAME and can't build from source."
  echo ""
  echo "  You have three options:"
  echo ""
  echo "  1. Try again later (GitHub may be temporarily down)"
  echo "     curl -fsSL https://statelessagent.com/install.sh | bash"
  echo ""
  echo "  2. Install Go and build from source"
  echo "     https://go.dev/dl/"
  echo "     Then: git clone https://github.com/$REPO.git"
  echo "           cd statelessagent && make install"
  echo ""
  echo "  3. Ask for help"
  echo "     https://discord.gg/GZGHtrrKF2"
  exit 1
}

# ── Decide: download or build ─────────────────────────────
# Skip download attempt for platforms where pre-built binaries won't work
SKIP_DOWNLOAD=false
if [ "$MUSL" = true ]; then
  SKIP_DOWNLOAD=true
fi
if [ "$OS" = "Linux" ] && [ "$ARCH" = "aarch64" -o "$ARCH" = "arm64" ]; then
  SKIP_DOWNLOAD=true
fi

BINARY_ACQUIRED=false

if [ "$SKIP_DOWNLOAD" = false ]; then
  # Strategy 1: Download pre-built binary from GitHub Releases
  echo "  Checking for latest release..."

  LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/' || true)

  if [ -n "$LATEST" ]; then
    echo "  Latest version: $LATEST"
    BINARY_NAME="same-$SUFFIX"
    URL="https://github.com/$REPO/releases/download/$LATEST/$BINARY_NAME"
    TEMP_FILE="/tmp/same-download-$$"

    if curl -fsSL "$URL" -o "$TEMP_FILE" 2>/dev/null; then
      mv "$TEMP_FILE" "$OUTPUT"
      chmod +x "$OUTPUT"
      BINARY_ACQUIRED=true
      echo "  Downloaded successfully."
    else
      rm -f "$TEMP_FILE"
      echo "  Download failed."
    fi
  else
    # Check for network vs no-release
    if curl -fsSL "https://api.github.com" >/dev/null 2>&1; then
      echo "  No release found on GitHub."
    else
      echo -e "  ${YELLOW}!${RESET} Can't reach GitHub."
      echo "    Behind a proxy? Set https_proxy=http://your-proxy:port"
    fi
  fi
fi

if [ "$BINARY_ACQUIRED" = false ]; then
  echo ""
  # Strategy 2: Build from source
  if build_prereqs_ok; then
    GO_VER=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1)
    echo "  $GO_VER found — building from source instead..."
    if build_from_source; then
      BINARY_ACQUIRED=true
    fi
  else
    # Explain what's missing for build
    if ! command -v go >/dev/null 2>&1; then
      echo "  Go not installed (needed to build from source)."
    elif ! command -v git >/dev/null 2>&1; then
      echo "  git not installed (needed to clone source)."
    elif ! command -v gcc >/dev/null 2>&1 && ! command -v cc >/dev/null 2>&1; then
      echo "  No C compiler found (needed for CGO/SQLite)."
      if [ "$OS" = "Darwin" ]; then
        echo "    Install Xcode Command Line Tools: xcode-select --install"
      elif command -v apt-get >/dev/null 2>&1; then
        echo "    Install: sudo apt-get install build-essential"
      elif command -v dnf >/dev/null 2>&1; then
        echo "    Install: sudo dnf install gcc"
      fi
    else
      OLD_GO_VER=$(go version | grep -oE '[0-9]+\.[0-9]+' | head -1)
      echo "  Go $OLD_GO_VER found but SAME needs Go 1.23+."
      echo "  Upgrade: https://go.dev/dl/"
    fi
  fi
fi

if [ "$BINARY_ACQUIRED" = false ]; then
  no_binary_error
fi

echo ""

# ─────────────────────────────────────────────────────────────
# Step 3: Install to your computer
# ─────────────────────────────────────────────────────────────

echo "[3/5] Installing SAME..."
echo ""

echo "  Installing to: $INSTALL_DIR"
echo ""

chmod +x "$OUTPUT"

# On macOS, clear quarantine so it actually runs
if [ "$OS" = "Darwin" ]; then
  xattr -cr "$OUTPUT" 2>/dev/null || true
fi

# On RHEL/SELinux, restore context
if command -v restorecon >/dev/null 2>&1; then
  restorecon "$OUTPUT" 2>/dev/null || true
fi

# Make sure it works
if ! "$OUTPUT" version >/dev/null 2>&1; then
  echo "  Something went wrong — the program downloaded but won't run."
  echo ""
  if [ "$OS" = "Darwin" ]; then
    echo "  If macOS says 'unidentified developer':"
    echo "    Right-click the file → Open, or:"
    echo "    System Settings → Privacy & Security → Allow"
    echo ""
  fi
  echo "  Please share this info in Discord and we'll help:"
  echo "  - OS: $OS_NAME ($ARCH_NAME)"
  echo "  - File: $OUTPUT"
  echo "  https://discord.gg/GZGHtrrKF2"
  exit 1
fi

INSTALLED_VERSION=$("$OUTPUT" version 2>/dev/null)
echo -e "  ${GREEN}✓${RESET} Installed: $INSTALLED_VERSION"
echo ""

# ─────────────────────────────────────────────────────────────
# Step 4: Make sure your computer can find SAME
# ─────────────────────────────────────────────────────────────

echo "[4/5] Setting up your terminal..."
echo ""

# Check if the install directory is already in PATH
if echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  echo "  Your terminal already knows where to find SAME."
else
  echo "  Adding SAME to your PATH..."
  echo ""

  # Detect which shell config file to use
  SHELL_NAME="$(basename "$SHELL" 2>/dev/null || echo "bash")"
  case "$SHELL_NAME" in
    zsh)  RC_FILE="$HOME/.zshrc" ;;
    fish) RC_FILE="$HOME/.config/fish/config.fish" ;;
    *)    RC_FILE="$HOME/.bashrc" ;;
  esac

  # Add to PATH automatically
  if [ "$SHELL_NAME" = "fish" ]; then
    PATH_LINE="fish_add_path $INSTALL_DIR"
  else
    PATH_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
  fi

  # Check if we already added it before
  if grep -q "$INSTALL_DIR" "$RC_FILE" 2>/dev/null; then
    echo "  Already configured in $RC_FILE"
  else
    echo "" >> "$RC_FILE"
    echo "# Added by SAME installer" >> "$RC_FILE"
    echo "$PATH_LINE" >> "$RC_FILE"
    echo "  Added SAME to your PATH in $RC_FILE"
  fi

  # Also add to current session so version check works below
  export PATH="$INSTALL_DIR:$PATH"

  echo ""
  echo "  NOTE: You may need to open a new terminal for 'same'"
  echo "  to be available everywhere."
fi

echo ""

# ─────────────────────────────────────────────────────────────
# Step 5: Check and install dependencies
# ─────────────────────────────────────────────────────────────

echo "[5/5] Checking dependencies..."
echo ""

# ── Homebrew (macOS only) ──────────────────────────────────
# Homebrew is the easiest way to install Ollama and Node on macOS.
# If missing, offer to install it first so the dep checks below can use it.
if [ "$OS" = "Darwin" ] && ! command -v brew >/dev/null 2>&1; then
  if confirm_install "  Homebrew not found. Install it? (makes installing deps easier)" "  Installing Homebrew..."; then
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" </dev/null
    # Homebrew on Apple Silicon installs to /opt/homebrew, add to PATH for this session
    if [ -f /opt/homebrew/bin/brew ]; then
      eval "$(/opt/homebrew/bin/brew shellenv)"
    elif [ -f /usr/local/bin/brew ]; then
      eval "$(/usr/local/bin/brew shellenv)"
    fi
    if command -v brew >/dev/null 2>&1; then
      echo -e "  ${GREEN}✓${RESET} Homebrew installed"
    else
      echo -e "  ${YELLOW}!${RESET} Homebrew install may need a terminal restart"
    fi
    echo ""
  fi
fi

MISSING_OLLAMA=false
MISSING_NODE=false

# ── Ollama ─────────────────────────────────────────────────
if command -v ollama >/dev/null 2>&1; then
  echo -e "  ${GREEN}✓${RESET} Ollama installed"
else
  INSTALLED_OLLAMA=false

  if [ "$OS" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
    if confirm_install "  Install Ollama via Homebrew?" "  Installing Ollama..."; then
      if brew install ollama 2>/dev/null; then
        INSTALLED_OLLAMA=true
      fi
    fi
  elif [ "$OS" = "Linux" ]; then
    if confirm_install "  Install Ollama?" "  Installing Ollama..."; then
      if curl -fsSL https://ollama.com/install.sh | sh 2>/dev/null; then
        INSTALLED_OLLAMA=true
      fi
    fi
  fi

  if [ "$INSTALLED_OLLAMA" = true ] && command -v ollama >/dev/null 2>&1; then
    echo -e "  ${GREEN}✓${RESET} Ollama installed"
  else
    MISSING_OLLAMA=true
    echo -e "  ${YELLOW}✗${RESET} Ollama not installed"
    echo "    Download from: https://ollama.ai"
  fi
fi

# ── Node.js ────────────────────────────────────────────────
if command -v node >/dev/null 2>&1; then
  echo -e "  ${GREEN}✓${RESET} Node.js installed"
else
  INSTALLED_NODE=false

  if [ "$OS" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
    if confirm_install "  Install Node.js via Homebrew?" "  Installing Node.js..."; then
      if brew install node 2>/dev/null; then
        INSTALLED_NODE=true
      fi
    fi
  elif command -v apt-get >/dev/null 2>&1; then
    if confirm_install "  Install Node.js via apt?" "  Installing Node.js..."; then
      if sudo apt-get install -y nodejs npm 2>/dev/null; then
        INSTALLED_NODE=true
      fi
    fi
  elif command -v dnf >/dev/null 2>&1; then
    if confirm_install "  Install Node.js via dnf?" "  Installing Node.js..."; then
      if sudo dnf install -y nodejs 2>/dev/null; then
        INSTALLED_NODE=true
      fi
    fi
  fi

  if [ "$INSTALLED_NODE" = true ] && command -v node >/dev/null 2>&1; then
    echo -e "  ${GREEN}✓${RESET} Node.js installed"
  else
    MISSING_NODE=true
    echo -e "  ${YELLOW}✗${RESET} Node.js not installed"
    echo "    Download from: https://nodejs.org"
  fi
fi

echo ""

# ─────────────────────────────────────────────────────────────
# What's Next — adapt based on what's actually ready
# ─────────────────────────────────────────────────────────────

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "  WHAT'S NEXT?"
echo ""

if [ "$MISSING_OLLAMA" = false ] && [ "$MISSING_NODE" = false ]; then
  # Everything's ready
  echo "  Everything's ready! Run:"
  echo ""
  echo "    same init"
  echo ""
  echo "  This walks you through setup step by step."
elif [ "$MISSING_OLLAMA" = true ] && [ "$MISSING_NODE" = true ]; then
  # Missing both
  echo "  SAME is installed! Before running 'same init', you'll need:"
  echo ""
  echo "    • Ollama  — https://ollama.ai"
  echo "    • Node.js — https://nodejs.org"
  echo ""
  echo "  Install those, then run:"
  echo ""
  echo "    same init"
elif [ "$MISSING_OLLAMA" = true ]; then
  # Missing only Ollama
  echo "  Almost there! Install Ollama first:"
  echo "    https://ollama.ai"
  echo ""
  echo "  Then run:"
  echo ""
  echo "    same init"
elif [ "$MISSING_NODE" = true ]; then
  # Missing only Node
  echo "  SAME is installed! Install Node.js for full AI tool integration:"
  echo "    https://nodejs.org"
  echo ""
  echo "  Then run:"
  echo ""
  echo "    same init"
fi

echo ""
echo "  Questions? Join us: https://discord.gg/GZGHtrrKF2"
echo ""
