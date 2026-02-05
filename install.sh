#!/bin/bash
# SAME installer — downloads the appropriate binary for your platform.
# Usage: curl -fsSL https://raw.githubusercontent.com/sgx-labs/statelessagent/main/install.sh | bash
#
# Options:
#   INSTALL_DIR=/path    Install to a custom directory (default: ~/.local/bin)
#   VERSION=v0.4.0       Install a specific version (default: latest)

set -euo pipefail

REPO="sgx-labs/statelessagent"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-}"

# Detect platform
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Darwin)
    case "$ARCH" in
      arm64) SUFFIX="darwin-arm64" ;;
      x86_64) SUFFIX="darwin-amd64" ;;
      *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
    ;;
  Linux)
    case "$ARCH" in
      x86_64) SUFFIX="linux-amd64" ;;
      *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
    ;;
  MINGW*|MSYS*|CYGWIN*|Windows*)
    SUFFIX="windows-amd64.exe"
    ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# Get release tag (specific version or latest)
if [ -n "$VERSION" ]; then
  LATEST="$VERSION"
  echo "Installing SAME $LATEST..."
else
  echo "Fetching latest release..."
  LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/' || true)
fi

if [ -z "$LATEST" ]; then
  echo "No releases found. Building from source..."
  if command -v go >/dev/null 2>&1; then
    CGO_ENABLED=1 go build -ldflags "-s -w" -o "$INSTALL_DIR/same" ./cmd/same
    echo "Built $INSTALL_DIR/same from source"
    exit 0
  fi
  echo "Go not found. Install Go or wait for a release."
  exit 1
fi

BINARY_NAME="same-$SUFFIX"
URL="https://github.com/$REPO/releases/download/$LATEST/$BINARY_NAME"

echo "Downloading SAME $LATEST for $SUFFIX..."
mkdir -p "$INSTALL_DIR"

OUTPUT="$INSTALL_DIR/same"
if [[ "$SUFFIX" == *".exe" ]]; then
  OUTPUT="$INSTALL_DIR/same.exe"
fi

curl -fsSL "$URL" -o "$OUTPUT"
chmod +x "$OUTPUT"

# Clear macOS quarantine attributes (prevents Gatekeeper from blocking unsigned binaries)
if [ "$OS" = "Darwin" ]; then
  xattr -cr "$OUTPUT" 2>/dev/null || true
fi

# Verify the binary runs
if ! "$OUTPUT" version >/dev/null 2>&1; then
  echo "WARNING: Binary downloaded but failed to execute."
  echo "  This may indicate a platform mismatch or missing dependencies."
  echo "  Try building from source: go install github.com/$REPO/cmd/same@latest"
  exit 1
fi

INSTALLED_VERSION=$("$OUTPUT" version 2>/dev/null || echo "$LATEST")

echo ""
echo "Installed: $OUTPUT"
echo "Version:   $INSTALLED_VERSION"

# Check if install directory is in PATH
if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  echo ""
  echo "NOTE: $INSTALL_DIR is not in your PATH."
  # Detect shell
  SHELL_NAME="$(basename "$SHELL" 2>/dev/null || echo "bash")"
  case "$SHELL_NAME" in
    zsh)  RC_FILE="~/.zshrc" ;;
    fish) RC_FILE="~/.config/fish/config.fish" ;;
    *)    RC_FILE="~/.bashrc" ;;
  esac
  if [ "$SHELL_NAME" = "fish" ]; then
    echo "  Add to $RC_FILE:"
    echo "    fish_add_path $INSTALL_DIR"
  else
    echo "  Add to $RC_FILE:"
    echo "    export PATH=\"$INSTALL_DIR:\$PATH\""
  fi
fi

# Check for Ollama
echo ""
if command -v ollama >/dev/null 2>&1; then
  echo "Ollama: found"
else
  echo "Ollama: not found (required for embeddings)"
  echo "  Install: https://ollama.ai"
fi

echo ""
echo "Next step: cd to your notes directory and run 'same init'"
echo ""
