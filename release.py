#!/usr/bin/env python3
"""Build native BuildHeadway releases. Python is only a developer packaging tool."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import zipfile

TARGETS = [
    "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64",
    "windows-amd64", "windows-arm64",
]

BRAND_ASSETS = [
    "buildheadway-logo-v1.png",
    "buildheadway-icon-v1.png",
    "buildheadway-logo-on-white-v1.png",
]


def command(args, root, **kwargs):
    result = subprocess.run(args, cwd=root, capture_output=True, text=True, **kwargs)
    if result.returncode:
        raise RuntimeError(" ".join(map(str, args)) + "\n" + result.stdout + result.stderr)
    return result.stdout


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        text = text.lstrip()
        value, index = decoder.raw_decode(text)
        yield value
        text = text[index:]


def host_target():
    system = {"Windows": "windows", "Darwin": "darwin", "Linux": "linux"}[platform.system()]
    arch = "arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64"
    return system + "-" + arch


def build(args):
    root = Path(__file__).resolve().parent
    output = Path(args.output).resolve()
    stage = output / "BuildHeadway"
    if stage.exists():
        shutil.rmtree(stage)
    (stage / "bin").mkdir(parents=True)
    if shutil.which("node") is None or shutil.which("npm") is None:
        raise RuntimeError("Release packaging needs Node.js 20+ and npm for the terminal package")
    package = json.loads((root / "npm" / "package.json").read_text())
    if args.repository and not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*", args.repository):
        raise ValueError("--repository must be OWNER/REPOSITORY")
    if args.repository:
        package["repository"] = {"type": "git", "url": "git+https://github.com/" + args.repository + ".git"}
        package["homepage"] = "https://github.com/" + args.repository + "#readme"
        package["bugs"] = {"url": "https://github.com/" + args.repository + "/issues"}
    source_version = next(line.split('"')[1] for line in (root / "types.go").read_text().splitlines() if line.startswith("const Version ="))
    if package["version"] != source_version:
        raise ValueError("Native and npm package versions must match")
    command(["node", "npm/check-docs.cjs"], root)
    if not args.skip_tests:
        print("Running native tests", flush=True)
        command([args.go, "test", "-buildvcs=false", "./..."], root)
    targets = args.targets.split(",") if args.targets else TARGETS
    if any(target not in TARGETS for target in targets):
        raise ValueError("Unknown native target")

    def compile_target(target):
        goos, goarch = target.split("-")
        filename = "buildheadway-" + target + (".exe" if goos == "windows" else "")
        destination = stage / "bin" / filename
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
        command([args.go, "build", "-buildvcs=false", "-trimpath",
                 "-ldflags=-s -w" + (" -X main.ReleaseRepository=" + args.repository if args.repository else ""), "-o", str(destination), "."], root, env=env)
        destination.chmod(0o755)
        print("Built " + target, flush=True)
        return destination

    with ThreadPoolExecutor(max_workers=args.jobs) as executor:
        binaries = list(executor.map(compile_target, targets))
    for name in ["README.md", "CHANGELOG.md", "CONTRIBUTING.md", "LICENSE", "install.sh", "install.ps1"]:
        data = (root / name).read_bytes()
        if name in ("install.sh", "install.ps1"):
            data = data.replace(b"@RELEASE_REPOSITORY@", args.repository.encode())
        (stage / name).write_bytes(data)
        if name.endswith(".sh"):
            (stage / name).chmod(0o755)
    shutil.copytree(root / "docs", stage / "docs")
    (stage / "package.json").write_text(json.dumps(package, indent=2) + "\n")
    (stage / "npm").mkdir()
    for name in ["buildheadway.cjs", "check-package.cjs", "check-docs.cjs"]:
        shutil.copyfile(root / "npm" / name, stage / "npm" / name)
        (stage / "npm" / name).chmod(0o755)
    brand_paths = {Path("assets") / "brand" / name for name in BRAND_ASSETS}
    for relative in sorted(brand_paths):
        destination = stage / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(root / relative, destination)
    source = stage / "source"
    source.mkdir()
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        relative = path.relative_to(root)
        if any(part in ("dist", "__pycache__", ".git", ".forge", "node_modules") for part in relative.parts):
            continue
        if path.suffix not in (".go", ".mod", ".sum", ".py", ".md", ".yml", ".sh", ".ps1", ".cjs", ".json") and path.name not in ("LICENSE", ".gitignore") and relative not in brand_paths:
            continue
        destination = source / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, destination)
    licenses = stage / "THIRD_PARTY_LICENSES"
    licenses.mkdir()
    modules = json_stream(command([args.go, "list", "-m", "-json", "all"], root))
    module_rows = []
    for module in modules:
        directory = module.get("Dir")
        if module.get("Main") or not directory:
            continue
        module_rows.append({"module": module["Path"], "version": module.get("Version")})
        for path in Path(directory).iterdir():
            if path.is_file() and path.name.upper().startswith(("LICENSE", "COPYING", "NOTICE")):
                name = module["Path"].replace("/", "__") + "__" + path.name
                shutil.copyfile(path, licenses / name)
    goroot = command([args.go, "env", "GOROOT"], root).strip()
    shutil.copyfile(Path(goroot) / "LICENSE", licenses / "Go-LICENSE")
    (licenses / "modules.json").write_text(json.dumps(module_rows, indent=2) + "\n")
    hashes = [
        hashlib.sha256(path.read_bytes()).hexdigest() + "  bin/" + path.name
        for path in sorted(binaries)
    ]
    (stage / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    native = next((path for path in binaries if path.name == "buildheadway-" + host_target() +
                   (".exe" if host_target().startswith("windows-") else "")), None)
    smoke = ""
    if native is not None:
        env = dict(os.environ, BUILDHEADWAY_HOME=str(output / "native-smoke-home"))
        smoke = command([str(native), "--self-test"], root, env=env)
        print("Native self-test passed", flush=True)
        if os.name != "nt":
            print(command([sys.executable, "scripts/installer_download_test.py", str(native)], root), flush=True)
    validation = {
        "built_at": datetime.now(timezone.utc).isoformat(),
        "go": command([args.go, "version"], root).strip(),
        "compiled_targets": targets,
        "executed_target": host_target() if smoke else None,
        "native_self_test_passed": bool(smoke),
        "unit_integration_tests_passed": not args.skip_tests,
        "windows_macos_runtime_tests": "Pending native CI execution unless this is that native host",
        "code_signing": "Unsigned build",
        "distribution": "npm package with native binaries; no install or postinstall scripts",
        "npm_publication": "Not published by this build command",
    }
    (stage / "validation.json").write_text(json.dumps(validation, indent=2) + "\n")
    report = (
        "# Build validation\n\n"
        f"Build host: **{host_target()}**. Compiler: {validation['go']}.\n\n"
        f"Native test suite: **{'passed' if not args.skip_tests else 'not run by this packaging command'}**.\n\n"
        f"Offline native self-test: **{'passed on ' + host_target() if smoke else 'not executed'}**.\n\n"
        "Compiled binaries:\n\n" + "\n".join("- " + target for target in targets) +
        "\n\nmacOS and Windows execution is not claimed from a Linux build. "
        "The included CI workflow runs the native tests and install/upgrade/uninstall checks on all three OS families.\n\n"
        "This package is unsigned and is not described as a fully verified production release.\n"
    )
    if (root / "docs/maintainers/validation.md").exists():
        report += "\n" + (root / "docs/maintainers/validation.md").read_text()
    (stage / "VALIDATION.md").write_text(report)
    if set(targets) == set(TARGETS):
        # Use npm's own packlist and lifecycle, with no third-party build dependencies.
        npm_cli = os.environ.get("npm_execpath")
        npm = ["node", npm_cli] if npm_cli else [shutil.which("npm")]
        if os.name == "nt" and not npm_cli:
            # Invoke npm's JavaScript entry point directly rather than passing a .cmd to CreateProcess.
            node_dir = Path(shutil.which("node")).parent
            npm_cli = node_dir / "node_modules" / "npm" / "bin" / "npm-cli.js"
            if not npm_cli.exists():
                raise RuntimeError("Set npm_execpath to npm-cli.js for this Node.js installation")
            npm = ["node", str(npm_cli)]
        def pack():
            command(["node", "npm/check-package.cjs"], stage)
            # Lifecycle stdout can precede npm's JSON. Run our check explicitly,
            # then request a script-free pack with a machine-readable result.
            rows = json.loads(command(npm + ["pack", "--ignore-scripts", "--json", "--pack-destination", str(output)], stage))
            if len(rows) != 1:
                raise RuntimeError("npm did not produce exactly one package")
            return output / rows[0]["filename"]

        tarball = pack()
        env = dict(os.environ, BUILDHEADWAY_PACKAGE_DIR=str(stage), BUILDHEADWAY_PACKAGE_TGZ=str(tarball))
        result = command(["node", "--test", "npm/test/docs.test.cjs", "npm/test/launcher.test.cjs", "npm/test/package.test.cjs"], root, env=env)
        print(result, flush=True)
        validation["npm_package_tests_passed"] = True
        validation["npm_test_host"] = host_target()
        (stage / "validation.json").write_text(json.dumps(validation, indent=2) + "\n")
        report += (
            "\n## Terminal package validation\n\n"
            f"The real npm release package passed installation and launch tests on **{host_target()}**. "
            "Tests install without lifecycle scripts and without registry access into a path containing spaces, "
            "run the installed buildheadway command, complete its native offline repair loop, scan coding agents, "
            "preserve native exit codes, reinstall, reject a tampered binary, and uninstall while retaining user settings.\n\n"
            "All six binaries pass checksum validation. This validates execution only on the recorded host. "
            "Public npm publication is a separate step and has not been performed by the build.\n"
        )
        (stage / "VALIDATION.md").write_text(report)
        # Only validation documentation changes after testing; repack it with the identical executables.
        tarball = pack()
        print(f"{tarball} ({tarball.stat().st_size:,} bytes)", flush=True)
    # These small per-platform assets power one-command native installs/updates.
    assets = output / "release-assets"
    if assets.exists():
        shutil.rmtree(assets)
    assets.mkdir()
    for binary in binaries:
        shutil.copyfile(binary, assets / binary.name)
    for name in ("SHA256SUMS", "install.sh", "install.ps1"):
        shutil.copyfile(stage / name, assets / name)
    archive = output / "buildheadway.zip"
    temporary_archive = output / "buildheadway.zip.partial"
    with zipfile.ZipFile(temporary_archive, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as bundle:
        for path in sorted(stage.rglob("*")):
            if not path.is_file():
                continue
            relative = Path("BuildHeadway") / path.relative_to(stage)
            info = zipfile.ZipInfo(relative.as_posix())
            info.create_system = 3
            executable = path in binaries or path.suffix in (".sh", ".cjs")
            info.external_attr = (0o100755 if executable else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            bundle.writestr(info, path.read_bytes())
    with zipfile.ZipFile(temporary_archive) as bundle:
        if bundle.testzip() is not None:
            raise RuntimeError("Release ZIP failed its integrity check")
        for relative in brand_paths:
            expected = (root / relative).read_bytes()
            for prefix in (Path("BuildHeadway"), Path("BuildHeadway/source")):
                if bundle.read((prefix / relative).as_posix()) != expected:
                    raise RuntimeError("Release ZIP changed approved brand artwork: " + str(relative))
    print("Approved brand artwork verified in distribution and source archive", flush=True)
    os.replace(temporary_archive, archive)
    print(f"{archive} ({archive.stat().st_size:,} bytes)", flush=True)
    return archive


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default="dist")
    parser.add_argument("--go", default="go")
    parser.add_argument("--targets", help="Comma-separated targets; default is all six")
    parser.add_argument("--jobs", type=int, default=2)
    parser.add_argument("--skip-tests", action="store_true")
    parser.add_argument("--repository", default="", help="Real GitHub OWNER/REPOSITORY for native installation and updates")
    build(parser.parse_args())
