#!/usr/bin/env python3
"""Move an existing project onto the Template's build/ + single-VERSION layout.

    build/tools/adopt_layout.py [--apply] [--backup-dir DIR] [--no-verify] PROJECT_DIR...

Default is a DRY RUN: it prints what it would do for each project, in the order given (use alphabetical globs). With --apply it
changes the project. Nothing is committed and the template is never modified.

Per project it
  * reads the names from installerbear.yaml (binary file names, identity) and FyneApp.toml (title, icon), and the version from
    main.go's `appVersion = "x.y.z"`;
  * writes build/ from the template (lib.sh, version.sh, compile-*, sync2*, tools/...) plus a build/app.env with those names;
  * moves the old per-project scripts (compile-*.sh, package.sh, setver.sh, Inno/, the old get-mesa.sh and tools/ ...) into
    build/legacy/ (nothing is deleted) and git-ignores build/hosts.env and build/legacy/;
  * writes VERSION and makes main.go read it through go:embed (version = strings.TrimSpace(versionFile));
  * then checks: go build, go vet, build/version.sh check, kbinstallerbear validate. If go build fails the project is
    restored from the backup and reported as FAILED.
Skipped (reported): no installerbear.yaml (convert first), already adopted (has VERSION and build/lib.sh), no `appVersion = "..."`
literal in main.go, TaniumMigrator (yours; pass --include-migrator to override).
Backups: DIR/<project>/ holds a copy of every file touched or moved (default ~/Allan/Source/go/_layout-backups/<timestamp>).
"""
import argparse, datetime, os, re, shutil, subprocess, sys

TEMPLATE = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
LEGACY_FILES = ["compile-all.sh", "compile-mac.sh", "compile-linux.sh", "compile-win.sh", "compile-windows.sh", "compile-windows.ps1",
                "compile-windows.bat", "compile-windows-ssh.sh", "compile.sh", "check-windows-env.ps1", "package.sh", "packagemod.sh",
                "package-assets.sh", "setver.sh", "sync2windows.sh", "sync2ubuntubuild.sh", "sync2linuxmint.sh", "sync2ubuntu18.sh",
                "get-mesa.sh", "prepare-deps.sh", "prepare-deps.ps1", "update-deps.sh", "install-go.sh", "install-fyne.sh",
                "iconprep.sh", "images-optimize.sh", "setIcon.sh", "build-config.sh", "mkAllZip.sh", "mkicns.sh", "rename-app.sh",
                "buildrun.sh", "innoBuildIntel.ps1", "Inno"]
ANY_TOOL_OK = {"fetch_mesa_licenses.sh", "check_mesa_updates.sh"}  # old root tools/ files that the template's build/tools replaces
BUILD_SKIP = {"new-app.py", "new-app.sh", "hosts.env", "legacy"}


def sh(cmd, cwd, check=False):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)


def yaml_val(text, key):
    m = re.search(rf"^\s*{key}:\s*['\"]?([^'\"\n#]+?)['\"]?\s*(#.*)?$", text, re.M)
    return m.group(1).strip() if m else None


def derive(proj):
    """names for build/app.env, or (None, reason)"""
    yp = os.path.join(proj, "installerbear.yaml")
    if not os.path.exists(yp):
        return None, "no installerbear.yaml (convert the Inno script first)"
    y = open(yp).read()
    paths = re.findall(r"^\s*path:\s*['\"]?(bin/[^'\"\s]+)", y, re.M)
    win = next((os.path.basename(p) for p in paths if p.endswith(".exe")), None)
    lin = next((os.path.basename(p) for p in paths if "linux-amd64" in p), None)
    if not (win and lin):
        return None, "installerbear.yaml binaries lack a windows .exe and a linux-amd64 binary"
    fpm = lin[: -len("-linux-amd64")]
    fy = open(os.path.join(proj, "FyneApp.toml")).read() if os.path.exists(os.path.join(proj, "FyneApp.toml")) else ""
    title = (re.search(r'^Name\s*=\s*"([^"]*)"', fy, re.M) or [None, None])[1] or yaml_val(y, "name")
    icon = (re.search(r'^Icon\s*=\s*"([^"]*)"', fy, re.M) or [None, "assets/images/KrankyBearBeret.png"])[1]
    for cand in (icon, "assets/images/" + icon, "assets/" + icon, "images/" + icon):
        if os.path.isfile(os.path.join(proj, cand)):
            icon = cand
            break
    url = yaml_val(y, "url") or ""
    mainp = os.path.join(proj, "main.go")
    if not os.path.exists(mainp):
        return None, "no main.go in the project root"
    mg = open(mainp).read()
    m = re.search(r'^[ \t]*(?:(?:var|const)\s+)?appVersion\s*=\s*"([^"]+)"', mg, re.M)
    if not m:
        return None, 'no `appVersion = "x.y.z"` literal in main.go'
    dev = re.sub(r"[^a-z0-9]", "", os.path.basename(proj).lower().replace("krankybear", "")) or "app"
    return dict(title=title, fpm=fpm, win=win, icon=icon, url=url, dev=dev, version=m.group(1)), None


def adopt(proj, backup, apply_, verify):
    name = os.path.basename(proj)
    if os.path.exists(os.path.join(proj, "VERSION")) and os.path.exists(os.path.join(proj, "build", "lib.sh")):
        return "skipped", "already adopted"
    d, why = derive(proj)
    if why:
        return "skipped", why
    moves = [f for f in LEGACY_FILES if os.path.exists(os.path.join(proj, f))]
    tdir = os.path.join(proj, "tools")
    oldtools = [f for f in (os.listdir(tdir) if os.path.isdir(tdir) else []) if f in ANY_TOOL_OK]   # the project's own other tools stay
    notes = [f"names: exe={d['win']} binaries={d['fpm']}-* title={d['title']!r} icon={d['icon']} dev={d['dev']} version={d['version']}",
             f"move to build/legacy: {', '.join(moves) or '(none)'}"]
    if oldtools:
        notes.append("old tools/: " + ", ".join(oldtools) + " (moved to build/legacy/tools; other files in tools/ stay)")
    if not apply_:
        return "dry-run", "\n    ".join(notes)

    bk = os.path.join(backup, name)
    os.makedirs(bk, exist_ok=True)
    for f in moves + ["main.go", ".gitignore", "installerbear.yaml", "FyneApp.toml"]:
        s = os.path.join(proj, f)
        if os.path.exists(s):
            (shutil.copytree if os.path.isdir(s) else shutil.copy2)(s, os.path.join(bk, f))

    # build/ from the template
    b = os.path.join(proj, "build")
    os.makedirs(os.path.join(b, "legacy"), exist_ok=True)
    for root, dirs, files in os.walk(os.path.join(TEMPLATE, "build")):
        rel = os.path.relpath(root, os.path.join(TEMPLATE, "build"))
        dirs[:] = [x for x in dirs if not (rel == "." and x in BUILD_SKIP)]
        for f in files:
            if rel == "." and f in BUILD_SKIP:
                continue
            if f in (".DS_Store", "app.env"):
                continue
            dst = os.path.join(b, rel, f)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copy2(os.path.join(root, f), dst)
    open(os.path.join(b, "app.env"), "w").write(f'''# This project's identity for the build scripts (bash and PowerShell both read it): the only place these names live.
# The version is NOT here: edit ../VERSION (or run build/version.sh X.Y.Z) and every consumer follows.
KB_PROJECT_TITLE="{d['title']}"
KB_FPM_NAME="{d['fpm']}"              # bin/${{KB_FPM_NAME}}-macos-arm64, -macos-amd64, -linux-amd64, -linux-arm64
KB_WINDOWS_EXE="{d['win']}"        # bin/<this>
KB_MAC_ICON="{d['icon']}"
KB_DEV_BINARY="{d['dev']}"                       # what build/buildrun.sh builds and runs
KB_HOMEPAGE="{d['url']}"
KB_VENDOR="Allan Marillier"
KB_LICENSE="GPL v3"
''')
    # old scripts -> build/legacy (a file in both places: the template's wins in build/, the old one goes to legacy)
    for f in moves:
        dst = os.path.join(b, "legacy", f)
        if os.path.exists(dst):
            shutil.rmtree(dst) if os.path.isdir(dst) else os.remove(dst)
        shutil.move(os.path.join(proj, f), dst)
    if oldtools:
        os.makedirs(os.path.join(b, "legacy", "tools"), exist_ok=True)
        for f in oldtools:
            shutil.move(os.path.join(tdir, f), os.path.join(b, "legacy", "tools", f))
        if not os.listdir(tdir):
            os.rmdir(tdir)
    # VERSION and main.go
    open(os.path.join(proj, "VERSION"), "w").write(d["version"] + "\n")
    mp = os.path.join(proj, "main.go")
    mg = open(mp).read()
    # remove the literal (wherever: var, const, or a block entry) and declare it once, after the imports, from VERSION
    mg = re.sub(r'^[ \t]*(?:(?:var|const)\s+)?appVersion\s*=\s*"[^"]+"[^\n]*\n', "", mg, count=1, flags=re.M)
    if '_ "embed"' not in mg:
        mg = re.sub(r"^import \(\n", 'import (\n\t_ "embed"\n', mg, count=1, flags=re.M)
    if not re.search(r'^\s*"strings"', mg, re.M):
        mg = re.sub(r"^import \(\n", 'import (\n\t"strings"\n', mg, count=1, flags=re.M)
    decl = "\n// The version lives in the VERSION file (build/version.sh X.Y.Z).\n//go:embed VERSION\nvar versionFile string\n\nvar appVersion = strings.TrimSpace(versionFile)\n"
    mg, n = re.subn(r"^(import \((?:.|\n)*?\n\)\n)", lambda m: m.group(1) + decl, mg, count=1, flags=re.M)
    if n == 0:
        mg += decl
    open(mp, "w").write(mg)
    sh(["gofmt", "-w", "main.go"], proj)
    # .gitignore
    gp = os.path.join(proj, ".gitignore")
    g = open(gp).read() if os.path.exists(gp) else ""
    g = re.sub(r"^/?build/?\s*$\n?", "", g, flags=re.M)   # an old blanket 'build/' rule would hide the scripts
    add = [x for x in ("/build/hosts.env", "/build/legacy/") if x not in g.split("\n")]
    if add:
        g = g.rstrip("\n") + "\n\n# Build host details and retired scripts (see build/)\n" + "\n".join(add) + "\n"
    open(gp, "w").write(g)

    results = []
    ok = True
    if verify:
        for label, cmd in [("go build", ["go", "build", "./..."]), ("go vet", ["go", "vet", "./..."])]:
            r = sh(cmd, proj)
            results.append(f"{'ok  ' if r.returncode == 0 else 'FAIL'} {label}" + ("" if r.returncode == 0 else ": " + " | ".join((r.stdout + r.stderr).splitlines()[:3])))
            if label == "go build" and r.returncode != 0:
                ok = False
        sh(["bash", "build/version.sh", "sync"], proj)
        r = sh(["bash", "build/version.sh", "check"], proj)
        results.append(f"{'ok  ' if r.returncode == 0 else 'WARN'} version check" + ("" if r.returncode == 0 else ": " + " ".join((r.stderr).splitlines()[:3])))
        kb = shutil.which("kbinstallerbear") or os.path.expanduser("~/bin/kbinstallerbear")
        if os.path.exists(kb):
            r = sh([kb, "validate", "-p", "installerbear.yaml"], proj)
            results.append(f"{'ok  ' if r.returncode == 0 else 'WARN'} installerbear.yaml validate")
    if not ok:
        # restore
        for f in os.listdir(bk):
            s, t = os.path.join(bk, f), os.path.join(proj, f)
            if os.path.isdir(t) and not os.path.islink(t):
                shutil.rmtree(t)
            (shutil.copytree if os.path.isdir(s) else shutil.copy2)(s, t)
        for f in ("VERSION",):
            if os.path.exists(os.path.join(proj, f)):
                os.remove(os.path.join(proj, f))
        if os.path.isdir(os.path.join(b, "legacy", "tools")):
            os.makedirs(tdir, exist_ok=True)
            for f in os.listdir(os.path.join(b, "legacy", "tools")):
                shutil.move(os.path.join(b, "legacy", "tools", f), os.path.join(tdir, f))
        for f in moves:
            if os.path.exists(os.path.join(b, "legacy", f)) and not os.path.exists(os.path.join(proj, f)):
                shutil.move(os.path.join(b, "legacy", f), os.path.join(proj, f))
        shutil.rmtree(b, ignore_errors=True)
        return "FAILED", "go build failed after the change; project restored from the backup\n    " + "\n    ".join(notes + results)
    return "adopted", "\n    ".join(notes + results)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("projects", nargs="+")
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--backup-dir")
    ap.add_argument("--no-verify", action="store_true")
    ap.add_argument("--include-migrator", action="store_true")
    a = ap.parse_args()
    backup = a.backup_dir or os.path.expanduser("~/Allan/Source/go/_layout-backups/" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
    counts = {}
    for p in sorted(os.path.abspath(os.path.expanduser(x)) for x in a.projects):
        name = os.path.basename(p)
        if not os.path.exists(os.path.join(p, "go.mod")):
            continue
        if name == "TaniumMigrator" and not a.include_migrator:
            status, msg = "skipped", "TaniumMigrator is yours (use --include-migrator)"
        elif p == TEMPLATE or os.path.exists(os.path.join(p, "build", "lib.sh")):
            status, msg = "skipped", "already adopted"
        else:
            status, msg = adopt(p, backup, a.apply, not a.no_verify)
        counts[status] = counts.get(status, 0) + 1
        print(f"{status:8} {name}\n    {msg}")
    print("\n" + ", ".join(f"{k}: {v}" for k, v in counts.items()) + (f"\nbackups: {backup}" if a.apply else "\n(dry run: nothing changed; add --apply)"))


main()
