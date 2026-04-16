#!/usr/bin/env python3
"""install.py — build and install MCP servers from this repository.

Usage:
    python3 install.py                  # install all MCPs
    python3 install.py --mcp kg         # install only kg
    python3 install.py --mcp markitdown # install only markitdown-mcp
    python3 install.py --list           # list available MCPs
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
IS_WINDOWS = platform.system() == "Windows"
SYSTEM = platform.system()  # "Darwin", "Linux", "Windows"

# Each MCP entry: { src_dir, bin_name, cgo, description, extra_args }
MCPS = {
    "markitdown": {
        "src_dir": REPO_ROOT / "src" / "markitdown",
        "bin_name": "markitdown-mcp.exe" if IS_WINDOWS else "markitdown-mcp",
        "cgo": False,
        "description": "Document-to-Markdown converter (HTML, PDF, DOCX, XLSX, PPTX, …)",
        "mcp_args": [],
    },
    "kg": {
        "src_dir": REPO_ROOT / "src" / "kg",
        "bin_name": "kg.exe" if IS_WINDOWS else "kg",
        "cgo": True,
        "description": "Project knowledge graph (KuzuDB-backed, with code indexer)",
        "mcp_args": ["handle-server", "--stdio"],
    },
}

# ── colour helpers ─────────────────────────────────────────────────────────────
_USE_COLOR = sys.stdout.isatty()

def _c(code: str, text: str) -> str:
    return f"\033[{code}m{text}\033[0m" if _USE_COLOR else text

def info(msg: str)  -> None: print(f"{_c('0;32', '==>')} {msg}")
def warn(msg: str)  -> None: print(f"{_c('1;33', 'warn:')} {msg}")
def error(msg: str) -> None: print(f"{_c('0;31', 'error:')} {msg}", file=sys.stderr)
def die(msg: str)   -> None: error(msg); sys.exit(1)
def header(msg: str)-> None: print(f"\n{_c('0;34', '─── ' + msg + ' ───')}")

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
    version_str = result.stdout.split()[2].lstrip("go")  # e.g. "1.24.0"
    major, minor = int(version_str.split(".")[0]), int(version_str.split(".")[1])
    if major < REQUIRED_GO_MAJOR or (major == REQUIRED_GO_MAJOR and minor < REQUIRED_GO_MINOR):
        die(f"Go {REQUIRED_GO_MAJOR}.{REQUIRED_GO_MINOR}+ required, found {version_str}")
    info(f"Go {version_str} found")

# ── install dir (per platform) ─────────────────────────────────────────────────
def default_install_dir() -> Path:
    """Return the platform-appropriate default install directory."""
    override = os.environ.get("INSTALL_DIR")
    if override:
        return Path(override)
    if SYSTEM == "Darwin":
        return Path("/usr/local/bin")
    if SYSTEM == "Linux":
        return Path("/usr/local/bin")
    # Windows: %LOCALAPPDATA%\Programs
    local_app_data = os.environ.get("LOCALAPPDATA", str(Path.home() / "AppData" / "Local"))
    return Path(local_app_data) / "Programs" / "mcp"

# ── Tesseract (markitdown optional dep) ───────────────────────────────────────
def detect_pkg_manager() -> str:
    for pm in ("brew", "apt-get", "dnf", "yum", "pacman", "zypper", "choco"):
        if which(pm):
            return pm
    return "unknown"

def install_tesseract() -> None:
    pm = detect_pkg_manager()
    info(f"Installing Tesseract using: {pm}")
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
    output = result.stdout or result.stderr
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
            warn("Tesseract not found — image OCR will be unavailable.")
            warn("Re-run with --with-ocr to install, or install manually.")

# ── build + install ────────────────────────────────────────────────────────────
def build_mcp(name: str, cfg: dict) -> Path:
    src_dir: Path = cfg["src_dir"]
    bin_name: str = cfg["bin_name"]
    cgo_env = {"CGO_ENABLED": "1"} if cfg["cgo"] else {"CGO_ENABLED": "0"}
    env = {**os.environ, **cgo_env}

    info(f"Building {bin_name}...")
    try:
        subprocess.run(
            ["go", "mod", "tidy", "-e"],
            cwd=src_dir, env=env, check=True, capture_output=True, text=True,
        )
        subprocess.run(
            ["go", "build", "-ldflags=-s -w", f"-o={bin_name}", "."],
            cwd=src_dir, env=env, check=True, capture_output=True, text=True,
        )
    except subprocess.CalledProcessError as e:
        stderr = e.stderr or ""
        die(f"Build failed for {name}:\n{stderr.strip()}")

    built = src_dir / bin_name
    info(f"Built: {built}")
    return built

def install_binary(bin_path: Path, install_dir: Path) -> Path:
    install_dir.mkdir(parents=True, exist_ok=True)
    dest = install_dir / bin_path.name
    shutil.copy2(bin_path, dest)
    if not IS_WINDOWS:
        dest.chmod(0o755)
    info(f"Installed: {dest}")
    return dest

def check_path(install_dir: Path) -> None:
    path_dirs = os.environ.get("PATH", "").split(os.pathsep)
    if str(install_dir) not in path_dirs:
        warn(f"{install_dir} is not on your PATH.")
        if IS_WINDOWS:
            warn(f'Add it: setx PATH "%PATH%;{install_dir}"')
        else:
            warn(f'Add to your shell profile: export PATH="{install_dir}:$PATH"')

def print_config(installed: dict[str, tuple[Path, list[str]]]) -> None:
    """Print combined MCP config snippet for all installed servers."""
    if not installed:
        return

    if SYSTEM == "Darwin":
        desktop_cfg = Path.home() / "Library" / "Application Support" / "Claude" / "claude_desktop_config.json"
    elif SYSTEM == "Linux":
        desktop_cfg = Path.home() / ".config" / "Claude" / "claude_desktop_config.json"
    else:
        appdata = os.environ.get("APPDATA", str(Path.home() / "AppData" / "Roaming"))
        desktop_cfg = Path(appdata) / "Claude" / "claude_desktop_config.json"

    snippets = []
    for name, (bin_path, mcp_args) in installed.items():
        args_part = ""
        if mcp_args:
            args_part = ',\n      "args": [' + ", ".join(f'"{a}"' for a in mcp_args) + "]"
        snippets.append(
            f'    "{name}": {{\n      "command": "{bin_path}"{args_part}\n    }}'
        )

    print()
    print("────────────────────────────────────────────────")
    print("  Add to your MCP client configuration:")
    print("────────────────────────────────────────────────")
    print('{\n  "mcpServers": {')
    print(",\n".join(snippets))
    print("  }\n}")
    print()
    print(f"Claude Desktop config: {desktop_cfg}")
    print("Claude Code config:    .mcp.json in your project root")
    print()

# ── main ───────────────────────────────────────────────────────────────────────
def main() -> None:
    install_dir = default_install_dir()

    parser = argparse.ArgumentParser(
        description="Build and install MCP servers.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Available MCPs:\n"
            + "\n".join(f"  {n:12s}  {c['description']}" for n, c in MCPS.items())
            + "\n\nEnvironment:\n  INSTALL_DIR   Override the install directory"
        ),
    )
    parser.add_argument(
        "--mcp",
        metavar="NAME",
        default="all",
        help=f"MCP server to install: {' | '.join(MCPS)} | all (default: all)",
    )
    parser.add_argument(
        "--list",
        action="store_true",
        help="List available MCPs and exit",
    )
    parser.add_argument(
        "--with-ocr",
        action="store_true",
        help="Install Tesseract OCR engine (used by markitdown)",
    )
    parser.add_argument(
        "--prefix",
        metavar="DIR",
        type=Path,
        default=install_dir,
        help=f"Install binaries to DIR (default: {install_dir})",
    )
    args = parser.parse_args()

    if args.list:
        print("Available MCPs:")
        for name, cfg in MCPS.items():
            print(f"  {name:12s}  {cfg['description']}")
        return

    # Resolve which MCPs to install
    if args.mcp == "all":
        selected = list(MCPS.keys())
    elif args.mcp in MCPS:
        selected = [args.mcp]
    else:
        die(f"Unknown MCP: {args.mcp!r}. Available: {', '.join(MCPS)}")

    check_go()

    if "markitdown" in selected:
        check_tesseract(args.with_ocr)

    if SYSTEM == "Linux" and str(args.prefix).startswith("/usr"):
        warn(f"Installing to {args.prefix} may require sudo on Linux.")
        warn("Use INSTALL_DIR=~/.local/bin python3 install.py to install without sudo.")

    installed: dict[str, tuple[Path, list[str]]] = {}
    for name in selected:
        cfg = MCPS[name]
        header(name)
        bin_path = build_mcp(name, cfg)
        full_path = install_binary(bin_path, args.prefix)
        installed[name] = (full_path, cfg["mcp_args"])

    check_path(args.prefix)
    print_config(installed)
    info("Done. Restart your MCP client to pick up the new server(s).")


if __name__ == "__main__":
    main()
