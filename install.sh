#!/bin/bash
# SAME installer — friendly edition for humans
# Usage: curl -fsSL statelessagent.com/install.sh | bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────
# Welcome!
# ─────────────────────────────────────────────────────────────

echo ""
echo "  ╔═══════════════════════════════════════════════════════╗"
echo "  ║                                                       ║"
echo "  ║   SAME Installer                                      ║"
echo "  ║   Stateless Agent Memory Engine                       ║"
echo "  ║                                                       ║"
echo "  ║   Your AI is about to get a memory.                   ║"
echo "  ║                                                       ║"
echo "  ╚═══════════════════════════════════════════════════════╝"
echo ""

# ─────────────────────────────────────────────────────────────
# Step 1: Figure out what kind of computer you have
# ─────────────────────────────────────────────────────────────

echo "[1/4] Detecting your system..."
echo ""

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Darwin)
    OS_NAME="macOS"
    case "$ARCH" in
      arm64)
        SUFFIX="darwin-arm64"
        ARCH_NAME="Apple Silicon (M1/M2/M3)"
        ;;
      x86_64)
        SUFFIX="darwin-amd64"
        ARCH_NAME="Intel"
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
    case "$ARCH" in
      x86_64)
        SUFFIX="linux-amd64"
        ARCH_NAME="64-bit"
        ;;
      *)
        echo "  I only support 64-bit Linux right now."
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
echo "  Perfect, I have a version for you."
echo ""

# ─────────────────────────────────────────────────────────────
# Step 2: Download SAME
# ─────────────────────────────────────────────────────────────

echo "[2/4] Downloading SAME..."
echo ""

REPO="sgx-labs/statelessagent"

# Get the latest version number from GitHub
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/' || true)

if [ -z "$LATEST" ]; then
  echo "  Couldn't reach GitHub to get the latest version."
  echo "  Check your internet connection and try again."
  echo ""
  echo "  If this keeps happening, ask for help:"
  echo "  https://discord.gg/GZGHtrrKF2"
  exit 1
fi

echo "  Latest version: $LATEST"

BINARY_NAME="same-$SUFFIX"
URL="https://github.com/$REPO/releases/download/$LATEST/$BINARY_NAME"

# Download to a temp location first
TEMP_FILE="/tmp/same-download-$$"
if ! curl -fsSL "$URL" -o "$TEMP_FILE" 2>/dev/null; then
  echo ""
  echo "  Download failed. This might mean:"
  echo "  - Your internet connection dropped"
  echo "  - GitHub is having issues"
  echo "  - The release doesn't have a build for your system"
  echo ""
  echo "  Try again in a minute. If it keeps failing:"
  echo "  https://discord.gg/GZGHtrrKF2"
  rm -f "$TEMP_FILE"
  exit 1
fi

echo "  Downloaded successfully."
echo ""

# ─────────────────────────────────────────────────────────────
# Step 3: Install to your computer
# ─────────────────────────────────────────────────────────────

echo "[3/4] Installing SAME..."
echo ""

# Where to put it
INSTALL_DIR="$HOME/.local/bin"

echo "  I'm going to put SAME in a folder called:"
echo "  $INSTALL_DIR"
echo ""
echo "  This is a standard place for personal programs."
echo "  It won't affect anything else on your computer."
echo ""

# Create the directory if it doesn't exist
if [ ! -d "$INSTALL_DIR" ]; then
  echo "  Creating that folder now..."
  mkdir -p "$INSTALL_DIR"
fi

# Move the binary into place
OUTPUT="$INSTALL_DIR/same"
if [[ "$SUFFIX" == *".exe" ]]; then
  OUTPUT="$INSTALL_DIR/same.exe"
fi

mv "$TEMP_FILE" "$OUTPUT"
chmod +x "$OUTPUT"

# On macOS, clear quarantine so it actually runs
if [ "$OS" = "Darwin" ]; then
  xattr -cr "$OUTPUT" 2>/dev/null || true
fi

# Make sure it works
if ! "$OUTPUT" version >/dev/null 2>&1; then
  echo "  Something went wrong — the program downloaded but won't run."
  echo ""
  echo "  This is rare. Please share this info in Discord and we'll help:"
  echo "  - OS: $OS_NAME ($ARCH_NAME)"
  echo "  - File: $OUTPUT"
  echo "  https://discord.gg/GZGHtrrKF2"
  exit 1
fi

INSTALLED_VERSION=$("$OUTPUT" version 2>/dev/null)
echo "  Installed: $INSTALLED_VERSION"
echo ""

# ─────────────────────────────────────────────────────────────
# Step 4: Make sure your computer can find SAME
# ─────────────────────────────────────────────────────────────

echo "[4/4] Setting up your terminal..."
echo ""

# Check if the install directory is already in PATH
if echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  echo "  Your terminal already knows where to find SAME."
  echo "  You're all set!"
else
  echo "  Your terminal doesn't know where SAME is yet."
  echo "  Let me fix that for you..."
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

  echo ""
  echo "  IMPORTANT: To start using SAME, you need to do ONE of these:"
  echo ""
  echo "    Option A (Easiest): Close this terminal window completely,"
  echo "                        then open a brand new one."
  echo ""
  echo "    Option B (Quick):   Copy and paste this command:"
  echo "                        source $RC_FILE"
  echo ""
  echo "  Not sure? Just close this window and open a new one."
fi

echo ""

# ─────────────────────────────────────────────────────────────
# Check for Ollama (required dependency)
# ─────────────────────────────────────────────────────────────

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if command -v ollama >/dev/null 2>&1; then
  echo "  ✓ Ollama is installed"
  echo ""
  echo "  SAME is ready to use!"
else
  echo "  ⚠ Ollama is not installed yet"
  echo ""
  echo "  SAME needs Ollama to work. It's free and takes about 2 minutes:"
  echo ""
  echo "  1. Open this link in your browser: https://ollama.ai"
  echo "  2. Click the big 'Download' button"
  echo "  3. Open the downloaded file to install it (like any other app)"
  echo "  4. When Ollama opens, you'll see a llama icon in your menu bar"
  echo "     (Mac: top right corner, Windows: bottom right system tray)"
  echo ""
  echo "  Once you see the llama icon, Ollama is running and you're ready!"
  echo ""
  echo "  Stuck? Join our Discord and we'll help: https://discord.gg/GZGHtrrKF2"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "  WHAT'S NEXT?"
echo ""
echo "  1. Open a NEW terminal window (close this one first)"
echo ""
echo "  2. Navigate to your project folder. For example:"
echo "     cd ~/Documents/my-project"
echo ""
echo "     (Replace 'my-project' with your actual folder name)"
echo ""
echo "  3. Run the setup wizard:"
echo "     same init"
echo ""
echo "     This walks you through everything step by step."
echo ""
echo "  That's it! SAME will connect to your project automatically."
echo ""
echo "  Questions? Join us: https://discord.gg/GZGHtrrKF2"
echo ""
