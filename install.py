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
import datetime
import os
import platform
import re
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
        "mcp_args": ["server", "--stdio"],
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

    # Collect version stamp vars (best-effort; fall back to defaults if git unavailable).
    def _git(args: list[str], fallback: str) -> str:
        try:
            r = subprocess.run(["git"] + args, cwd=src_dir, capture_output=True, text=True)
            return r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else fallback
        except FileNotFoundError:
            return fallback

    version    = _git(["describe", "--tags", "--always", "--dirty"], "dev")
    commit     = _git(["rev-parse", "--short", "HEAD"], "unknown")
    build_time = datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ")
    ldflags = (
        f"-s -w"
        f" -X main.Version={version}"
        f" -X main.Commit={commit}"
        f" -X main.BuildTime={build_time}"
    )

    info(f"Building {bin_name} ({version})...")
    try:
        subprocess.run(
            ["go", "mod", "tidy", "-e"],
            cwd=src_dir, env=env, check=True, capture_output=True, text=True,
        )
        subprocess.run(
            ["go", "build", f"-ldflags={ldflags}", f"-o={bin_name}", "."],
            cwd=src_dir, env=env, check=True, capture_output=True, text=True,
        )
    except subprocess.CalledProcessError as e:
        stderr = e.stderr or ""
        die(f"Build failed for {name}:\n{stderr.strip()}")

    built = src_dir / bin_name
    info(f"Built: {built}")
    return built

# How many superseded binaries to keep beside the current one. One is all the
# migration path needs — the immediately-outgoing build — but these are ~45 MB
# each and they sit in /usr/local/bin, so the cap stays tight.
RETAINED_BINARIES = 2

def retained_name(dest: Path, version: str) -> Path:
    """Where the outgoing copy of dest goes: beside it, under a marked name.

    Retained binaries live in the install directory rather than a private one,
    so they are on PATH and can simply be run. That also sidesteps a trap: under
    `sudo make install`, Path.home() is root's home, and a retained binary filed
    under /var/root/.kg is one the user will never find.

    The ".old-" marker mirrors how archived databases are named, and matters
    more here than it looks. Pruning globs this pattern, and it globs inside a
    shared directory — a looser pattern like "kg-*" could match an unrelated
    command someone installed and delete it.
    """
    return dest.with_name(f"{dest.name}.old-{version}")

def binary_version(binary: Path) -> str:
    """Best-effort version of an installed binary, for naming the retained copy."""
    try:
        r = subprocess.run([str(binary), "--version"], capture_output=True, text=True, timeout=10)
        first = (r.stdout or "").strip().splitlines()
        if first:
            # "kg version v0.1.0-7-g73dc211 built ..." -> "v0.1.0-7-g73dc211"
            parts = first[0].split()
            for part in parts:
                if part.startswith("v") and any(ch.isdigit() for ch in part):
                    return re.sub(r"[^A-Za-z0-9._-]", "_", part)
    except (OSError, subprocess.SubprocessError):
        pass
    # A binary that will not report a version still needs a distinct name.
    return datetime.datetime.now().strftime("%Y%m%d%H%M%S")

def retain_previous(dest: Path) -> Path | None:
    """Keep the binary being replaced, so a database it wrote can still be read.

    Kuzu pins its storage format to the library version. Databases written
    before kg started stamping that version have no way to declare what wrote
    them, and no journal to replay, so the only route back into one is the
    binary that created it. Keeping the outgoing build makes that possible for
    the one generation of databases that predates journaling; after that the
    journal handles it and this is dead weight.
    """
    if not dest.exists():
        return None

    kept = retained_name(dest, binary_version(dest))
    if kept == dest:
        return None

    if not kept.exists():
        try:
            shutil.copy2(dest, kept)
            if not IS_WINDOWS:
                kept.chmod(0o755)
        except OSError as e:
            # Retention is insurance, not a prerequisite. A full disk, or a
            # directory this process cannot write, should not block an upgrade.
            warn(f"Could not retain the previous {dest.name}: {e}")
            return None

    prune_retained(dest)
    return kept

def prune_retained(dest: Path) -> None:
    """Drop all but the most recent RETAINED_BINARIES copies of one binary.

    The glob is deliberately narrow. This runs in a shared directory, usually
    /usr/local/bin, and anything it matches it deletes.
    """
    try:
        kept = sorted(
            (p for p in dest.parent.glob(f"{dest.name}.old-*") if p.is_file()),
            key=lambda p: p.stat().st_mtime,
            reverse=True,
        )
    except OSError:
        return
    for stale in kept[RETAINED_BINARIES:]:
        try:
            stale.unlink()
        except OSError:
            pass

def install_binary(bin_path: Path, install_dir: Path) -> Path:
    install_dir.mkdir(parents=True, exist_ok=True)
    dest = install_dir / bin_path.name
    retained = retain_previous(dest)
    shutil.copy2(bin_path, dest)
    if not IS_WINDOWS:
        dest.chmod(0o755)
    info(f"Installed: {dest}")
    if retained:
        info(f"Previous build kept at {retained} (for reading databases it wrote)")
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
        "--retain-only",
        metavar="PATH",
        type=Path,
        default=None,
        help="Retain the binary already at PATH and exit, installing nothing. "
             "Used by the Makefiles so `make install` gets the same safety net "
             "as this script rather than a second copy of the logic.",
    )
    parser.add_argument(
        "--prefix",
        metavar="DIR",
        type=Path,
        default=install_dir,
        help=f"Install binaries to DIR (default: {install_dir})",
    )
    args = parser.parse_args()

    if args.retain_only:
        kept = retain_previous(args.retain_only)
        if kept:
            info(f"Previous build kept at {kept}")
        return

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
