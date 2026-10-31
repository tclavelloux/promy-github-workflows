#!/usr/bin/env python3
"""Patch the Python advisories osv-scanner reports in uv.lock files.

Local twin of the step inlined in .github/workflows/python-vuln-fix.yml: the block
between the `python-vuln-fix body` markers must stay byte-identical in both
(internal/syncheck/python_workflows_test.go compares them). The tests drive it with
fake osv-scanner and uv executables, so no network is needed.

Usage: scripts/python_vuln_fix.py run --projects JSON --out DIR [--osv-scanner BIN]
       [--uv CMD] [--exclude TEXT] [--allow-major true|false] [--root DIR]
--uv is a command split with shlex, e.g. --uv "uvx uv@0.13.5". When <root>/.uv-version
exists, `<uv> --version` must report that version, else exit 2 (a different uv rewrites
the lock format).
Exit codes: 0 completed (fixed or not), 2 usage, scanner or uv-pin error, 4 uv error
that is not a resolver refusal.
"""

# >>> python-vuln-fix body
# Keep this block byte-identical between scripts/python_vuln_fix.py and
# .github/workflows/python-vuln-fix.yml (governance CI diffs them).
import argparse
import collections
import fnmatch
import hashlib
import json
import os
import posixpath
import re
import shlex
import shutil
import subprocess
import sys

if sys.version_info < (3, 11):
    sys.exit("::error::python-vuln-fix needs Python >= 3.11 (tomllib)")
import tomllib

PROJECT_RE = re.compile(r"^[A-Za-z0-9._/-]+$")
GLOB_RE = re.compile(r"^[A-Za-z0-9._@/*?+-]+$")
ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$")
NAME_RE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,99}$")
VERSION_TEXT_RE = re.compile(r"^[A-Za-z0-9.+!_-]{1,64}$")
SEVERITY_RE = re.compile(r"^[0-9]{1,2}(\.[0-9]{1,2})?$")
REFUSAL_RE = re.compile(r"No solution found|unsatisfiable", re.IGNORECASE)
MARKER = "<!-- python-vuln-fix:unfixed -->"
ISSUE_TITLE = "security: Python advisories need a human"

VERSION_RE = re.compile(
    r"^\s*v?(?:(?P<epoch>[0-9]+)!)?(?P<release>[0-9]+(?:\.[0-9]+)*)"
    r"(?:[-_.]?(?P<pre_l>alpha|a|beta|b|preview|pre|c|rc)[-_.]?(?P<pre_n>[0-9]+)?)?"
    r"(?:(?:-(?P<post_n1>[0-9]+))|(?:[-_.]?(?P<post_l>post|rev|r)[-_.]?(?P<post_n2>[0-9]+)?))?"
    r"(?:[-_.]?(?P<dev_l>dev)[-_.]?(?P<dev_n>[0-9]+)?)?"
    r"(?:\+(?P<local>[a-z0-9]+(?:[-_.][a-z0-9]+)*))?\s*$",
    re.IGNORECASE,
)
PRE_ORDER = {"a": 0, "b": 1, "rc": 2}
PRE_ALIAS = {"alpha": "a", "beta": "b", "c": "rc", "pre": "rc", "preview": "rc"}


class UsageError(Exception):
    """Bad input or a scanner failure: exit 2."""


class UvError(Exception):
    """uv failed for a reason other than a resolver refusal: exit 4."""


class Version:
    """A PEP 440 version ordered like `packaging`, minus the local segment."""

    def __init__(self, text, epoch, release, key):
        self.text, self.epoch, self.release, self.key = text, epoch, release, key

    def __lt__(self, other):
        return self.key < other.key

    def __le__(self, other):
        return self.key <= other.key

    def __eq__(self, other):
        return isinstance(other, Version) and self.key == other.key

    def __hash__(self):
        return hash(self.key)


def parse_version(text):
    """Parse a PEP 440 version; None when it is not one. A local `+cpu` never affects ordering."""
    m = VERSION_RE.match(text or "")
    if not m:
        return None
    epoch = int(m.group("epoch") or 0)
    release = tuple(int(x) for x in m.group("release").split("."))
    pre = None
    if m.group("pre_l"):
        letter = m.group("pre_l").lower()
        pre = (PRE_ALIAS.get(letter, letter), int(m.group("pre_n") or 0))
    post = None
    if m.group("post_n1") is not None:
        post = int(m.group("post_n1"))
    elif m.group("post_l"):
        post = int(m.group("post_n2") or 0)
    dev = int(m.group("dev_n") or 0) if m.group("dev_l") else None
    rel = list(release)
    while rel and rel[-1] == 0:
        rel.pop()
    if pre is None and post is None and dev is not None:
        pre_key = (-1, 0)
    elif pre is None:
        pre_key = (9, 0)
    else:
        pre_key = (PRE_ORDER[pre[0]], pre[1])
    key = (epoch, tuple(rel), pre_key, -1 if post is None else post, 10**18 if dev is None else dev)
    return Version(text, epoch, release, key)


def is_major(old, new):
    """True when the first release segment rises. CalVer (both >= 1000) is never a major."""
    a, b = parse_version(old), parse_version(new)
    if a is None or b is None:
        return True
    if a.release[0] >= 1000 and b.release[0] >= 1000:
        return False
    return b.epoch > a.epoch or b.release[0] > a.release[0]


def norm_name(name):
    """PEP 503 normalisation."""
    return re.sub(r"[-_.]+", "-", str(name)).lower()


def parse_excludes(text):
    """Newline- or comma-separated globs, normalised like package names."""
    out = []
    for raw in re.split(r"[\n,]", text or ""):
        raw = raw.strip()
        if not raw:
            continue
        if not GLOB_RE.match(raw):
            raise UsageError("invalid exclude entry: only [A-Za-z0-9._@/*?+-] are allowed in a glob")
        out.append((raw, norm_name(raw)))
    return out


def excluded_by(name, excludes):
    """The first glob matching the package name, else None. `numpy` does not match `numpy-financial`."""
    for raw, glob in excludes:
        if fnmatch.fnmatchcase(name, glob):
            return raw
    return None


def as_dict(x):
    return x if isinstance(x, dict) else {}


def as_list(x):
    return x if isinstance(x, list) else []


def clean_text(text, limit=1500):
    """Make untrusted tool output safe for a fenced Markdown block."""
    text = text.replace("\r", "")
    text = "".join(c if c == "\n" or (c.isprintable() and c != "`") else " " for c in text)
    text = text.replace("~~~", "~~")
    return text.strip()[-limit:]


def cell(text):
    return str(text).replace("|", "/").replace("\n", " ").replace("\t", " ")


class Dropped:
    count = 0


def safe_id(raw):
    if isinstance(raw, str) and ID_RE.match(raw):
        return raw
    Dropped.count += 1
    return None


class Pkg:
    """One vulnerable package: the installed versions and its advisory groups."""

    def __init__(self, name):
        self.name = name
        self.installed = []
        self.groups = []  # {"ids": [...], "severity": float|None, "fix": str|None}


def fixed_versions(vuln, name, installed):
    """Minimal fixed version among ECOSYSTEM ranges whose [introduced, fixed) holds `installed`."""
    best = None
    for aff in as_list(as_dict(vuln).get("affected")):
        aff = as_dict(aff)
        pkg = as_dict(aff.get("package"))
        if pkg.get("ecosystem") != "PyPI" or norm_name(pkg.get("name", "")) != name:
            continue
        for rng in as_list(aff.get("ranges")):
            rng = as_dict(rng)
            if rng.get("type") != "ECOSYSTEM":
                continue
            intro = None
            for ev in as_list(rng.get("events")):
                ev = as_dict(ev)
                if "introduced" in ev:
                    intro = parse_version(str(ev["introduced"]))
                elif "fixed" in ev:
                    fx = str(ev["fixed"])
                    fv = parse_version(fx)
                    if intro is not None and fv is not None and VERSION_TEXT_RE.match(fx) and intro <= installed < fv:
                        if best is None or fv < parse_version(best):
                            best = fx
                    intro = None
                elif "last_affected" in ev:
                    intro = None
    return best


def extract(report):
    """Vulnerable packages from an osv-scanner JSON report, untrusted values dropped."""
    pkgs = {}
    for res in as_list(as_dict(report).get("results")):
        for entry in as_list(as_dict(res).get("packages")):
            entry = as_dict(entry)
            info = as_dict(entry.get("package"))
            if info.get("ecosystem") != "PyPI":
                continue
            name = norm_name(info.get("name", ""))
            version = info.get("version")
            if not NAME_RE.match(name) or not isinstance(version, str) or not VERSION_TEXT_RE.match(version):
                Dropped.count += 1
                continue
            installed = parse_version(version)
            vulns = {v.get("id"): v for v in map(as_dict, as_list(entry.get("vulnerabilities")))}
            pkg = pkgs.setdefault(name, Pkg(name))
            pkg.installed.append(version)
            for grp in map(as_dict, as_list(entry.get("groups"))):
                raw_ids = [i for i in as_list(grp.get("ids")) if isinstance(i, str)]
                ids = sorted({i for i in map(safe_id, raw_ids) if i})
                if not ids:
                    continue
                sev = str(grp.get("max_severity", ""))
                severity = float(sev) if SEVERITY_RE.match(sev) else None
                fix = None
                if installed is not None:
                    # Every alias must be fixed for the group to disappear: take the highest minimum.
                    for rid in raw_ids:
                        cand = fixed_versions(vulns.get(rid), name, installed)
                        if cand is not None and (fix is None or parse_version(fix) < parse_version(cand)):
                            fix = cand
                pkg.groups.append({"ids": ids, "severity": severity, "fix": fix})
    return pkgs


def lock_versions(path):
    """{normalised name: Counter(version)} for every [[package]] of a uv.lock."""
    with open(path, "rb") as f:
        data = tomllib.load(f)
    out = {}
    for p in as_list(data.get("package")):
        p = as_dict(p)
        out.setdefault(norm_name(p.get("name", "")), collections.Counter())[str(p.get("version", ""))] += 1
    return out


def lock_diff(before, after, skip):
    """{name: (removed versions, added versions)} for every package other than `skip` that differs."""
    out = {}
    for name in sorted(set(before) | set(after)):
        if name == skip:
            continue
        b, a = before.get(name, collections.Counter()), after.get(name, collections.Counter())
        if b != a:
            out[name] = (sorted((b - a).elements()), sorted((a - b).elements()))
    return out


def max_version(versions):
    return max(versions, key=lambda v: parse_version(v).key)


def sev_text(values):
    values = [v for v in values if v is not None]
    return ("%g" % max(values)) if values else "unknown"


class Ctx:
    def __init__(self, root, osv, uv, excludes, allow_major):
        self.root, self.osv, self.uv = root, osv, uv
        self.excludes, self.allow_major = excludes, allow_major
        self.restore = {}  # lock path -> original bytes, for every project touched


def scan(ctx, project, lock_rel):
    """Run osv-scanner over one lock: {} when clean, the parsed report on findings."""
    cmd = [ctx.osv, "scan", "source"]
    if os.path.isfile(os.path.join(ctx.root, project, "osv-scanner.toml")):
        cmd += ["--config", posixpath.join(project, "osv-scanner.toml")]
    elif os.path.isfile(os.path.join(ctx.root, "osv-scanner.toml")):
        cmd += ["--config", "osv-scanner.toml"]
    cmd += ["--lockfile", lock_rel, "--format", "json"]
    try:
        proc = subprocess.run(cmd, cwd=ctx.root, capture_output=True, text=True)
    except OSError as e:
        raise UsageError(f"cannot run osv-scanner: {e.strerror}")
    if proc.returncode == 0:
        return {}
    if proc.returncode != 1:
        raise UsageError(f"osv-scanner failed on {lock_rel} (exit {proc.returncode})")
    try:
        return json.loads(proc.stdout)
    except ValueError:
        raise UsageError(f"osv-scanner printed no JSON for {lock_rel}")


def run_uv(ctx, project_dir, args):
    env = dict(os.environ, NO_COLOR="1")
    try:
        return subprocess.run(ctx.uv + args, cwd=project_dir, capture_output=True, text=True, env=env)
    except OSError as e:
        raise UsageError(f"cannot run uv: {e.strerror}")


def write_lock(path, data):
    with open(path, "wb") as f:
        f.write(data)


def fix_project(ctx, project):
    """Fix one project. Returns {"fixed", "cascades", "unfixed", "aborted"}; refusals never raise."""
    lock_rel = posixpath.normpath(posixpath.join(project, "uv.lock"))
    lock = os.path.join(ctx.root, lock_rel)
    proj_dir = os.path.join(ctx.root, project)
    res = {"project": project, "lock": lock_rel, "fixed": [], "cascades": [], "unfixed": [], "aborted": ""}
    with open(lock, "rb") as f:
        orig = f.read()
    ctx.restore[lock] = orig

    def unfixed(pkg, groups, fix, reason, detail=""):
        ids = sorted({i for g in groups for i in g["ids"]})
        res["unfixed"].append({
            "package": pkg.name, "installed": ", ".join(sorted(set(pkg.installed))), "ids": ids,
            "severity": sev_text(g["severity"] for g in groups), "fixed": fix, "reason": reason, "detail": detail,
        })

    pkgs = extract(scan(ctx, project, lock_rel))
    for name in sorted(pkgs):
        pkg = pkgs[name]
        nofix = [g for g in pkg.groups if not g["fix"]]
        todo = [g for g in pkg.groups if g["fix"]]
        if nofix:
            unfixed(pkg, nofix, "", "no-fixed-version")
        if not todo:
            continue
        target = max_version([g["fix"] for g in todo])
        lowest = min(pkg.installed, key=lambda v: (parse_version(v) or parse_version("0")).key)
        low_v, target_v = parse_version(lowest), parse_version(target)
        if low_v is not None and target_v <= low_v:
            continue
        pattern = excluded_by(name, ctx.excludes)
        if pattern:
            unfixed(pkg, todo, target, f"excluded (pattern '{pattern}')")
            continue
        if not ctx.allow_major and is_major(lowest, target):
            unfixed(pkg, todo, target, f"major {lowest} -> {target}")
            continue

        with open(lock, "rb") as f:
            before_bytes = f.read()
        before = lock_versions(lock)
        proc = run_uv(ctx, proj_dir, ["lock", "--upgrade-package", f"{name}=={target}"])
        output = (proc.stdout or "") + (proc.stderr or "")
        if proc.returncode != 0:
            write_lock(lock, before_bytes)
            if REFUSAL_RE.search(output):
                unfixed(pkg, todo, target, "outside constraints", clean_text(output))
                continue
            raise UvError(f"uv lock failed for {name}=={target} in {project} (exit {proc.returncode}): {clean_text(output, 400)}")
        try:
            after = lock_versions(lock)
        except (OSError, ValueError, tomllib.TOMLDecodeError):
            write_lock(lock, before_bytes)
            raise UvError(f"uv left an unreadable lock in {project}")
        got = sorted(after.get(name, {}))
        chosen = [v for v in got if parse_version(v) == target_v]
        if not chosen:
            write_lock(lock, before_bytes)
            unfixed(pkg, todo, target, f"resolver chose {', '.join(got) or 'none'}")
            continue
        casc = lock_diff(before, after, name)
        blocked = None
        for other, (removed, added) in casc.items():
            hit = excluded_by(other, ctx.excludes)
            if hit:
                blocked = (other, f"excluded (pattern '{hit}')")
            elif removed and added and not ctx.allow_major and is_major(max_version(removed), max_version(added)):
                blocked = (other, f"major {max_version(removed)} -> {max_version(added)}")
            if blocked:
                break
        if blocked:
            write_lock(lock, before_bytes)
            unfixed(pkg, todo, target, f"cascade into {blocked[0]} ({blocked[1]})")
            continue
        for other, (removed, added) in casc.items():
            res["cascades"].append({"package": name, "other": other, "from": "/".join(removed) or "-", "to": "/".join(added) or "-"})
        res["fixed"].append({
            "package": name, "from": lowest, "to": chosen[0],
            "ids": sorted({i for g in todo for i in g["ids"]}),
            "firsts": sorted({g["ids"][0] for g in todo}),
            "severity": sev_text(g["severity"] for g in todo),
        })

    if res["fixed"]:
        report2 = scan(ctx, project, lock_rel)
        remaining = set()
        for r in as_list(as_dict(report2).get("results")):
            for entry in as_list(as_dict(r).get("packages")):
                for grp in as_list(as_dict(entry).get("groups")):
                    remaining.update(i for i in as_list(as_dict(grp).get("ids")) if isinstance(i, str))
        claimed = {i for f in res["fixed"] for i in f["ids"]}
        still = sorted(claimed & remaining)
        if still:
            write_lock(lock, orig)
            res["aborted"] = "still reported " + ", ".join(still)
            for f in res["fixed"]:
                res["unfixed"].append({
                    "package": f["package"], "installed": f["from"], "ids": f["ids"], "severity": f["severity"],
                    "fixed": f["to"], "reason": "aborted: " + res["aborted"], "detail": "",
                })
            res["fixed"], res["cascades"] = [], []
    return res


def load_projects(text, root):
    try:
        raw = json.loads(text)
    except ValueError:
        raise UsageError("--projects is not JSON")
    if not isinstance(raw, list) or not raw or not all(isinstance(p, str) for p in raw):
        raise UsageError("--projects must be a non-empty JSON array of strings")
    out = set()
    for p in raw:
        if not PROJECT_RE.match(p) or p.startswith("/") or ".." in p.split("/"):
            raise UsageError("a project must be a relative path of [A-Za-z0-9._/-] without '..'")
        n = posixpath.normpath(p)
        if not os.path.isfile(os.path.join(root, n, "uv.lock")):
            raise UsageError(f"{n}/uv.lock not found; every project needs a committed lock")
        out.add(n)
    return sorted(out)


def write(out, name, text):
    with open(os.path.join(out, name), "w", encoding="utf-8") as f:
        f.write(text)


def link(i):
    return f"[{i}](https://osv.dev/vulnerability/{i})"


def uv_version(ctx):
    try:
        proc = subprocess.run(ctx.uv + ["--version"], capture_output=True, text=True)
        return clean_text(proc.stdout.strip().splitlines()[0], 100) if proc.returncode == 0 and proc.stdout.strip() else "unknown"
    except OSError:
        return "unknown"


def check_uv_pin(ctx):
    """None when <root>/.uv-version is absent or matches `<uv> --version`, else the ::error line."""
    try:
        with open(os.path.join(ctx.root, ".uv-version"), encoding="utf-8") as f:
            want = f.read().strip().splitlines()[0].strip().lstrip("v")
    except (OSError, IndexError):
        return None
    if not want:
        return None
    m = re.match(r"^uv (\S+)", uv_version(ctx))
    got = m.group(1) if m else "unknown"
    if got == want:
        return None
    return (f"::error file=.uv-version::uv {got} does not match .uv-version {want}; a different uv rewrites "
            f'the lock format. Rerun with --uv "uvx uv@{want}".')


def write_outputs(ctx, results, out):
    fixed = [(r["project"], f) for r in results for f in r["fixed"]]
    claimed = sorted({(p, i) for p, f in fixed for i in f["ids"]})
    firsts = sorted({i for _, f in fixed for i in f["firsts"]})
    unfixed = [(r["project"], u) for r in results for u in r["unfixed"]]
    aborts = [(r["project"], r["aborted"]) for r in results if r["aborted"]]
    changed = []
    for r in results:
        lock = os.path.join(ctx.root, r["lock"])
        with open(lock, "rb") as f:
            if f.read() != ctx.restore[lock]:
                changed.append(r["lock"])
    digest = hashlib.sha256("".join(f"{p}\t{i}\n" for p, i in claimed).encode()).hexdigest()[:12]

    title = "fix(deps): patch " + ", ".join(firsts)
    if len(title) > 72:
        title = f"fix(deps): patch {len(firsts)} Python advisories"

    write(out, "changed.txt", "true\n" if changed else "false\n")
    write(out, "unfixed.txt", "true\n" if unfixed or aborts else "false\n")
    write(out, "aborted.txt", "true\n" if aborts else "false\n")
    write(out, "locks.txt", "".join(f"{p}\n" for p in sorted(changed)))
    write(out, "hash.txt", digest + "\n")
    write(out, "ids.txt", "".join(f"{i}\n" for i in sorted({i for _, i in claimed})))
    write(out, "title.txt", title + "\n")
    write(out, "abort.tsv", "".join(f"{p}\t{cell(a)}\n" for p, a in aborts))
    write(out, "fixed.tsv", "".join(
        f"{p}\t{f['package']}\t{f['from']}\t{f['to']}\t{','.join(f['ids'])}\t{f['severity']}\n" for p, f in fixed))
    write(out, "cascade.tsv", "".join(
        f"{r['project']}\t{c['package']}\t{c['other']}\t{c['from']}\t{c['to']}\n" for r in results for c in r["cascades"]))
    write(out, "unfixed.tsv", "".join(
        f"{p}\t{u['package']}\t{u['installed']}\t{','.join(u['ids'])}\t{u['severity']}\t{u['fixed']}\t{cell(u['reason'])}\n"
        for p, u in unfixed))

    body = []
    if fixed:
        body.append(f"Patches {len(claimed)} advisory id(s) in {len(changed)} uv.lock file(s). Only lock files change.")
        for r in results:
            if not r["fixed"]:
                continue
            body += ["", f"### {r['project']}"]
            for f in r["fixed"]:
                ids = ", ".join(link(i) for i in f["ids"])
                body.append(f"- `{f['package']}` `{f['from']}` -> `{f['to']}`: {ids} (max severity {f['severity']})")
            if r["cascades"]:
                body += ["", "Also moved by the resolver:"]
                for c in r["cascades"]:
                    body.append(f"- `{c['other']}` `{c['from']}` -> `{c['to']}` (pulled by `{c['package']}`)")
        body += [
            "",
            f"- Resolver: `{uv_version(ctx)}`, `uv lock --upgrade-package <pkg>==<fixed>` per package.",
            "- osv-scanner re-verified these IDs are gone from the patched locks.",
            "- This PR is never auto-merged: review it and merge it by hand.",
        ]
    write(out, "pr-body.md", "\n".join(body) + "\n" if body else "")

    issue = [MARKER, "Python advisories that `python-vuln-fix` will not patch automatically. Updated by the weekly run; never closed automatically.", ""]
    if unfixed:
        issue += ["| project | package installed | ids | max severity | fixed | reason |", "| --- | --- | --- | --- | --- | --- |"]
        for p, u in unfixed:
            issue.append("| " + " | ".join(cell(x) for x in (
                p, f"{u['package']} {u['installed']}", ", ".join(link(i) for i in u["ids"]), u["severity"], u["fixed"] or "none", u["reason"])) + " |")
        for p, u in unfixed:
            if u["detail"]:
                issue += ["", f"#### {cell(p)} {cell(u['package'])}: uv output", "", "~~~", u["detail"], "~~~"]
    if aborts:
        issue += ["", "### Automatic fix aborted"]
        for p, a in aborts:
            issue.append(f"- {cell(p)}: {cell(a)}; its lock was restored.")
    issue += ["", "Each reason says what blocks the fix: no fixed release, an excluded dependency, a major bump (needs `allow-major`), or a constraint in pyproject.toml."]
    write(out, "issue-body.md", "\n".join(issue) + "\n")

    print(f"projects: {len(results)}; fixed packages: {len(fixed)}; unfixed rows: {len(unfixed)}; aborted: {len(aborts)}; changed locks: {len(changed)}")
    return 0


def cmd_run(args):
    root = os.path.abspath(args.root)
    excludes = parse_excludes(args.exclude)
    projects = load_projects(args.projects, root)

    def binary(x):
        return os.path.abspath(x) if os.sep in x else (shutil.which(x) or x)

    try:
        uv_cmd = shlex.split(args.uv)
    except ValueError as e:
        raise UsageError(f"--uv is not a valid command: {e}")
    if not uv_cmd:
        raise UsageError("--uv is empty")
    uv_cmd[0] = binary(uv_cmd[0])
    ctx = Ctx(root, binary(args.osv_scanner), uv_cmd, excludes, args.allow_major == "true")
    mismatch = check_uv_pin(ctx)
    if mismatch:
        print(mismatch)
        return 2
    out = os.path.abspath(args.out)
    os.makedirs(out, exist_ok=True)
    results = []
    try:
        for project in projects:
            results.append(fix_project(ctx, project))
    except (UsageError, UvError):
        for lock, data in ctx.restore.items():
            write_lock(lock, data)
        raise
    if Dropped.count:
        print(f"::warning::dropped {Dropped.count} untrusted value(s) from the scanner output", file=sys.stderr)
    return write_outputs(ctx, results, out)


def main(argv=None):
    ap = argparse.ArgumentParser(prog="python_vuln_fix.py")
    sub = ap.add_subparsers(dest="cmd", required=True)
    run = sub.add_parser("run")
    run.add_argument("--projects", required=True)
    run.add_argument("--out", required=True)
    run.add_argument("--osv-scanner", default="osv-scanner")
    run.add_argument("--uv", default="uv")
    run.add_argument("--exclude", default="")
    run.add_argument("--allow-major", choices=["true", "false"], default="false")
    run.add_argument("--root", default=".")
    args = ap.parse_args(argv)
    try:
        return cmd_run(args)
    except UsageError as e:
        print(f"python-vuln-fix: {e}", file=sys.stderr)
        return 2
    except UvError as e:
        print(f"python-vuln-fix: {e}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
# <<< python-vuln-fix body
