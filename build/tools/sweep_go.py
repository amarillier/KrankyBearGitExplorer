#!/usr/bin/env python3
"""Bring Go projects to one baseline: the Go version, the Fyne version, and a clean govulncheck.

    build/tools/sweep_go.py [--go 1.27.2] [--fyne v2.8.1] [--workers 3] [--dry-run] [--backup-dir DIR] [--log-dir DIR] PROJECT_DIR...

Per project (GOWORK is off so a workspace file never interferes):
  1. back up go.mod and go.sum
  2. set the go directive (and drop any older `toolchain` line), move fyne.io/fyne/v2 to the given version if the project uses it,
     and take golang.org/x/image, net and text to their latest releases if they are in the module graph (the usual source of
     reachable findings through Fyne's image decoding)
  3. go mod tidy, then go build ./...  -- if the BUILD breaks, go.mod/go.sum are restored from the backup and the project is
     reported ROLLED BACK with the compiler's complaint, so a Fyne upgrade that needs code changes never leaves a broken project
  4. govulncheck ./...; for every module it reports a fix for, go get the fix (golang.org/x/* at latest, others at the fixed
     version), tidy, rebuild (same rollback rule) and scan again
  5. go vet ./... and go test ./... are run and reported (failures there are reported, not rolled back: they are often old)

Only go.mod and go.sum are ever changed. Nothing is committed. Vendored projects and modules without dependencies are skipped. Run it from anywhere:
    build/tools/sweep_go.py ~/Allan/Source/go/KrankyBear*   (the default skip list leaves out third-party clones and backups)
"""
import argparse
import concurrent.futures
import json
import os
import re
import shutil
import subprocess
import sys
import time

DEFAULT_SKIP = re.compile(r"(backup|^pdfcpu$|^go-minesweeper-new$|^go-solitaire$|^go-sudoku$)")
XMODS = ["golang.org/x/image", "golang.org/x/net", "golang.org/x/text"]


def run(cmd, cwd, timeout=1200, env=None):
    e = dict(os.environ)
    e["GOWORK"] = "off"
    if env:
        e.update(env)
    try:
        p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout, env=e)
        out = p.stdout + p.stderr
        return p.returncode, "\n".join(l for l in out.splitlines() if not l.startswith("#") and "ld: warning" not in l)
    except subprocess.TimeoutExpired:
        return 124, "timed out"


def gomod(proj):
    return open(os.path.join(proj, "go.mod"), encoding="utf-8").read()


def version_of(text, mod):
    m = re.search(r"^\s*" + re.escape(mod) + r"\s+(v\S+)", text, re.M)
    return m.group(1) if m else None


def go_directive(text):
    m = re.search(r"^go\s+(\S+)", text, re.M)
    return m.group(1) if m else "?"


def parse_vulns(out):
    """-> (clean, {module: fixed_version}, stdlib_fix) from govulncheck text output."""
    if "No vulnerabilities found" in out:
        return True, {}, None
    fixes, std = {}, None
    cur = None
    for line in out.splitlines():
        line = line.strip()
        m = re.match(r"Module:\s+(\S+)", line)
        if m:
            cur = m.group(1)
        m = re.match(r"Fixed in:\s+(\S+)@(\S+)", line)
        if m:
            fixes[m.group(1)] = m.group(2)
        m = re.match(r"Fixed in:\s+(go\S+)", line)
        if m:
            std = m.group(1)
    return False, fixes, std


def summarize_vulns(out):
    clean, fixes, std = parse_vulns(out)
    if clean:
        return "clean"
    m = re.search(r"Your code is affected by (\d+) vulnerabilit", out)
    other = re.search(r"also found (\d+) vulnerabilities in packages you import and (\d+) vulnerabilities in modules", out)
    parts = []
    if m:
        parts.append(f"{m.group(1)} reachable")
    if other:
        parts.append(f"{other.group(1)}+{other.group(2)} not called")
    return ", ".join(parts) or "see log"


def process(proj, a):
    name = os.path.basename(proj.rstrip("/"))
    res = {"project": name, "status": "", "notes": []}
    log = open(os.path.join(a.log_dir, name + ".log"), "w")

    def L(msg):
        log.write(msg + "\n")
        log.flush()

    mod = os.path.join(proj, "go.mod")
    if not os.path.exists(mod):
        res["status"] = "SKIPPED (no go.mod)"
        return res
    if os.path.exists(os.path.join(proj, "vendor", "modules.txt")):
        res["status"] = "SKIPPED (vendored)"
        return res
    before = gomod(proj)
    if not re.search(r"^\s*require\b", before, re.M):
        res["status"] = "SKIPPED (no dependencies)"
        res["before"] = f"go {go_directive(before)}"
        return res
    uses_fyne = bool(version_of(before, "fyne.io/fyne/v2"))
    res["before"] = f"go {go_directive(before)}" + (f", fyne {version_of(before, 'fyne.io/fyne/v2')}" if uses_fyne else "")
    if a.dry_run:
        res["status"] = "WOULD UPDATE"
        return res

    bdir = os.path.join(a.backup_dir, name)
    os.makedirs(bdir, exist_ok=True)
    for f in ("go.mod", "go.sum"):
        if os.path.exists(os.path.join(proj, f)):
            shutil.copy(os.path.join(proj, f), os.path.join(bdir, f))

    def rollback(why):
        for f in ("go.mod", "go.sum"):
            src = os.path.join(bdir, f)
            if os.path.exists(src):
                shutil.copy(src, os.path.join(proj, f))
        res["status"] = "ROLLED BACK"
        res["notes"].append(why[:600])
        L("ROLLED BACK: " + why)
        return res

    def step(cmd, label):
        rc, out = run(cmd, proj)
        L(f"$ {' '.join(cmd)}\n{out}\n(exit {rc})")
        return rc, out

    step(["go", "mod", "edit", f"-go={a.go}"], "go directive")
    step(["go", "mod", "edit", "-toolchain=none"], "toolchain")
    if uses_fyne:
        rc, out = step(["go", "get", f"fyne.io/fyne/v2@{a.fyne}"], "fyne")
        if rc != 0:
            return rollback("go get fyne failed: " + out)
    present = [m for m in XMODS if version_of(gomod(proj), m)]
    if present:
        rc, out = step(["go", "get"] + [m + "@latest" for m in present], "x/ modules")
        if rc != 0:
            res["notes"].append("go get x/* failed: " + out[:200])
    rc, out = step(["go", "mod", "tidy"], "tidy")
    if rc != 0:
        return rollback("go mod tidy failed: " + out)
    rc, out = step(["go", "build", "./..."], "build")
    if rc != 0:
        return rollback("build failed after the update (needs code changes?): " + out)

    vuln = ""
    for attempt in range(2):
        rc, out = step(["govulncheck", "./..."], "govulncheck")
        clean, fixes, std = parse_vulns(out)
        vuln = summarize_vulns(out)
        if clean or attempt == 1 or not fixes:
            if std:
                res["notes"].append(f"stdlib findings need {std}")
            break
        gets = []
        for m, v in fixes.items():
            gets.append(m + ("@latest" if m.startswith("golang.org/x/") else "@" + v))
        step(["go", "get"] + gets, "apply fixes")
        rc, out2 = step(["go", "mod", "tidy"], "tidy")
        rc, out2 = step(["go", "build", "./..."], "build")
        if rc != 0:
            return rollback("build failed after applying vulnerability fixes: " + out2)
    res["vuln"] = vuln

    rc, out = step(["go", "vet", "./..."], "vet")
    res["vet"] = "ok" if rc == 0 else "FAILED"
    if rc != 0:
        res["notes"].append("vet: " + out.strip().splitlines()[0][:160] if out.strip() else "vet failed")
    rc, out = step(["go", "test", "./..."], "test")
    res["test"] = "ok" if rc == 0 else "FAILED"
    if rc != 0:
        first = next((l for l in out.splitlines() if "FAIL" in l or "fatal" in l or "panic" in l), "test failed")
        res["notes"].append("test: " + first[:160])
    after = gomod(proj)
    res["after"] = f"go {go_directive(after)}" + (f", fyne {version_of(after, 'fyne.io/fyne/v2')}" if uses_fyne else "")
    res["status"] = "UPDATED" if after != before else "UNCHANGED"
    return res


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("projects", nargs="+")
    ap.add_argument("--go", default="1.27.2")
    ap.add_argument("--fyne", default="v2.8.1")
    ap.add_argument("--workers", type=int, default=3)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--include-all", action="store_true", help="do not apply the default skip list")
    ap.add_argument("--backup-dir", default=os.path.expanduser("~/Allan/Source/go/_sweep-backups/" + time.strftime("%Y%m%d-%H%M")))
    ap.add_argument("--log-dir", default=None)
    a = ap.parse_args()
    a.log_dir = a.log_dir or os.path.join(a.backup_dir, "logs")
    os.makedirs(a.log_dir, exist_ok=True)
    projects = []
    for p in a.projects:
        p = os.path.abspath(p)
        name = os.path.basename(p.rstrip("/"))
        if not os.path.isdir(p) or not os.path.exists(os.path.join(p, "go.mod")):
            continue
        if not a.include_all and DEFAULT_SKIP.search(name):
            print(f"skipping {name} (default skip list)")
            continue
        projects.append(p)
    print(f"{len(projects)} project(s), go {a.go}, fyne {a.fyne}, workers {a.workers}{' (dry run)' if a.dry_run else ''}")
    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=a.workers) as ex:
        futs = {ex.submit(process, p, a): p for p in projects}
        for f in concurrent.futures.as_completed(futs):
            r = f.result()
            results.append(r)
            print(f"  done {r['project']:34} {r['status']:12} vuln={r.get('vuln','-'):22} vet={r.get('vet','-'):6} test={r.get('test','-')}", flush=True)
    results.sort(key=lambda r: r["project"].lower())
    with open(os.path.join(a.backup_dir, "summary.json"), "w") as f:
        json.dump(results, f, indent=2)
    print("\nSUMMARY")
    for r in results:
        print(f"{r['project']:34} {r['status']:12} {r.get('before','-'):28} -> {r.get('after','-'):28} vuln={r.get('vuln','-'):20} vet={r.get('vet','-'):6} test={r.get('test','-')}")
        for n in r["notes"]:
            print(f"      - {n[:240]}")
    print(f"\nbackups and per-project logs: {a.backup_dir}")


if __name__ == "__main__":
    main()
