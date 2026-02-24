#!/usr/bin/env python3
"""install.py — build and install markitdown-mcp, optionally install Tesseract.

Usage:
    python3 install.py              # build only
    python3 install.py --with-ocr   # install Tesseract then build
    python3 install.py --help
"""

import argparse
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

# ── constants ──────────────────────────────────────────────────────────────────
REQUIRED_GO_MAJOR = 1
REQUIRED_GO_MINOR = 24
REPO_ROOT = Path(__file__).resolve().parent
SRC_DIR = REPO_ROOT / "src" / "markitdown"
IS_WINDOWS = platform.system() == "Windows"
BIN_NAME = "markitdown-mcp.exe" if IS_WINDOWS else "markitdown-mcp"

# ── colour helpers ─────────────────────────────────────────────────────────────
_USE_COLOR = sys.stdout.isatty()

def _c(code: str, text: str) -> str:
    return f"\033[{code}m{text}\033[0m" if _USE_COLOR else text

def info(msg: str)  -> None: print(f"{_c('0;32', '==>')} {msg}")
def warn(msg: str)  -> None: print(f"{_c('1;33', 'warn:')} {msg}")
def error(msg: str) -> None: print(f"{_c('0;31', 'error:')} {msg}", file=sys.stderr)
def die(msg: str)   -> None: error(msg); sys.exit(1)

# ── subprocess helpers ─────────────────────────────────────────────────────────
def run(*cmd: str, cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, cwd=cwd, check=check, text=True, capture_output=True)

def which(name: str) -> bool:
    return shutil.which(name) is not None

# ── Go version check ───────────────────────────────────────────────────────────
def check_go() -> None:
    if not which("go"):
        die("Go is not installed. Download it from https://go.dev/dl/ and re-run.")
    result = run("go", "version")
    # output: "go version go1.24.0 darwin/arm64"
    version_str = result.stdout.split()[2].lstrip("go")  # "1.24.0"
    major, minor = int(version_str.split(".")[0]), int(version_str.split(".")[1])
    if major < REQUIRED_GO_MAJOR or (major == REQUIRED_GO_MAJOR and minor < REQUIRED_GO_MINOR):
        die(f"Go {REQUIRED_GO_MAJOR}.{REQUIRED_GO_MINOR}+ required, found {version_str}")
    info(f"Go {version_str} found")

# ── package manager detection ──────────────────────────────────────────────────
def detect_pkg_manager() -> str:
    for pm in ("brew", "apt-get", "dnf", "yum", "pacman", "zypper", "choco"):
        if which(pm):
            return pm
    return "unknown"

# ── Tesseract ──────────────────────────────────────────────────────────────────
def install_tesseract() -> None:
    pm = detect_pkg_manager()
    info(f"Installing Tesseract using package manager: {pm}")
    try:
        if pm == "brew":
            subprocess.run(["brew", "install", "tesseract"], check=True)
        elif pm == "apt-get":
            subprocess.run(["sudo", "apt-get", "update", "-q"], check=True)
            subprocess.run(["sudo", "apt-get", "install", "-y", "tesseract-ocr"], check=True)
        elif pm == "dnf":
            subprocess.run(["sudo", "dnf", "install", "-y", "tesseract"], check=True)
        elif pm == "yum":
            subprocess.run(["sudo", "yum", "install", "-y", "tesseract"], check=True)
        elif pm == "pacman":
            subprocess.run(["sudo", "pacman", "-Sy", "--noconfirm", "tesseract", "tesseract-data-eng"], check=True)
        elif pm == "zypper":
            subprocess.run(["sudo", "zypper", "install", "-y", "tesseract-ocr"], check=True)
        elif pm == "choco":
            subprocess.run(["choco", "install", "-y", "tesseract"], check=True)
        else:
            warn("Could not detect a supported package manager.")
            warn("Install Tesseract manually: https://github.com/tesseract-ocr/tesseract#installing-tesseract")
    except subprocess.CalledProcessError as e:
        die(f"Failed to install Tesseract: {e}")

def tesseract_version() -> str:
    result = run("tesseract", "--version", check=False)
    output = result.stdout or result.stderr  # tesseract prints version to stderr on some builds
    return output.splitlines()[0] if output else "unknown"

def check_tesseract(with_ocr: bool) -> None:
    if with_ocr:
        if which("tesseract"):
            info(f"Tesseract already installed: {tesseract_version()}")
        else:
            install_tesseract()
            info(f"Tesseract installed: {tesseract_version()}")
    else:
        if which("tesseract"):
            info(f"Tesseract found: {tesseract_version()} — OCR enabled")
        else:
            warn("Tesseract not found. Image OCR will be unavailable.")
            warn("Re-run with --with-ocr to install, or install manually and restart the server.")

# ── build ──────────────────────────────────────────────────────────────────────
def build() -> Path:
    info(f"Building {BIN_NAME}...")
    try:
        run("go", "mod", "tidy", "-e", cwd=SRC_DIR)
        run("go", "build", "-ldflags=-s -w", f"-o={BIN_NAME}", ".", cwd=SRC_DIR)
    except subprocess.CalledProcessError as e:
        die(f"Build failed: {e}")
    bin_path = SRC_DIR / BIN_NAME
    info(f"Built: {bin_path}")
    return bin_path

# ── install ────────────────────────────────────────────────────────────────────
def install_binary(bin_path: Path, install_dir: Path) -> Path:
    install_dir.mkdir(parents=True, exist_ok=True)
    dest = install_dir / BIN_NAME
    shutil.copy2(bin_path, dest)
    if not IS_WINDOWS:
        dest.chmod(0o755)
    info(f"Installed to: {dest}")
    return dest

def check_path(install_dir: Path) -> None:
    path_dirs = os.environ.get("PATH", "").split(os.pathsep)
    if str(install_dir) not in path_dirs:
        warn(f"{install_dir} is not on your PATH.")
        if IS_WINDOWS:
            warn(f'Add it via: setx PATH "%PATH%;{install_dir}"')
        else:
            warn(f'Add this to your shell profile: export PATH="{install_dir}:$PATH"')

# ── MCP config snippet ─────────────────────────────────────────────────────────
def print_config(full_path: Path) -> None:
    print()
    print("────────────────────────────────────────────────────")
    print("  Add to your MCP client configuration:")
    print("────────────────────────────────────────────────────")
    print(f"""
{{
  "mcpServers": {{
    "markitdown": {{
      "command": "{full_path}"
    }}
  }}
}}
""")

    system = platform.system()
    if system == "Darwin":
        config = Path.home() / "Library" / "Application Support" / "Claude" / "claude_desktop_config.json"
    elif system == "Linux":
        config = Path.home() / ".config" / "Claude" / "claude_desktop_config.json"
    elif system == "Windows":
        appdata = os.environ.get("APPDATA", str(Path.home() / "AppData" / "Roaming"))
        config = Path(appdata) / "Claude" / "claude_desktop_config.json"
    else:
        config = Path("your MCP client config file")

    print(f"Claude Desktop config: {config}")
    print("Claude Code config:    .mcp.json in your project root")
    print()

# ── main ───────────────────────────────────────────────────────────────────────
def main() -> None:
    default_install_dir = Path(
        os.environ.get("INSTALL_DIR", str(Path.home() / ".local" / "bin"))
    )

    parser = argparse.ArgumentParser(
        description="Build and install markitdown-mcp, optionally install Tesseract.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Environment:\n  INSTALL_DIR   Override the install directory",
    )
    parser.add_argument(
        "--with-ocr",
        action="store_true",
        help="Install Tesseract OCR engine before building",
    )
    parser.add_argument(
        "--prefix",
        metavar="DIR",
        type=Path,
        default=default_install_dir,
        help=f"Install binary to DIR (default: {default_install_dir})",
    )
    args = parser.parse_args()

    check_go()
    check_tesseract(args.with_ocr)
    bin_path = build()
    full_path = install_binary(bin_path, args.prefix)
    check_path(args.prefix)
    print_config(full_path)
    info("Done. Restart your MCP client to pick up the new server.")


if __name__ == "__main__":
    main()
