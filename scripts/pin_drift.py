#!/usr/bin/env python3
"""Report manually pinned tool versions that are behind upstream.

Reads pin-manifest.json, resolves every pin location in the checkout, asks the
GitHub REST API for the latest upstream version and writes a markdown report.
Run by .github/workflows/pin-drift.yml; the Go test internal/syncheck/pin_manifest_test.go
keeps the manifest complete and resolvable.

Usage: scripts/pin_drift.py --manifest pin-manifest.json --out report.md [--root DIR]
Writes behind=N and unknown=M to $GITHUB_OUTPUT when set, and prints them.
Exit: 0 scan ran (pins may be behind), 2 manifest error (a location does not resolve).
"""

import argparse
import datetime
import json
import os
import re
import sys
import urllib.error
import urllib.request

if sys.version_info < (3, 11):
    sys.exit("::error::pin_drift needs Python >= 3.11")

MARKER = "<!-- pin-drift -->"
DOCS_URL = "https://github.com/tclavelloux/promy-github-workflows/blob/main/docs/pin-bumps.md"
API = "https://api.github.com"


class ManifestError(Exception):
    pass


def strip_v(version):
    return version[1:] if version.startswith("v") else version


def semver(version):
    """Tuple of ints for a plain X.Y.Z (any number of numeric parts), else None."""
    if not re.fullmatch(r"\d+(\.\d+)*", version):
        return None
    return tuple(int(p) for p in version.split("."))


def resolve_location(loc, root):
    """Current version string at a manifest location. Raises ManifestError."""
    rel = loc.get("file")
    if not rel:
        raise ManifestError(f"location has no file: {loc!r}")
    try:
        with open(os.path.join(root, rel), encoding="utf-8") as fh:
            text = fh.read()
    except OSError as exc:
        raise ManifestError(f"cannot read {rel}: {exc}") from exc
    if "const" in loc:
        name = loc["const"]
        found = re.findall(r'^\s*' + re.escape(name) + r'\s*=\s*"([^"]*)"', text, re.MULTILINE)
        what = f"const {name} in {rel}"
    elif "pattern" in loc:
        try:
            rx = re.compile(loc["pattern"], re.MULTILINE)
        except re.error as exc:
            raise ManifestError(f"bad pattern for {rel}: {exc}") from exc
        if rx.groups != 1:
            raise ManifestError(f"pattern for {rel} must have exactly one capture group, has {rx.groups}")
        found = rx.findall(text)
        what = f"pattern {loc['pattern']!r} in {rel}"
    else:
        value = text.strip()
        found = [value] if value else []
        what = f"file {rel}"
    if len(found) != 1:
        raise ManifestError(f"{what} matched {len(found)} times, want exactly 1")
    value = strip_v(found[0].strip())
    if semver(value) is None:
        raise ManifestError(f"{what} resolved to {value!r}, not a numeric version")
    return value


def describe(loc):
    return f"{loc['file']}" + (f" `{loc['const']}`" if "const" in loc else "")


def github_fetch(path):
    """GET a GitHub API path and return parsed JSON. Raises on any failure."""
    req = urllib.request.Request(API + path, headers={
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "promy-pin-drift",
    })
    token = os.environ.get("GH_TOKEN")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def latest_version(upstream, fetch):
    """Latest upstream version string (no leading v). Raises on lookup failure."""
    repo = upstream["repo"]
    kind = upstream["kind"]
    if kind == "release":
        data = fetch(f"/repos/{repo}/releases/latest")
        tag = data.get("tag_name") if isinstance(data, dict) else None
        if not tag:
            raise ValueError(f"{repo}: latest release has no tag_name")
        prefix = upstream.get("tag_prefix", "")
        if prefix and tag.startswith(prefix):
            tag = tag[len(prefix):]
        version = strip_v(tag)
        if semver(version) is None:
            raise ValueError(f"{repo}: latest release tag {tag!r} is not a plain version")
        return version
    if kind == "tag":
        prefix = upstream["prefix"]
        # matching-refs returns every matching ref in one response and ignores
        # page/per_page (verified 2026-10-28), so a page loop would never end.
        refs = fetch(f"/repos/{repo}/git/matching-refs/tags/{prefix}")
        if not isinstance(refs, list):
            raise ValueError(f"{repo}: unexpected matching-refs response")
        head = "refs/tags/" + prefix
        best = None
        for ref in refs:
            name = ref.get("ref", "")
            if not name.startswith(head):
                continue
            sv = semver(name[len(head):])
            if sv is not None and (best is None or sv > best[0]):
                best = (sv, name[len(head):])
        if best is None:
            raise ValueError(f"{repo}: no tag matching {prefix}X.Y.Z")
        return best[1]
    raise ManifestError(f"unknown upstream kind {kind!r}")


def scan(manifest, root, fetch):
    """One result dict per pin. Raises ManifestError for unresolvable locations."""
    results = []
    for pin in manifest["pins"]:
        values = []
        for loc in pin["locations"]:
            try:
                values.append((loc, resolve_location(loc, root)))
            except ManifestError as exc:
                raise ManifestError(f"pin {pin['name']!r}: {exc}") from exc
        if not values:
            raise ManifestError(f"pin {pin['name']!r} has no locations")
        current = min((v for _, v in values), key=semver)
        res = {"name": pin["name"], "current": current, "locations": values,
               "disagree": len({v for _, v in values}) > 1, "latest": None, "error": None}
        try:
            res["latest"] = latest_version(pin["upstream"], fetch)
            res["status"] = "behind" if semver(res["latest"]) > semver(current) else "current"
        except ManifestError:
            raise
        except (OSError, ValueError, KeyError, urllib.error.URLError) as exc:
            res["status"] = "unknown"
            res["error"] = f"{type(exc).__name__}: {exc}"
        results.append(res)
    return results


def cell(text):
    return str(text).replace("|", "\\|").replace("\n", " ")


def render(results, today):
    behind = sum(r["status"] == "behind" for r in results)
    unknown = sum(r["status"] == "unknown" for r in results)
    lines = [
        MARKER,
        f"{behind} pin(s) behind upstream, {unknown} unknown. Scanned {today.isoformat()}. "
        f"Bump procedures: [docs/pin-bumps.md]({DOCS_URL}).",
        "",
        "| Pin | Current | Latest | Status | Locations |",
        "|---|---|---|---|---|",
    ]
    for r in results:
        locs = "; ".join(
            describe(loc) + (f" ({v})" if r["disagree"] else "") for loc, v in r["locations"])
        status = r["status"] + (" (locations disagree)" if r["disagree"] else "")
        latest = r["latest"] or "?"
        lines.append(f"| {cell(r['name'])} | {r['current']} | {latest} | {status} | {cell(locs)} |")
    errors = [r for r in results if r["error"]]
    if errors:
        lines += ["", "Lookup failures:"]
        lines += [f"- {r['name']}: `{cell(r['error'])}`" for r in errors]
    lines.append("")
    return "\n".join(lines), behind, unknown


def main(argv=None, fetch=github_fetch):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--manifest", default="pin-manifest.json")
    ap.add_argument("--out", required=True)
    ap.add_argument("--root", default=".")
    args = ap.parse_args(argv)
    try:
        with open(args.manifest, encoding="utf-8") as fh:
            manifest = json.load(fh)
        results = scan(manifest, args.root, fetch)
    except (OSError, ValueError, KeyError, ManifestError) as exc:
        print(f"::error::pin manifest error: {exc}", file=sys.stderr)
        return 2
    report, behind, unknown = render(results, datetime.datetime.now(datetime.timezone.utc).date())
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(report)
    print(f"behind={behind}\nunknown={unknown}")
    gh_out = os.environ.get("GITHUB_OUTPUT")
    if gh_out:
        with open(gh_out, "a", encoding="utf-8") as fh:
            fh.write(f"behind={behind}\nunknown={unknown}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
