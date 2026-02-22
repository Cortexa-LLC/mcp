#!/usr/bin/env bash
# install.sh — build and install markitdown-mcp, optionally install Tesseract.
#
# Usage:
#   ./install.sh              # build only
#   ./install.sh --with-ocr   # install Tesseract then build
#   ./install.sh --help

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="$REPO_ROOT/src/markitdown"
BIN_NAME="markitdown-mcp"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# ── colour helpers ────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; RESET='\033[0m'
else
  GREEN=''; YELLOW=''; RED=''; RESET=''
fi

info()  { echo -e "${GREEN}==>${RESET} $*"; }
warn()  { echo -e "${YELLOW}warn:${RESET} $*"; }
error() { echo -e "${RED}error:${RESET} $*" >&2; }
die()   { error "$*"; exit 1; }

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Options:
  --with-ocr    Install Tesseract OCR engine before building
  --prefix DIR  Install binary to DIR (default: $INSTALL_DIR)
  --help        Show this message

Environment:
  INSTALL_DIR   Override the install directory
EOF
}

# ── argument parsing ──────────────────────────────────────────────────────────
WITH_OCR=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --with-ocr) WITH_OCR=true; shift ;;
    --prefix)   INSTALL_DIR="$2"; shift 2 ;;
    --help|-h)  usage; exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done

# ── detect OS / package manager ───────────────────────────────────────────────
OS="$(uname -s)"
case "$OS" in
  Darwin)  PLATFORM="macOS" ;;
  Linux)   PLATFORM="Linux" ;;
  MINGW*|MSYS*|CYGWIN*) PLATFORM="Windows" ;;
  *) PLATFORM="unknown ($OS)" ;;
esac

detect_pkg_manager() {
  if command -v brew &>/dev/null; then echo "brew"
  elif command -v apt-get &>/dev/null; then echo "apt"
  elif command -v dnf &>/dev/null; then echo "dnf"
  elif command -v yum &>/dev/null; then echo "yum"
  elif command -v pacman &>/dev/null; then echo "pacman"
  elif command -v zypper &>/dev/null; then echo "zypper"
  elif command -v choco &>/dev/null; then echo "choco"
  else echo "unknown"
  fi
}

# ── check Go ──────────────────────────────────────────────────────────────────
if ! command -v go &>/dev/null; then
  die "Go is not installed. Download it from https://go.dev/dl/ and re-run."
fi

GO_VERSION="$(go version | awk '{print $3}' | tr -d 'go')"
REQUIRED_MAJOR=1; REQUIRED_MINOR=24
IFS='.' read -r maj min _ <<< "$GO_VERSION"
if (( maj < REQUIRED_MAJOR || (maj == REQUIRED_MAJOR && min < REQUIRED_MINOR) )); then
  die "Go $REQUIRED_MAJOR.$REQUIRED_MINOR+ required, found $GO_VERSION"
fi
info "Go $GO_VERSION found"

# ── optional: install Tesseract ───────────────────────────────────────────────
install_tesseract() {
  local pm
  pm="$(detect_pkg_manager)"
  info "Installing Tesseract using package manager: $pm"
  case "$pm" in
    brew)   brew install tesseract ;;
    apt)    sudo apt-get update -q && sudo apt-get install -y tesseract-ocr ;;
    dnf)    sudo dnf install -y tesseract ;;
    yum)    sudo yum install -y tesseract ;;
    pacman) sudo pacman -Sy --noconfirm tesseract tesseract-data-eng ;;
    zypper) sudo zypper install -y tesseract-ocr ;;
    choco)  choco install -y tesseract ;;
    *)
      warn "Could not detect a supported package manager."
      warn "Install Tesseract manually: https://github.com/tesseract-ocr/tesseract#installing-tesseract"
      return 1
      ;;
  esac
}

if $WITH_OCR; then
  if command -v tesseract &>/dev/null; then
    info "Tesseract already installed: $(tesseract --version 2>&1 | head -1)"
  else
    install_tesseract
    info "Tesseract installed: $(tesseract --version 2>&1 | head -1)"
  fi
else
  if command -v tesseract &>/dev/null; then
    info "Tesseract found: $(tesseract --version 2>&1 | head -1) — OCR enabled"
  else
    warn "Tesseract not found. Image OCR will be unavailable."
    warn "Re-run with --with-ocr to install, or install manually and restart the server."
  fi
fi

# ── build ─────────────────────────────────────────────────────────────────────
info "Building $BIN_NAME..."
(
  cd "$SRC_DIR"
  go mod tidy -e
  go build -ldflags="-s -w" -o "$BIN_NAME" .
)

BIN_PATH="$SRC_DIR/$BIN_NAME"
info "Built: $BIN_PATH"

# ── install binary ────────────────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"
cp "$BIN_PATH" "$INSTALL_DIR/$BIN_NAME"
info "Installed to: $INSTALL_DIR/$BIN_NAME"

FULL_PATH="$INSTALL_DIR/$BIN_NAME"

# Warn if the install dir is not on PATH
if ! echo ":$PATH:" | grep -q ":$INSTALL_DIR:"; then
  warn "$INSTALL_DIR is not on your PATH."
  warn "Add this to your shell profile: export PATH=\"$INSTALL_DIR:\$PATH\""
fi

# ── print MCP config snippet ──────────────────────────────────────────────────
echo ""
echo "────────────────────────────────────────────────────"
echo "  Add to your MCP client configuration:"
echo "────────────────────────────────────────────────────"
cat <<EOF

{
  "mcpServers": {
    "markitdown": {
      "command": "$FULL_PATH"
    }
  }
}

EOF

case "$PLATFORM" in
  macOS)
    CONFIG="$HOME/Library/Application Support/Claude/claude_desktop_config.json"
    ;;
  Linux)
    CONFIG="$HOME/.config/Claude/claude_desktop_config.json"
    ;;
  Windows)
    CONFIG='%APPDATA%\Claude\claude_desktop_config.json'
    ;;
  *)
    CONFIG="your MCP client config file"
    ;;
esac

echo "Claude Desktop config: $CONFIG"
echo "Claude Code config:    .mcp.json in your project root"
echo ""
info "Done. Restart your MCP client to pick up the new server."
