#!/usr/bin/env python3
"""Parity harness: runs the Python kb (kb.py) and the Go kb side by side and compares what they do.

    python parity/parity.py [--kb ../convkb] [--go path/to/kb.exe] [--work DIR] [--report FILE] [--only SECTION]

Every case runs each version in turn in the same freshly made copy of a knowledge base (a copy of the
real one, or an empty one), at the same path, so outputs can be compared byte for byte. The real
knowledge base is only ever read, to make the first copy. Session sources are frozen first: the
session files `kb snapshot` reads are copied into a private projects folder (CLAUDE_PROJECTS_DIR), so a
conversation still being written cannot make the two runs differ.

What is compared, per case: exit status, stdout, stderr, and every file the command created, changed
or deleted. Differences the design allows are normalised away and listed in the report: the snapshot
and ingest headers' snapshot_at / ingested_at / system_version / system_hash / source, statement-file
timestamps and stamps, the install folder `kb where` prints, the files `kb version` hashes, and the
text of an unexpected crash (Python prints a traceback, Go one line; both exit 1).

Needs: Python 3.13 with the repository's kb.py, Go (unless --go is given), Windows or POSIX.
"""
from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import os
import platform
import re
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import zipfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
PY_KB = [sys.executable, str(REPO / "kb.py")]

# ---------------------------------------------------------------- bookkeeping

RESULTS: list[dict] = []


def record(section, name, status, detail=""):
    RESULTS.append({"section": section, "case": name, "status": status, "detail": detail})
    mark = {"same": ".", "expected": "e", "diff": "X"}[status]
    print(mark, end="", flush=True)
    if status == "diff":
        print(f"\n  DIFF {section}: {name}\n    " + detail.replace("\n", "\n    ")[:3000], flush=True)


class Out:
    def __init__(self, rc, raw_out: bytes, raw_err: bytes):
        self.rc, self.raw_out, self.raw_err = rc, raw_out, raw_err
        self.stdout, self.stderr = raw_out.decode("utf-8", "replace"), raw_err.decode("utf-8", "replace")


def split_crash(o: Out):
    """(stderr before the crash, "Type: message") when the run died of an unexpected exception: Python's
    traceback ends with that line; Go prints "kb: unexpected Type: message". Else None."""
    err = o.stderr
    if "Traceback (most recent call last)" in err:
        before = err[:err.index("Traceback (most recent call last)")]
        lines = [l for l in err.splitlines() if l.strip()]
        return before, lines[-1].strip()
    i = err.find("kb: unexpected ")
    if i >= 0 and (i == 0 or err[i - 1] == "\n"):
        return err[:i], err[i + len("kb: unexpected "):].strip()
    return None


# ---------------------------------------------------------------- environment

def base_env(extra=None, drop=()):
    env = {k: v for k, v in os.environ.items()
           if k not in ("CLAUDE_CODE_SESSION_ID", "KENT_KB", "KB_USER", "CLAUDE_PROJECTS_DIR", "COLUMNS", "LINES")}
    env["PYTHONIOENCODING"] = "utf-8"
    env["KB_USER"] = "jamie"
    env["CLAUDE_PROJECTS_DIR"] = str(G.projects)
    for k in drop:
        env.pop(k, None)
    env.update(extra or {})
    return env


def run(impl, args, cwd, env=None, timeout=300) -> Out:
    cmd = (PY_KB if impl == "py" else [str(G.go)]) + list(args)
    p = subprocess.run(cmd, cwd=cwd, env=env or base_env(), capture_output=True, timeout=timeout)
    return Out(p.returncode, p.stdout, p.stderr)


class G:  # globals set in main
    go: Path
    work: Path
    projects: Path
    real_kb: Path
    bases: dict[str, Path] = {}
    base_hashes: dict[str, dict[str, str]] = {}


def file_hashes(root: Path) -> dict[str, str]:
    out = {}
    for p in root.rglob("*"):
        if p.is_file():
            out[p.relative_to(root).as_posix()] = hashlib.sha256(p.read_bytes()).hexdigest()
    return out


def workspace(base: str) -> Path:
    w = G.work / "kb"
    if w.exists():
        shutil.rmtree(w)
    shutil.copytree(G.bases[base], w)
    return w


# ---------------------------------------------------------------- normalisation

# What legitimately differs between the two runs: the time of the act and the content hash of the system.
# Only those values are blanked, in the raw bytes; everything else (key order, escaping, number format,
# source, system_version) must be identical.
HEADER_VOLATILE_RE = re.compile(rb'"(snapshot_at|ingested_at|system_hash)": "[^"]*"')
TS_RE = re.compile(rb'"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\+00:00"')
HASH_RE = re.compile(rb'^(\s*(?:- )?(?:withdrawn_)?system_hash): "sha256:[0-9a-f]{64}"', re.M)


def norm_file(rel: str, data: bytes) -> bytes:
    if rel.endswith(".jsonl"):
        first, nl, rest = data.partition(b"\n")
        return HEADER_VOLATILE_RE.sub(rb'"\1": "<volatile>"', first) + nl + rest
    if rel.endswith(".md"):
        return HASH_RE.sub(rb'\1: "<HASH>"', TS_RE.sub(b'"<TS>"', data))
    return data


def changed_files(w: Path, base: str) -> dict[str, bytes | None]:
    before = G.base_hashes[base]
    now = file_hashes(w)
    out = {}
    for rel, h in now.items():
        if rel.startswith("index.db"):
            continue
        if before.get(rel) != h:
            out[rel] = (w / rel).read_bytes()
    for rel in before:
        if rel not in now and not rel.startswith("index.db"):
            out[rel] = None
    return out


def first_diff(a: str, b: str, label: str) -> str:
    al, bl = a.splitlines(), b.splitlines()
    for i, (x, y) in enumerate(zip(al, bl)):
        if x != y:
            return f"{label} line {i + 1}:\n  py: {x[:400]!r}\n  go: {y[:400]!r}"
    return f"{label}: py has {len(al)} lines, go has {len(bl)} lines" + (
        f"\n  py: {al[len(bl)][:300]!r}" if len(al) > len(bl) else (f"\n  go: {bl[len(al)][:300]!r}" if len(bl) > len(al) else ""))


# ---------------------------------------------------------------- the pair runner

def pair(section, name, steps, base="convkb", setup=None, env=None, cwd_sub="", norm=None, allow=None, expect=None,
         known=None):
    """Run the steps (lists of kb arguments) with each version in a fresh copy of `base`; compare.

    setup(w) prepares the copy (the same for both). norm(text) normalises stdout/stderr. allow(py, go)
    returns a reason string when a remaining stderr difference is expected. expect is the exit status the
    last step must have (earlier steps must succeed), so a case cannot pass by both versions failing alike.
    known names a deliberate deviation: the case is reported as expected, with what differed."""
    res = {}
    for impl in ("py", "go"):
        w = workspace(base)
        if setup:
            setup(w)
        outs = []
        for st in steps:
            st_env = env(w) if callable(env) else env
            args = st(w) if callable(st) else st
            outs.append(run(impl, args, w / cwd_sub, st_env))
        res[impl] = (outs, changed_files(w, base))
    problems = []      # stdout, exit status, files
    err_problems = []  # stderr only
    expected = []
    if expect is not None:
        rcs = [o.rc for o in res["py"][0]]
        want = [0] * (len(rcs) - 1) + [expect]
        if rcs != want:
            problems.append(f"Python's exit statuses {rcs}, expected {want}: the case does not exercise what it claims")
    for i, (po, go) in enumerate(zip(res["py"][0], res["go"][0])):
        tag = f"step {i + 1} " if len(steps) > 1 else ""
        if norm:
            same_out = norm(po.stdout) == norm(go.stdout)
            pse, gse = norm(po.stderr), norm(go.stderr)
            same_err = pse == gse
        else:
            same_out, same_err = po.raw_out == go.raw_out, po.raw_err == go.raw_err
            pse, gse = po.stderr, go.stderr
        if po.rc != go.rc:
            problems.append(f"{tag}exit status py={po.rc} go={go.rc}")
        if not same_out:
            problems.append(first_diff(po.stdout, go.stdout, f"{tag}stdout"))
        if not same_err:
            pc, gc = split_crash(po), split_crash(go)
            if pc and gc and (norm(pc[0]) if norm else pc[0]) == (norm(gc[0]) if norm else gc[0]) and pc[1] == gc[1]:
                expected.append(f"{tag}both raised {pc[1][:240]} (Python prints a traceback, Go one line)")
            else:
                err_problems.append(first_diff(pse, gse, f"{tag}stderr"))
    pf, gf = res["py"][1], res["go"][1]
    if set(pf) != set(gf):
        problems.append(f"changed files differ: py only {sorted(set(pf) - set(gf))}, go only {sorted(set(gf) - set(pf))}")
    for rel in sorted(set(pf) & set(gf)):
        a, b = pf[rel], gf[rel]
        if (a is None) != (b is None):
            problems.append(f"{rel}: deleted by one version only")
        elif a is not None and norm_file(rel, a) != norm_file(rel, b):
            problems.append(first_diff(norm_file(rel, a).decode("utf-8", "backslashreplace"),
                                       norm_file(rel, b).decode("utf-8", "backslashreplace"), rel))
    if err_problems and not problems and allow:
        reason = allow(res["py"][0], res["go"][0])
        if reason:
            record(section, name, "expected", reason + "\n" + "\n".join(err_problems)[:600])
            return res
    problems += err_problems
    if problems and known:
        record(section, name, "expected", known + "\n" + "\n".join(problems)[:600])
        return res
    if problems:
        record(section, name, "diff", "\n".join(problems))
    elif expected:
        record(section, name, "expected", "; ".join(expected))
    else:
        record(section, name, "same", f"rc={res['py'][0][-1].rc}; {len(pf)} file(s) written")
    return res


# ---------------------------------------------------------------- knowledge-base facts

def load_jsonl(p: Path):
    return [json.loads(l) for l in p.read_text(encoding="utf-8").splitlines() if l.strip()]


def sessions_of(kb: Path):
    out = []
    for d in (kb / "evidence", kb / "off-record" / "sessions"):
        for p in sorted(d.glob("*.jsonl")):
            recs = load_jsonl(p)
            h = recs[0]
            if h.get("type") == "kb-snapshot":
                out.append((p, h, recs[1:]))
    return out


def documents_of(kb: Path):
    out = []
    for p in sorted((kb / "evidence").glob("*-doc-*.jsonl")):
        recs = load_jsonl(p)
        out.append((p, recs[0], recs[1:]))
    return out


def meta_of(p: Path) -> dict:
    sys.path.insert(0, str(REPO))
    from kb.common import parse_frontmatter  # noqa: E402
    return parse_frontmatter(p.read_text(encoding="utf-8"))[0]


def body_of(p: Path) -> str:
    sys.path.insert(0, str(REPO))
    from kb.common import load_statement  # noqa: E402
    return load_statement(p).body


def write(p: Path, s: str):
    """Write exactly these characters (no newline translation), as UTF-8."""
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(s.encode("utf-8"))


def draft(w: Path, name: str, meta: str, body: str):
    write(w / "off-record" / "drafts" / name, "---\n" + meta.strip("\n") + "\n---\n\n" + body + "\n")
    return f"off-record/drafts/{name}"


# ---------------------------------------------------------------- sections

def sec_cli():
    S = "cli"
    cases = ["", "-h", "foo", "snapshot -h", "ingest", "ingest -h", "show -h", "attest -h", "record", "record -h",
             "seen -h", "withdraw -h", "withdraw A-1", "search", "search -h", "search x --limit q", "index extra",
             "version -h", "serve -h", "show a --human-only b", "check --x", "snapshot a b", "--hel", "-hx",
             "snapshot --mode", "snapshot --force=1", "attest --tu x", "where -h", "--foo snapshot",
             "check -h", "index -h", "search -- -x --limit 2"]
    for c in cases:
        pair(S, f"kb {c}", [c.split()], base="empty")
    pair(S, "kb attest -h, COLUMNS=50", [["attest", "-h"]], base="empty", env=base_env({"COLUMNS": "50"}))
    pair(S, "kb -h, COLUMNS=200", [["-h"]], base="empty", env=base_env({"COLUMNS": "200"}))


def sec_location():
    S = "location"
    pair(S, "no knowledge base", [["check"]], base="empty", cwd_sub="..")
    pair(S, "KENT_KB names a folder without the record", [["check"]], base="empty",
         env=lambda w: base_env({"KENT_KB": str(w / "evidence")}))
    pair(S, "KENT_KB names the knowledge base (run from elsewhere)", [["show"]], base="convkb", cwd_sub="..",
         env=lambda w: base_env({"KENT_KB": str(w)}))
    pair(S, "found from a folder above", [["show"]], base="convkb", cwd_sub="evidence")
    # kb where: the install folder differs by design (the Python package's folder vs the executable's)
    kent_line = lambda s: re.sub(r"(?m)^kent: .*$", "kent: <install folder>", s)
    pair(S, "kb where (install folder normalised)", [["where"]], base="convkb", norm=kent_line)
    pair(S, "kb where, KENT_KB in other case (normalised)", [["where"]], base="convkb", cwd_sub="..",
         env=lambda w: base_env({"KENT_KB": str(w).lower() if os.name == "nt" else str(w)}), norm=kent_line)
    pair(S, "kb where, nothing found", [["where"]], base="empty", cwd_sub="..", norm=kent_line)
    # kb version: same version line; the hash covers different files by design
    o = {impl: run(impl, ["version"], G.work) for impl in ("py", "go")}
    same_ver = o["py"].stdout.splitlines()[0] == o["go"].stdout.splitlines()[0]
    record(S, "kb version", "expected" if same_ver else "diff",
           f"version lines {'equal' if same_ver else 'differ'}: {o['py'].stdout.splitlines()[0]!r}; hashes differ by design "
           f"(py {o['py'].stdout.splitlines()[1][13:30]}…, go {o['go'].stdout.splitlines()[1][13:30]}…)")
    pair(S, "no user id", [["ingest", "x.md"]], base="convkb",
         env=lambda w: base_env({"USERPROFILE": str(w / "evidence"), "HOME": str(w / "evidence")}, drop=("KB_USER",)))
    pair(S, "user from ~/.kenttheclerk.json", [["snapshot", G.session_ids[0][0]]], base="convkb",
         setup=lambda w: write(w / "home" / ".kenttheclerk.json", '{"user": "jamie"}'),
         env=lambda w: base_env({"USERPROFILE": str(w / "home"), "HOME": str(w / "home")}, drop=("KB_USER",)))
    pair(S, "user from the older ~/.convkb.json", [["snapshot", G.session_ids[0][0]]], base="convkb",
         setup=lambda w: write(w / "home" / ".convkb.json", '{"user": "jamie"}'),
         env=lambda w: base_env({"USERPROFILE": str(w / "home"), "HOME": str(w / "home")}, drop=("KB_USER",)))


def sec_snapshot():
    S = "snapshot"
    for sid, mode, filt, user in G.session_ids:
        res = pair(S, f"re-snapshot {sid[:8]} ({mode}, {filt})", [["snapshot", sid, "--mode", mode, "--filter", filt, "--user", user]], expect=0)
        # how much of the snapshot in the record each version reproduces, line for line
        orig = next(p for p, h, _ in G.sessions if h["session_id"] == sid)
        old = orig.read_bytes().split(b"\n")[1:]
        for impl in ("py", "go"):
            new = res[impl][1].get(orig.relative_to(G.bases["convkb"]).as_posix())
            if new is not None:
                newset = set(new.split(b"\n")[1:])
                kept = sum(1 for l in old if l and l in newset)
                G.resnap_info.setdefault(sid[:8], {})[impl] = f"{kept}/{sum(1 for l in old if l)} recorded turn lines reproduced"
        pair(S, f"full-v1 into an empty base {sid[:8]}", [["snapshot", sid, "--filter", "full-v1", "--user", user]], base="empty", expect=0)
    sid0 = G.session_ids[0][0]
    on = next(s for s in G.session_ids if s[1] == "on-record")[0]
    off = next((s for s in G.session_ids if s[1] == "off-record"), None)
    pair(S, "mode change refused (on-record session as off-record)", [["snapshot", on, "--mode", "off-record"]])
    if off:
        pair(S, "mode change refused (off-record session as on-record)", [["snapshot", off[0], "--mode", "on-record"]])
    pair(S, "filter change refused without --force", [["snapshot", on, "--filter", "full-v1"]])
    pair(S, "filter change with --force", [["snapshot", on, "--filter", "full-v1", "--force"]])

    def add_fake_turn(w):
        p = next((w / "evidence").glob(f"*{on[:8]}.jsonl"))
        with p.open("a", encoding="utf-8", newline="\n") as fh:
            fh.write(json.dumps({"type": "turn", "uuid": "00000000-dead-beef-0000-000000000000", "role": "user",
                                 "author": "jamie", "content": []}) + "\n")
    pair(S, "append-only: a re-snapshot that would drop a turn", [["snapshot", on]], setup=add_fake_turn)
    pair(S, "append-only with --force", [["snapshot", on, "--force"]], setup=add_fake_turn)

    def header_mode_off(w):
        p = next((w / "evidence").glob(f"*{on[:8]}.jsonl"))
        lines = p.read_text(encoding="utf-8", newline="").split("\n")
        h = json.loads(lines[0]); h["mode"] = "off-record"
        lines[0] = json.dumps(h, ensure_ascii=False)
        p.write_text("\n".join(lines), encoding="utf-8", newline="\n")
    pair(S, "existing file has another mode", [["snapshot", on]], setup=header_mode_off)
    pair(S, "bad --mode", [["snapshot", on, "--mode", "public"]])
    pair(S, "bad --filter", [["snapshot", on, "--filter", "everything"]])
    pair(S, "unknown session id", [["snapshot", "ffffffff"]])
    pair(S, "ambiguous session id", [["snapshot", ""]] if False else [["snapshot", "0"]],
         norm=lambda s: s)  # several ids start with 0
    pair(S, "--title overrides the title", [["snapshot", on, "--title", "A title: with 'quotes' — and ü"]])
    src = next(G.projects.glob(f"*/{sid0}.jsonl"))
    pair(S, "snapshot by path to the .jsonl", [["snapshot", str(src), "--user", "jamie"]])
    # crafted sources in a private projects folder
    crafted = G.work / "crafted_projects" / "proj"
    crafted.mkdir(parents=True, exist_ok=True)
    def craft(name, lines):
        (crafted / f"{name}.jsonl").write_text("".join(l + "\n" for l in lines), encoding="utf-8", newline="\n")
    sid_ok = "11111111-2222-3333-4444-555555555555"
    rec = lambda **kw: json.dumps(kw, ensure_ascii=False)
    human = lambda u, text, parent=None, sid=sid_ok, **kw: rec(type="user", uuid=u, parentUuid=parent, sessionId=sid,
                                                             timestamp="2026-10-01T10:00:00.000Z", origin={"kind": "human"},
                                                             message={"role": "user", "content": text}, **kw)
    agent = lambda u, blocks, parent=None, sid=sid_ok: rec(type="assistant", uuid=u, parentUuid=parent, sessionId=sid,
                                                          timestamp="2026-10-01T10:00:01.000Z",
                                                          message={"model": "claude-x", "content": blocks})
    craft(sid_ok, [
        rec(type="custom-title", customTitle="Crafted: odd records"),
        rec(type="queue-operation"), rec(type="summary"), rec(type=None), rec(type=7),
        human("u1", "<system-reminder>hidden</system-reminder>Hello  there\u00a0\u3000 "),
        agent("a1", [{"type": "text", "text": "  "}, {"type": "thinking", "thinking": "secret"},
                     {"type": "tool_use", "id": "t1", "name": "Bash", "input": {"command": "x" * 300}},
                     {"type": "tool_use", "id": "t2", "name": "Read", "input": {"file_path": ""}},
                     {"type": "tool_use", "id": "t3", "name": "Edit", "input": {"description": 5}},
                     {"type": "tool_use", "id": "t4", "name": "Odd", "input": None},
                     {"type": "text", "text": "Answer with \"quotes\", tabs\t, é, 😀, \u2028 and \x7f"}], parent="u1"),
        rec(type="user", uuid="r1", parentUuid="a1", sessionId=sid_ok, timestamp="2026-10-01T10:00:02Z",
            message={"content": [{"type": "tool_result", "tool_use_id": "t1", "content": [{"type": "text", "text": "out é 😀"}]},
                                 {"type": "image", "source": {}}]}),
        rec(type="user", uuid="r2", parentUuid="r1", sessionId=sid_ok, message={"content": "[Request interrupted by user]  "}),
        rec(type="user", uuid="r3", parentUuid="r2", sessionId=sid_ok, message={"content": "<task-notification>x</task-notification>"}),
        rec(type="user", uuid="r4", parentUuid="r3", sessionId=sid_ok, isSidechain=True, message={"content": "side"}),
        rec(type="user", uuid="r5", parentUuid="r4", sessionId=sid_ok, origin={"kind": "human"},
            message={"content": [{"type": "text", "text": "list form <system-reminder>a</system-reminder> text"},
                                 {"type": "text", "text": "<system-reminder>only</system-reminder>"}]}),
        agent("a2", [{"type": "text", "text": "Numbers: " + "1.5e-7 1e16"}], parent="r5"),
        rec(type="assistant", uuid="a3", parentUuid="a2", sessionId=sid_ok, timestamp="2026-10-01T10:00:03Z",
            message={"content": [{"type": "text", "text": "no model"}], "usage": {"x": 1.0, "y": 1e-7, "z": 12345678901234567890}}),
        rec(type="ai-title", aiTitle="ignored when a custom title exists"),
    ])
    cenv = lambda w: base_env({"CLAUDE_PROJECTS_DIR": str(G.work / "crafted_projects")})
    for f in ("human-visible-v1", "full-v1"):
        pair(S, f"crafted session, {f}", [["snapshot", sid_ok, "--filter", f, "--user", "jamie"]], base="empty", env=cenv)
    sid_fork = "22222222-2222-3333-4444-555555555555"
    craft(sid_fork, [human("p1", "parent turn", sid=sid_ok), agent("p2", [{"type": "text", "text": "parent agent"}], parent="p1", sid=sid_ok),
                     rec(type="system", uuid="c0", parentUuid="p2", sessionId=sid_fork),
                     human("f1", "forked turn", parent="c0", sid=sid_fork)])
    pair(S, "crafted fork: other session's records dropped, branch point found", [["snapshot", sid_fork, "--user", "jamie"]], base="empty", env=cenv)
    sid_two = "33333333-2222-3333-4444-555555555555"
    craft(sid_two, [human("q1", "one", sid="aaaaaaaa-0000-0000-0000-000000000000"), human("q2", "two", sid="bbbbbbbb-0000-0000-0000-000000000000")])
    pair(S, "crafted: several sessionIds, none requested", [["snapshot", sid_two, "--user", "jamie"]], base="empty", env=cenv)
    sid_bad = "44444444-2222-3333-4444-555555555555"
    craft(sid_bad, [human("b1", "fine"), '{"type": "user", "uuid": "b2", ', ""])
    pair(S, "crafted: bad JSON line", [["snapshot", sid_bad, "--user", "jamie"]], base="empty", env=cenv)
    sid_nohuman = "55555555-2222-3333-4444-555555555555"
    craft(sid_nohuman, [agent("n1", [{"type": "text", "text": "only the agent"}], sid=sid_nohuman)])
    pair(S, "crafted: no human turns", [["snapshot", sid_nohuman, "--user", "jamie"]], base="empty", env=cenv)
    craft("66666666-0000-0000-0000-000000000001", [human("x1", "a", sid="66666666-0000-0000-0000-000000000001")])
    craft("66666666-0000-0000-0000-000000000002", [human("x2", "b", sid="66666666-0000-0000-0000-000000000002")])
    pair(S, "crafted: ambiguous prefix", [["snapshot", "66666666", "--user", "jamie"]], base="empty", env=cenv)

    # the no-argument default: this conversation, from CLAUDE_CODE_SESSION_ID, with its freshness guard
    pair(S, "no session and no CLAUDE_CODE_SESSION_ID", [["snapshot"]], base="empty")
    newest = sorted(crafted.glob("*.jsonl"), key=lambda p: p.stat().st_mtime)
    fresh = crafted / f"{sid_ok}.jsonl"
    def touch_fresh(w):
        t = time.time()
        os.utime(fresh, (t, t))
    pair(S, "default from CLAUDE_CODE_SESSION_ID (newest file)", [["snapshot", "--user", "jamie"]], base="empty",
         setup=touch_fresh, env=lambda w: base_env({"CLAUDE_PROJECTS_DIR": str(G.work / "crafted_projects"), "CLAUDE_CODE_SESSION_ID": sid_ok}))
    def make_stale(w):
        old = time.time() - 3600
        os.utime(crafted / f"{sid_fork}.jsonl", (old, old))
        t = time.time()
        os.utime(fresh, (t, t))
    age = lambda s: re.sub(r"last written \d+s ago", "last written <N>s ago", s)
    pair(S, "default refused: stale file and a newer one beside it", [["snapshot", "--user", "jamie"]], base="empty",
         setup=make_stale, norm=age,
         env=lambda w: base_env({"CLAUDE_PROJECTS_DIR": str(G.work / "crafted_projects"), "CLAUDE_CODE_SESSION_ID": sid_fork}))


def sec_ingest():
    S = "ingest"
    samples = G.work / "samples"
    samples.mkdir(exist_ok=True)
    files = []
    def add(name, data: bytes, extra=()):
        p = samples / name
        p.write_bytes(data)
        files.append((p, list(extra)))
    for p in (G.bases["convkb"] / "evidence" / "originals").iterdir():
        files.append((p, []))
    add("headings.md", "# Title one\n\nintro\n\n## Section two\ntext\n####### not a heading\n#nospace\n# \n#\tTabbed heading ü\n".encode())
    para = ("word " * 120).strip()
    add("paragraphs.txt", ("\n\n".join([para] * 12) + "\n\n\n").encode())
    add("crlf_bom.md", b"\xef\xbb\xbf# BOM title\r\nline\r\n\r\nmore\rlone cr\r\n")
    add("latin1.txt", "caf\xe9".encode("latin-1"))
    add("empty.txt", b"")
    add("blank.txt", b"\n\n  \n")
    add("data.csv", b"a,b\n1,2\n")
    add("data.json", b'{"a": 1}\n')
    add("UPPER.MD", b"# Upper extension\nbody\n")
    add("unicode-heading.md", "#\u3000Ideographic space heading\n\u00a0\nx\n".encode())
    add("long-heading.md", ("# " + "h" * 200 + "\nx\n").encode())
    add("image.png", bytes.fromhex("89504e470d0a1a0a0000000d49484452") + b"\x00" * 40, ["--caption", "A picture: of nothing"])
    add("blob.xyz", b"\x00\x01binary")
    add("archive.tar.gz", b"\x1f\x8b\x08\x00fake")
    add("noext", b"plain bytes")
    add(".hidden", b"dotfile")
    for p in sorted((REPO / "internal" / "htmltext" / "testdata" / "cases").glob("*.html")):
        files.append((p, []))
    add("small.html", b"<html><head><title> T&amp;itle </title><style>x{}</style></head><body><h1>Head <b>one</b></h1>"
                      b"<p>para &copy; &#8364; &nbsp;x</p><table><tr><td>a</td><th>b</th></tr></table>br<br>end"
                      b"<script>var x='</p>';</script></body></html>")
    add("page.htm", "<p>latin \xe9 text</p><h2>Heading</h2><div>after".encode("latin-1"))
    add("crash.html", b"a<![foo[x]]>b")
    for name, xml in docx_cases().items():
        buf = samples / name
        with zipfile.ZipFile(buf, "w") as z:
            if xml is not None:
                z.writestr("word/document.xml", xml)
            z.writestr("[Content_Types].xml", "<Types/>")
        files.append((buf, []))
    good_xml = docx_cases()["headings.docx"]
    with zipfile.ZipFile(samples / "bzip2.docx", "w", compression=zipfile.ZIP_BZIP2) as z:
        z.writestr("word/document.xml", good_xml)
    files.append((samples / "bzip2.docx", []))
    with zipfile.ZipFile(samples / "backslash.docx", "w") as z:
        z.writestr(zipfile.ZipInfo("word/document.xml"), good_xml)
    raw = (samples / "backslash.docx").read_bytes().replace(b"word/document.xml", b"word\\document.xml")
    (samples / "backslash.docx").write_bytes(raw)
    files.append((samples / "backslash.docx", []))
    with zipfile.ZipFile(samples / "badcrc.docx", "w", compression=zipfile.ZIP_STORED) as z:
        z.writestr("word/document.xml", good_xml)
    raw = bytearray((samples / "badcrc.docx").read_bytes())
    i = raw.index(b"Doc Title")
    raw[i] ^= 0x01
    (samples / "badcrc.docx").write_bytes(bytes(raw))
    files.append((samples / "badcrc.docx", []))
    for flag, nm in ((0x1, "encrypted.docx"), (0x40, "strong-encryption.docx")):
        with zipfile.ZipFile(samples / nm, "w") as z:
            zi = zipfile.ZipInfo("word/document.xml")
            z.writestr(zi, good_xml)
        raw = bytearray((samples / nm).read_bytes())
        for sig, off in ((b"PK\x03\x04", 6), (b"PK\x01\x02", 8)):
            j = raw.index(sig)
            raw[j + off] |= flag
        (samples / nm).write_bytes(bytes(raw))
        files.append((samples / nm, []))
    add("notazip.docx", b"PK not really")
    for p in sorted((REPO / "internal" / "pdftext" / "testdata").rglob("*.pdf")):
        files.append((p, []))
    def pypdf_logging(py, go):
        """pypdf logs what it repaired to stderr; the Go port does not. Expected when that is the only difference."""
        if all(a.rc == b.rc and a.stdout == b.stdout and not b.stderr and a.stderr for a, b in zip(py, go)):
            return "pypdf's repair warnings on stderr are not reproduced (stdout, exit status and files identical)"
    for p, extra in files:
        refused = {"latin1.txt", "empty.txt", "blank.txt", "crash.html", "notazip.docx", "nodocxml.docx", "nobody.docx",
                   "dup-attr.docx", "unbound-prefix.docx", "junk-after-root.docx", "second-root.docx", "space-before-decl.docx",
                   "mismatched.docx", "badcrc.docx", "encrypted.docx", "strong-encryption.docx"}
        pair(S, f"ingest {p.name}", [["ingest", str(p), *extra]], base="empty",
             allow=pypdf_logging if p.suffix == ".pdf" else None, expect=1 if p.name in refused else 0)
    md = samples / "headings.md"
    pair(S, "same bytes, same extractor: refused (frozen)", [["ingest", str(md)], ["ingest", str(md)]], base="empty")
    pair(S, "same bytes, another extractor: new item, original reused", [["ingest", str(md)], ["ingest", str(md), "--extractor", "opaque-v1"]], base="empty")
    copy = samples / "headings-copy.txt"
    shutil.copyfile(md, copy)
    pair(S, "same bytes under another extension: original reused", [["ingest", str(md)], ["ingest", str(copy), "--extractor", "opaque-v1"]], base="empty")
    pair(S, "same bytes in the other mode: refused", [["ingest", str(md)], ["ingest", str(md), "--mode", "off-record", "--extractor", "html-v1"]], base="empty")
    pair(S, "off-record ingest", [["ingest", str(md), "--mode", "off-record"]], base="empty")
    pair(S, "--caption on a text extractor: refused", [["ingest", str(md), "--caption", "x"]], base="empty")
    pair(S, "bad --extractor", [["ingest", str(md), "--extractor", "ocr-v1"]], base="empty")
    pair(S, "bad --mode", [["ingest", str(md), "--mode", "secret"]], base="empty")
    pair(S, "missing file", [["ingest", str(samples / "nope.md")]], base="empty")
    pair(S, "relative path with ./ and ..", [["ingest", "./samples/../samples/headings.md"]], base="empty", cwd_sub="..")
    pair(S, "--title", [["ingest", str(md), "--title", "Given title"]], base="empty")
    pair(S, "forced extractor text-v1 on html", [["ingest", str(samples / "small.html"), "--extractor", "text-v1"]], base="empty")
    pair(S, "into the convkb copy", [["ingest", str(md)]], base="convkb")


def docx_cases():
    W = 'xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"'
    def doc(body):
        return f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\r\n<w:document {W}><w:body>{body}<w:sectPr/></w:body></w:document>'
    p = lambda text, style=None: (f'<w:p>' + (f'<w:pPr><w:pStyle w:val="{style}"/></w:pPr>' if style else '')
                                  + f'<w:r><w:t xml:space="preserve">{text}</w:t></w:r></w:p>')
    return {
        "headings.docx": doc(p("Doc Title", "Title") + p("Intro &amp; more") + p("First", "Heading1") + p("body one")
                             + p("Sub", "heading2") + p("") + p("Zero", "Heading0") + p("Caps", "TITLE") + p("   ", "Heading1")),
        "table.docx": doc(p("Before") + '<w:tbl><w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r><w:r><w:br/><w:t>b</w:t></w:r></w:p></w:tc>'
                          '<w:tc><w:p><w:r><w:tab/><w:t> c </w:t></w:r></w:p></w:tc></w:tr><w:tr></w:tr>'
                          '<w:tr><w:tc><w:tbl><w:tr><w:tc><w:p><w:r><w:t>nested</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc></w:tr></w:tbl>'
                          + p("After")),
        "runs.docx": doc('<w:p><w:r><w:t>one</w:t></w:r><w:ins><w:r><w:t>two</w:t></w:r></w:ins><w:r><w:tab/></w:r>'
                         '<w:r><w:t><![CDATA[<cdata>]]></w:t></w:r><!-- comment --><w:r><w:t>x&#233;y</w:t></w:r></w:p>'
                         + "".join(p(("word " * 80).strip()) for _ in range(12))),
        "nodocxml.docx": None,
        "nobody.docx": f'<w:document {W}></w:document>',
        # declared encodings and versions expat reads
        "utf16.docx": ('\ufeff<?xml version="1.0" encoding="UTF-16"?>' + doc(p("UTF-16 text \u00e9"))[doc(p("")).index("<w:document"):]).encode("utf-16-le"),
        "latin1-declared.docx": doc(p("caf\u00e9")).replace('encoding="UTF-8"', 'encoding="ISO-8859-1"').encode("latin-1"),
        "cp1252-declared.docx": doc(p("smart \u201cquotes\u201d")).replace('encoding="UTF-8"', 'encoding="windows-1252"').encode("cp1252"),
        "ascii-declared.docx": doc(p("plain")).replace('encoding="UTF-8"', 'encoding="us-ascii"'),
        "xml11.docx": doc(p("one point one")).replace('version="1.0"', 'version="1.1"'),
        "entity.docx": doc(p("before &who; after")).replace("?>", '?><!DOCTYPE w:document [<!ENTITY who "the entity">]>', 1),
        # malformed: Python's ParseError
        "dup-attr.docx": doc('<w:p w:a="1" w:a="2"><w:r><w:t>x</w:t></w:r></w:p>'),
        "unbound-prefix.docx": doc('<q:p><w:r><w:t>x</w:t></w:r></q:p>'),
        "junk-after-root.docx": doc(p("x")) + "junk",
        "second-root.docx": doc(p("x")) + f'<w:document {W}><w:body>{p("second")}</w:body></w:document>',
        "space-before-decl.docx": "  " + doc(p("x")),
        "mismatched.docx": doc('<w:p><w:r><w:t>x</w:r></w:t></w:p>'),
        # style lookup
        "two-ppr.docx": doc('<w:p><w:pPr/><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Second pPr heading</w:t></w:r></w:p>' + p("body")),
        "dotless-i.docx": doc(p("Dotless", "Head\u0131ng1") + p("Dotted", "HEAD\u0130NG2") + p("body")),
    }


def sec_check_show_search():
    S = "read"
    pair(S, "kb check", [["check"]])
    pair(S, "kb show (list)", [["show"]])
    for sid, *_ in G.session_ids:
        pair(S, f"kb show {sid[:8]}", [["show", sid[:8]]])
    for p, h, _ in G.documents:
        pair(S, f"kb show document {h['document_id'][:8]}", [["show", h["document_id"][:8]]])
    pair(S, "kb show --human-only (all sessions)", [["show", "--human-only", *[s[0][:8] for s in G.session_ids]]])
    ptrs = []
    for p, h, turns in G.sessions[:4]:
        for t in turns[:: max(1, len(turns) // 5)]:
            ptrs.append(f"{h['session_id'][:8]}#{t['uuid'][:8]}")
    for p, h, chunks in G.documents:
        ptrs.append(f"{h['document_id'][:8]}#{chunks[0]['uuid']}")
    pair(S, f"kb show {len(ptrs)} pointers", [["show", *ptrs]])
    pair(S, "kb show unknown session", [["show", "zzzz"]])
    pair(S, "kb show unknown turn", [["show", G.session_ids[0][0][:8] + "#ffffffff"]])
    pair(S, "kb show bad pointer", [["show", "nothex#x"]])
    pair(S, "kb show by date prefix (substring match)", [["show", "2026-09-2"]])
    for q in ["attest", "kent rule", "snapshot NOT python", '"agent statement"', "superses*", "S-0090", "ü", "go AND pipeline",
              "zzzzunmatched", "a.b", "(", "title:recorder", "NEAR(kent rule)"]:
        pair(S, f"kb search {q}", [["search", *q.split()]])
    pair(S, "kb search --limit 2", [["search", "the", "--limit", "2"]])
    pair(S, "kb search --limit -1", [["search", "rule", "--limit", "-1"]])
    pair(S, "kb index", [["index"]])
    # tampered copies, to exercise kb check's findings
    st = lambda w, sid: next((w / "attested").glob(f"{sid}-*.md"))
    ag = lambda w, aid: next((w / "agent").glob(f"{aid}-*.md"))
    def edit(path_fn, fn):
        def setup(w):
            p = path_fn(w)
            raw = p.read_bytes().decode("utf-8")
            crlf = "\r\n" in raw
            old = raw.replace("\r\n", "\n")
            new = fn(old)
            assert new != old, f"tamper did not change {p.name}"
            if crlf:
                new = new.replace("\n", "\r\n")
            p.write_bytes(new.encode("utf-8"))  # line endings as they were
        return setup
    first_orig = lambda w: next((w / "evidence" / "originals").iterdir())
    tampers = {
        "statement body changed": edit(lambda w: st(w, "S-0090"), lambda s: s.replace("Go.", "Rust.")),
        "attestation by another user": edit(lambda w: st(w, "S-0090"), lambda s: s.replace("by: jamie", "by: someone")),
        "duplicate attestation": edit(lambda w: st(w, "S-0090"), lambda s: s.replace("attestations:\n", "attestations:\n  - by: jamie\n    turn: x\n    form: self\n", 1)),
        "unknown attestation form, no stamp": edit(lambda w: st(w, "S-0089"), lambda s: s.replace("form: proposed-and-agreed", "form: shouted")),
        "malformed system hash": edit(lambda w: st(w, "S-0089"), lambda s: re.sub(r'system_hash: "sha256:[0-9a-f]+"', 'system_hash: "sha1:abc"', s)),
        "supersession not reciprocal": edit(lambda w: next(p for p in (w / "attested").glob("*.md") if "superseded_by:" in p.read_text(encoding="utf-8")),
                                            lambda s: re.sub(r"(?m)^superseded_by: .*\n", "", s)),
        "statement id missing": edit(lambda w: st(w, "S-0002"), lambda s: re.sub(r"(?m)^id: .*\n", "", s)),
        "statement id mismatches filename": edit(lambda w: st(w, "S-0003"), lambda s: s.replace("id: S-0003", "id: S-0103")),
        "indented frontmatter line": edit(lambda w: st(w, "S-0004"), lambda s: s.replace("\ntitle:", "\n title:", 1)),
        "bad pointer in cites": edit(lambda w: st(w, "S-0090"), lambda s: s.replace('cites:\n', 'cites:\n  - "not a pointer"\n  - "deadbeef#12345678"\n', 1)),
        "no answers line on a 0.7.0 statement": edit(lambda w: st(w, "S-0090"), lambda s: re.sub(r"(?m)^answers: .*\n", "", s)),
        "agent statement seen turn wrong": edit(lambda w: ag(w, "A-0005"), lambda s: s.replace("seen:\n  - turn: \"0bcc63e5-4cfe-4415-b37a-406f627cf6ad#1e00230a", "seen:\n  - turn: \"0bcc63e5-4cfe-4415-b37a-406f627cf6ad#9a658507")),
        "agent statement relates to a human one": edit(lambda w: ag(w, "A-0005"), lambda s: s.replace("topics: []", "topics: []\namends: S-0001")),
        "agent statement with attestations and bad stamp": edit(lambda w: ag(w, "A-0004"), lambda s: s.replace("topics: []", "topics: []\nattestations: [x]").replace("system_version:", "x_version:")),
        "rests_on a missing statement": edit(lambda w: ag(w, "A-0005"), lambda s: s.replace("  - S-0072", "  - S-9999")),
        "agent rests on a superseded statement": edit(lambda w: ag(w, "A-0005"), lambda s: s.replace("  - S-0072", "  - " + G.superseded_id)),
        "original bytes modified": lambda w: first_orig(w).write_bytes(first_orig(w).read_bytes() + b"x"),
        "original missing": lambda w: first_orig(w).unlink(),
        "unreferenced original": lambda w: (w / "evidence" / "originals" / ("0" * 64 + ".txt")).write_bytes(b"x"),
        "statement deleted (id gap, dangling references)": lambda w: st(w, "S-0005").unlink(),
        "session header mode disagrees with its folder": edit(lambda w: next((w / "evidence").glob("*-8340bb5d.jsonl")), lambda s: s.replace('"mode": "on-record"', '"mode": "off-record"', 1)),
        "document chunk role changed": edit(lambda w: next((w / "evidence").glob("*-doc-*.jsonl")), lambda s: s.replace('"role": "document"', '"role": "user"', 1)),
        "statement with CRLF and BOM": lambda w: st(w, "S-0006").write_bytes(b"\xef\xbb\xbf" + st(w, "S-0006").read_bytes()),
    }
    for name, setup in tampers.items():
        pair(S, f"kb check: {name}", [["check"]], setup=setup)


def sec_index_cross():
    S = "index"
    dbs = {}
    for impl in ("py", "go"):
        w = workspace("convkb")
        o = run(impl, ["index"], w)
        if o.rc != 0:
            record(S, f"kb index ({impl})", "diff", o.stderr[:500])
        dst = G.work / f"index-{impl}.db"
        shutil.copyfile(w / "index.db", dst)
        dbs[impl] = dst
    tables = ["sessions", "turns", "statements", "attestations", "citations", "turns_fts", "statements_fts"]
    def dump(p):
        con = sqlite3.connect(p)
        out = {t: list(map(repr, con.execute(f"SELECT rowid, * FROM {t} ORDER BY rowid"))) for t in tables}
        out["sqlite_master"] = list(map(repr, con.execute("SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY name")))
        con.close()
        return out
    a, b = dump(dbs["py"]), dump(dbs["go"])
    bad = [t for t in a if a[t] != b[t]]
    record(S, "index.db contents (every table, sqlite3)", "diff" if bad else "same",
           f"tables differ: {bad}" if bad else f"{sum(len(v) for v in a.values())} rows identical")
    queries = [("attest",), ("kent", "rule"), ("python", "OR", "go"), ('"S-0090"',), ('"agent', 'statement"'), ("superses*",)]
    iq = G.work / ("indexquery.exe" if os.name == "nt" else "indexquery")
    subprocess.run(["go", "build", "-o", str(iq), "./parity/indexquery"], cwd=REPO, check=True)
    def py_query(db, q, limit=10):
        con = sqlite3.connect(db)
        res = {
            "statements": [list(r) for r in con.execute(
                "SELECT s.id, s.title, snippet(statements_fts, 2, '[', ']', '…', 12) AS snip FROM statements_fts JOIN statements s "
                "ON s.id = statements_fts.id WHERE statements_fts MATCH ? ORDER BY rank LIMIT ?", (q, limit))],
            "turns": [list(r) for r in con.execute(
                "SELECT t.session, t.uuid, t.label, t.author, t.locator, snippet(turns_fts, 1, '[', ']', '…', 12) AS snip FROM turns_fts "
                "JOIN turns t ON t.uuid = turns_fts.uuid WHERE turns_fts MATCH ? ORDER BY rank LIMIT ?", (q, limit))]}
        con.close()
        return {k: v for k, v in res.items() if v}
    def go_query(db, q, limit=10):
        p = subprocess.run([str(iq), str(db), str(limit), q], capture_output=True)
        return {k: v for k, v in json.loads(p.stdout or b"{}").items() if v}
    for q in queries:
        qs = " ".join(q)
        r = {(rd, built): (py_query if rd == "py" else go_query)(dbs[built], qs) for rd in ("py", "go") for built in ("py", "go")}
        vals = list(r.values())
        ok = all(v == vals[0] for v in vals)
        record(S, f"search {qs!r}: Python and Go readers on Python- and Go-built indexes", "same" if ok else "diff",
               f"{sum(len(x) for x in vals[0].values())} rows, all four combinations equal" if ok else
               "\n".join(f"{k}: {json.dumps(v, ensure_ascii=False)[:300]}" for k, v in r.items()))


def sec_serve():
    S = "serve"
    procs = {}
    ports = {}
    roots = {}
    for impl in ("py", "go"):
        w = G.work / f"serve-{impl}"
        if w.exists():
            shutil.rmtree(w)
        shutil.copytree(G.bases["convkb"], w)
        roots[impl] = w
        s = socket.socket(); s.bind(("127.0.0.1", 0)); ports[impl] = s.getsockname()[1]; s.close()
        procs[impl] = subprocess.Popen((PY_KB if impl == "py" else [str(G.go)]) + ["serve", "--port", str(ports[impl])],
                                       cwd=w, env=base_env(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        for impl in ("py", "go"):
            for _ in range(200):
                try:
                    socket.create_connection(("127.0.0.1", ports[impl]), timeout=0.2).close()
                    break
                except OSError:
                    time.sleep(0.1)
        stmts = sorted(p.name.split("-")[0] + "-" + p.name.split("-")[1] for p in (G.bases["convkb"] / "attested").glob("S-*.md"))
        agents = sorted("A-" + p.name.split("-")[1] for p in (G.bases["convkb"] / "agent").glob("A-*.md"))
        orig = next((G.bases["convkb"] / "evidence" / "originals").iterdir()).name
        # Ordinary URLs only: Go parses URLs the normal Go way (net/http), and odd forms such as a leading
        # "//", ";params" or malformed %-escapes are not meant to behave as Python's http.server did.
        urls = ["/", "/cards", "/e/", "/u/jamie", "/u/agent:claude-opus-5-5", "/search?q=attest", "/search?q=kent+rule",
                "/search?q=a.b", "/search?q=", "/search", "/search?q=%22agent%20statement%22&x=1", "/search?q=%E2%9C%93",
                "/o/" + orig, "/o/bad", "/o/" + "0" * 64 + ".md", "/nope", "/e/zzzz", "/s/S-9999", "/s/S-0001%20x",
                "/e/" + G.session_ids[0][0][:4], "/e/?x=1"]
        urls += [f"/e/{s[0]}" for s in G.session_ids] + [f"/e/{h['document_id']}" for _, h, _ in G.documents]
        urls += [f"/s/{x}" for x in stmts + agents]

        def get(impl, u):
            c = http.client.HTTPConnection("127.0.0.1", ports[impl], timeout=60)
            c.request("GET", u)
            r = c.getresponse()
            out = (r.status, r.getheader("Content-Type"), r.getheader("Location"), r.read())
            c.close()
            return out

        same = 0
        for u in urls:
            a, b = get("py", u), get("go", u)
            if a == b:
                same += 1
            else:
                record(S, f"GET {u}", "diff", f"status {a[0]}/{b[0]}, type {a[1]}/{b[1]}\n" + first_diff(
                    a[3].decode("utf-8", "backslashreplace").replace("><", ">\n<"),
                    b[3].decode("utf-8", "backslashreplace").replace("><", ">\n<"), "body"))
        record(S, f"GET {len(urls)} pages (status, content type, body)", "same" if same == len(urls) else "diff",
               f"{same}/{len(urls)} identical")
        a, b = get("py", "/reindex"), get("go", "/reindex")
        record(S, "GET /reindex (redirect to /: status and Location)", "same" if (a[0], a[2]) == (b[0], b[2]) else "diff",
               f"py {a[0]} {a[2]}, go {b[0]} {b[2]}")
    finally:
        for p in procs.values():
            p.kill()
            p.wait()
    record(S, "HTTP framing (not compared)", "expected", "Python's http.server answers HTTP/1.0 (501 for other methods, its own "
           "error pages, odd URL forms); Go's net/http answers HTTP/1.1 (405 for other methods, Go's URL parsing). The pages "
           "themselves are compared above.")


def sec_acts():
    S = "acts"
    # facts from the record: S-0090's proposal and agreement, A-0005's turn
    s90 = meta_of(next((G.bases["convkb"] / "attested").glob("S-0090-*.md")))
    a5 = meta_of(next((G.bases["convkb"] / "agent").glob("A-0005-*.md")))
    body90 = body_of(next((G.bases["convkb"] / "attested").glob("S-0090-*.md")))
    body_a5 = body_of(next((G.bases["convkb"] / "agent").glob("A-0005-*.md")))
    prop, agree = s90["proposed_in"], s90["attestations"][0]["turn"]
    made = a5["made_in"]
    sess = prop.split("#")[0]
    turns = next(t for p, h, t in G.sessions if h["session_id"] == sess)
    human_turns = [t for t in turns if t["role"] == "user" and t["author"] == "jamie"]
    early_human = f"{sess}#{human_turns[0]['uuid']}"
    long_human = max(human_turns, key=lambda t: len(" ".join(b.get("text", "") for b in t["content"] if b["type"] == "text")))
    long_text = " ".join(b.get("text", "") for b in long_human["content"] if b["type"] == "text")
    self_text = " ".join(long_text.split()[:12])
    other_sess = next(h["session_id"] for p, h, t in G.sessions if h["session_id"] != sess and h["mode"] == "on-record")
    other_agent = next(t for p, h, ts in G.sessions if h["session_id"] == other_sess for t in ts if t["role"] == "assistant")
    off = next((h["session_id"], ts) for p, h, ts in G.sessions if h["mode"] == "off-record")
    off_human = next(t for t in off[1] if t["role"] == "user" and t["author"] not in ("client", "tool"))
    doc_id, chunk = G.documents[0][1]["document_id"], G.documents[0][2][0]["uuid"]
    M = lambda **kw: "\n".join(f"{k}: {v}" for k, v in kw.items())
    good = M(title='"Recorder in Go (parity copy)"', answers='"what the recorder is written in; a test copy"')

    def d(name, meta=good, body=body90):
        return lambda w: draft(w, name, meta, body)

    A = lambda *a: ["attest", *a]
    cases = [
        ("proposed-and-agreed", d("d.md"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("--keep-draft", d("d.md"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop, "--keep-draft")]),
        ("agreed-with-modification (text contained: warns)", d("d.md"), [A("off-record/drafts/d.md", "--form", "agreed-with-modification", "--turn", agree, "--proposed", prop)]),
        ("self", d("d.md", body=self_text), [A("off-record/drafts/d.md", "--form", "self", "--turn", f"{sess}#{long_human['uuid']}")]),
        ("supersedes, amends, clarifies, rests_on, cites, topics",
         d("d.md", meta=good + "\nsupersedes: S-0089\namends: S-0088\nclarifies: S-0087\nrests_on: [S-0084, A-0005]\ncites:\n  - \""
           + prop + "\"\n  - \"" + doc_id[:8] + "#" + chunk + "\"\n  - \"deadbeef#12345678\"\ntopics: [go, release]"),
         [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("supersedes an agent statement (adopting it)", d("d.md", meta=good + "\nsupersedes: A-0005"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: --turn authored by the agent", d("d.md"), [A("off-record/drafts/d.md", "--turn", prop, "--proposed", prop)]),
        ("refused: --turn authored by another user", d("d.md"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop, "--user", "someone")]),
        ("refused: text not in the proposal", d("d.md", body="Text the agent never proposed."), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: agreement before the proposal", d("d.md"), [A("off-record/drafts/d.md", "--turn", early_human, "--proposed", prop)]),
        ("refused: proposal in another session", d("d.md"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", f"{other_sess}#{other_agent['uuid']}")]),
        ("refused: re-attestation (endorsing what I attested)", None, [A("--form", "endorsement", "--statement", "S-0090", "--turn", agree)]),
        ("refused: endorsement from a turn the endorser did not write", None, [A("--form", "endorsement", "--statement", "S-0090", "--turn", agree, "--user", "pat")]),
        ("endorsement by a second person", None, [pat_snapshot(), A("--form", "endorsement", "--statement", "S-0090", "--turn", PAT_TURN, "--user", "pat")]),
        ("refused: endorsing twice", None, [pat_snapshot(), A("--form", "endorsement", "--statement", "S-0090", "--turn", PAT_TURN, "--user", "pat"),
                                            A("--form", "endorsement", "--statement", "S-0090", "--turn", PAT_TURN, "--user", "pat")]),
        ("refused: endorsement without --statement", None, [A("--form", "endorsement", "--turn", agree)]),
        ("refused: endorsing an agent statement", None, [A("--form", "endorsement", "--statement", "A-0005", "--turn", agree)]),
        ("refused: endorsing a missing statement", None, [A("--form", "endorsement", "--statement", "S-9999", "--turn", agree)]),
        ("refused: missing answers line", d("d.md", meta='title: "No answers"'), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: blank answers line", d("d.md", meta='title: "Blank"\nanswers: "  "'), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: missing title", d("d.md", meta='answers: "x"'), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: draft carries an id", d("d.md", meta=good + "\nid: S-0999"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: empty body", d("d.md", body="   "), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: proposed-and-agreed without --proposed", d("d.md"), [A("off-record/drafts/d.md", "--turn", agree)]),
        ("refused: no draft", None, [A("--turn", agree, "--proposed", prop)]),
        ("refused: bad form", d("d.md"), [A("off-record/drafts/d.md", "--form", "shouted", "--turn", agree)]),
        ("refused: bad pointer", d("d.md"), [A("off-record/drafts/d.md", "--turn", "S-0001", "--proposed", prop)]),
        ("refused: unknown session", d("d.md"), [A("off-record/drafts/d.md", "--turn", "deadbeef#12345678", "--proposed", prop)]),
        ("refused: unknown turn", d("d.md"), [A("off-record/drafts/d.md", "--turn", sess[:8] + "#ffffffff", "--proposed", prop)]),
        ("refused: off-record turn", d("d.md"), [A("off-record/drafts/d.md", "--turn", f"{off[0]}#{off_human['uuid']}", "--proposed", prop)]),
        ("refused: document chunk as the turn", d("d.md"), [A("off-record/drafts/d.md", "--turn", f"{doc_id[:8]}#{chunk}", "--proposed", prop)]),
        ("refused: self text not in the turn", d("d.md"), [A("off-record/drafts/d.md", "--form", "self", "--turn", agree)]),
        ("refused: supersedes a missing statement", d("d.md", meta=good + "\nsupersedes: S-9999"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: amends an agent statement", d("d.md", meta=good + "\namends: A-0005"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: rests_on a missing statement", d("d.md", meta=good + "\nrests_on: [S-0001, X-1]"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("refused: rests_on written as a scalar", d("d.md", meta=good + "\nrests_on: S-0001"), [A("off-record/drafts/d.md", "--turn", agree, "--proposed", prop)]),
        ("crash: draft file missing", None, [A("off-record/drafts/missing.md", "--turn", agree, "--proposed", prop)]),
        ("refused: snapshot mode change", None, [["snapshot", sess, "--mode", "off-record"]]),
    ]
    R = lambda *a: ["record", *a]
    good_a = M(title='"Agent choice (parity copy)"', answers='"how kb finds the record; a test copy"')
    cases += [
        ("record (seen in the turn it was made)", d("a.md", meta=good_a, body=body_a5), [R("off-record/drafts/a.md", "--turn", made)]),
        ("record (not yet seen)", d("a.md", meta=good_a, body="A choice not shown in the turn."), [R("off-record/drafts/a.md", "--turn", made)]),
        ("record superseding an agent statement, then seen, then withdraw",
         d("a.md", meta=good_a + "\nsupersedes: A-0005\nrests_on: [S-0084]\ncites:\n  - \"" + prop + "\"", body=body_a5),
         [R("off-record/drafts/a.md", "--turn", made), ["withdraw", "A-0006", "--turn", agree, "--reason", "  replaced in a test  "]]),
        ("refused: record from a human turn", d("a.md", meta=good_a), [R("off-record/drafts/a.md", "--turn", agree)]),
        ("refused: record from a document", d("a.md", meta=good_a), [R("off-record/drafts/a.md", "--turn", f"{doc_id[:8]}#{chunk}")]),
        ("refused: record from an off-record session", d("a.md", meta=good_a), [R("off-record/drafts/a.md", "--turn", f"{off[0]}#{off_human['uuid']}")]),
        ("refused: record revising a human statement", d("a.md", meta=good_a + "\nsupersedes: S-0001"), [R("off-record/drafts/a.md", "--turn", made)]),
        ("refused: record revising a missing agent statement", d("a.md", meta=good_a + "\namends: A-9999"), [R("off-record/drafts/a.md", "--turn", made)]),
        ("refused: record revising a withdrawn one",
         lambda w: (d("a.md", meta=good_a + "\nclarifies: A-0004")(w), withdraw_a4(w)), [R("off-record/drafts/a.md", "--turn", made)]),
        ("refused: record without answers", d("a.md", meta='title: "x"'), [R("off-record/drafts/a.md", "--turn", made)]),
        ("seen in another conversation's turn", None, [pat_snapshot(), ["seen", "A-0005", "--turn", PAT_AGENT_TURN]]),
        ("refused: seen, text not shown", None, [["seen", "A-0005", "--turn", agree]]),
        ("refused: seen, already recorded there", None, [["seen", "A-0005", "--turn", made]]),
        ("refused: seen of a human statement id", None, [["seen", "S-0001", "--turn", made]]),
        ("refused: seen of a missing agent statement", None, [["seen", "A-0999", "--turn", made]]),
        ("withdraw", None, [["withdraw", "A-0005", "--turn", agree, "--reason", "test"]]),
        ("refused: withdraw twice", None, [["withdraw", "A-0005", "--turn", agree, "--reason", "test"], ["withdraw", "A-0005", "--turn", agree, "--reason", "again"]]),
        ("refused: withdraw with a blank reason", None, [["withdraw", "A-0005", "--turn", agree, "--reason", "   "]]),
        ("refused: withdraw from an off-record turn", None, [["withdraw", "A-0005", "--turn", f"{off[0]}#{off_human['uuid']}", "--reason", "x"]]),
    ]
    for name, setup, steps in cases:
        pair(S, name, steps, setup=setup, env=pat_env, expect=1 if name.startswith(("refused", "crash")) else 0)
    pair(S, "absolute draft path", [lambda w: A(str(w / "off-record" / "drafts" / "d.md"), "--turn", agree, "--proposed", prop)],
         setup=d("d.md"), known="deliberate: Python's attest crashes on an absolute draft path (load_statement is given a "
         "str and calls .read_text on it); Go attests the draft")
    pair(S, "absolute draft path (record)", [lambda w: R(str(w / "off-record" / "drafts" / "a.md"), "--turn", made)],
         setup=d("a.md", meta=good_a, body=body_a5), known="deliberate: Python's record crashes the same way; Go records the draft")
    G.acts = (prop, agree, made, body90, body_a5, good, good_a)


PAT_SESSION = "9a7a7a7a-0000-4000-8000-000000000001"
PAT_TURN = "9a7a7a7a#9a7a7a7a-0000-4000-8000-0000000000a1"
PAT_AGENT_TURN = "9a7a7a7a#9a7a7a7a-0000-4000-8000-0000000000b2"


def pat_projects() -> Path:
    """A crafted on-record conversation in which a second person, pat, says something."""
    d = G.work / "pat_projects" / "proj"
    if not d.exists():
        d.mkdir(parents=True)
        body_a5 = body_of(next((G.bases["convkb"] / "agent").glob("A-0005-*.md")))
        recs = [{"type": "user", "uuid": PAT_TURN.split("#")[1], "parentUuid": None, "sessionId": PAT_SESSION,
                 "timestamp": "2026-10-08T09:00:00.000Z", "origin": {"kind": "human"},
                 "message": {"role": "user", "content": "I agree with S-0090 too."}},
                {"type": "assistant", "uuid": PAT_AGENT_TURN.split("#")[1], "parentUuid": PAT_TURN.split("#")[1],
                 "sessionId": PAT_SESSION, "timestamp": "2026-10-08T09:00:05.000Z",
                 "message": {"model": "claude-x", "content": [{"type": "text", "text": "For the record, the working choice reads: " + body_a5}]}}]
        (d / f"{PAT_SESSION}.jsonl").write_bytes("".join(json.dumps(r) + "\n" for r in recs).encode("utf-8"))
    return d.parent


def pat_env(w):
    return base_env({"CLAUDE_PROJECTS_DIR": str(pat_projects())})


def pat_snapshot():
    return ["snapshot", PAT_SESSION, "--user", "pat"]


def withdraw_a4(w):
    p = next((w / "agent").glob("A-0004-*.md"))
    old = p.read_bytes().decode("utf-8")
    nl = "\r\n" if "\r\n" in old else "\n"
    new = old.replace("topics: []", "topics: []" + nl + 'withdrawn_in: "x#y"', 1)
    assert new != old
    p.write_bytes(new.encode("utf-8"))


def sec_cross():
    """Acts made by one version, checked by the other: attest, endorse, supersede, record, seen, withdraw."""
    S = "cross"
    prop, agree, made, body90, body_a5, good, good_a = G.acts
    steps = [
        ["attest", "off-record/drafts/s1.md", "--turn", agree, "--proposed", prop],
        pat_snapshot(),
        ["attest", "--form", "endorsement", "--statement", "S-0091", "--turn", PAT_TURN, "--user", "pat"],
        ["attest", "off-record/drafts/s2.md", "--turn", agree, "--proposed", prop],
        ["record", "off-record/drafts/a1.md", "--turn", made],
        ["seen", "A-0006", "--turn", PAT_AGENT_TURN],
        ["record", "off-record/drafts/a2.md", "--turn", made],
        ["withdraw", "A-0007", "--turn", agree, "--reason", "superseded in the test, then withdrawn"],
    ]
    def setup(w):
        draft(w, "s1.md", good + "\nrests_on: [S-0090]\ntopics: [parity]", body90)
        draft(w, "s2.md", good.replace("parity copy", "parity copy 2") + "\nsupersedes: S-0091\nclarifies: S-0090", body90)
        draft(w, "a1.md", good_a, body_a5)
        draft(w, "a2.md", good_a + "\nsupersedes: A-0006", "Not shown: the working choice, revised.")
    # each version's acts, side by side (files compared after timestamps and stamps are normalised)
    res = pair(S, "the same acts by each version", steps, setup=setup, env=pat_env, expect=0)
    # then each version checks the other's result
    for maker, checker in (("go", "py"), ("py", "go")):
        w = workspace("convkb")
        setup(w)
        failed = None
        for st in steps:
            o = run(maker, st, w, pat_env(w))
            if o.rc != 0:
                failed = f"{maker} act {' '.join(st[:2])} failed: {o.stderr.strip()[:300]}"
                break
        if failed:
            record(S, f"{checker} kb check accepts the acts {maker} made", "diff", failed)
            continue
        c = run(checker, ["check"], w)
        own = run(maker, ["check"], w)
        ok = c.rc == 0 and c.stdout == own.stdout and "0 error(s)" in c.stdout.splitlines()[-1]
        record(S, f"{checker} kb check accepts the acts {maker} made", "same" if ok else "diff",
               c.stdout.splitlines()[-1] + (f"\n{c.stdout}\n{c.stderr}" if not ok else ""))


# ---------------------------------------------------------------- main

SECTIONS = {"cli": sec_cli, "location": sec_location, "snapshot": sec_snapshot, "ingest": sec_ingest,
            "read": sec_check_show_search, "index": sec_index_cross, "serve": sec_serve, "acts": sec_acts, "cross": sec_cross}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--kb", default=next((str(d / "convkb") for d in REPO.parents if (d / "convkb" / "attested").is_dir()), None),
                    help="knowledge base to copy (read only; default: a convkb folder beside the repository)")
    ap.add_argument("--go", help="Go kb executable (default: build one)")
    ap.add_argument("--work", help="work folder (default: a new short temp folder)")
    ap.add_argument("--report", default=str(REPO / "parity" / "last-run.md"))
    ap.add_argument("--only", action="append", help="run only these sections: " + ", ".join(SECTIONS))
    a = ap.parse_args()
    G.real_kb = Path(a.kb).resolve()
    G.work = Path(a.work) if a.work else Path(tempfile.mkdtemp(prefix="kp", dir=os.environ.get("TEMP") or None))
    G.work.mkdir(parents=True, exist_ok=True)
    print(f"work folder: {G.work}")
    if a.go:
        G.go = Path(a.go).resolve()
    else:
        G.go = G.work / ("kb.exe" if os.name == "nt" else "kb")
        subprocess.run(["go", "build", "-o", str(G.go), "./cmd/kb"], cwd=REPO, check=True)
    # bases: a copy of the real knowledge base (read only, once) and an empty one
    base = G.work / "base-convkb"
    if not base.exists():
        shutil.copytree(G.real_kb, base, ignore=shutil.ignore_patterns(".git", "index.db", "index.db.tmp"))
    empty = G.work / "base-empty"
    (empty / "attested").mkdir(parents=True, exist_ok=True)
    (empty / "evidence").mkdir(parents=True, exist_ok=True)
    G.bases = {"convkb": base, "empty": empty}
    G.base_hashes = {k: file_hashes(v) for k, v in G.bases.items()}
    G.sessions = sessions_of(base)
    G.documents = documents_of(base)
    G.session_ids = [(h["session_id"], h["mode"], h["filter"], (h.get("participants") or ["jamie"])[0]) for _, h, _ in G.sessions]
    G.resnap_info = {}
    G.superseded_id = next(meta_of(p)["id"] for p in sorted((base / "attested").glob("*.md")) if meta_of(p).get("superseded_by"))
    # freeze the session sources
    G.projects = G.work / "projects"
    if not G.projects.exists():
        real = Path(os.environ.get("CLAUDE_PROJECTS_DIR", Path.home() / ".claude" / "projects"))
        for sid, *_ in G.session_ids:
            for src in real.glob(f"*/{sid}*.jsonl"):
                dst = G.projects / src.parent.name / src.name
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(src, dst)
    t0 = time.time()
    for name, fn in SECTIONS.items():
        if a.only and name not in a.only:
            continue
        print(f"\n[{name}] ", end="", flush=True)
        fn()
    print(f"\n\ndone in {time.time() - t0:.0f}s")
    write_report(Path(a.report), a)
    n = {s: sum(1 for r in RESULTS if r["status"] == s) for s in ("same", "expected", "diff")}
    print(f"{n['same']} identical, {n['expected']} with expected differences, {n['diff']} differing -> {a.report}")
    return 1 if n["diff"] else 0


def run_facts() -> list[str]:
    def out(cmd, cwd=REPO):
        try:
            return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=60).stdout.strip()
        except OSError:
            return "?"
    try:
        import pypdf
        pypdf_v = pypdf.__version__
    except ImportError:
        pypdf_v = "not installed (pdf-v1 falls back to pdftotext)"
    go_ver = out([str(G.go), "version"]).splitlines()
    return [
        f"- commit: {out(['git', 'rev-parse', '--short', 'HEAD'])}" + (" (with uncommitted changes)" if out(["git", "status", "--porcelain"]) else ""),
        f"- OS: {platform.platform()}; Python {sys.version.split()[0]}; pypdf {pypdf_v}; Python's SQLite {sqlite3.sqlite_version}",
        f"- {out(['go', 'version'])}; Go kb: {', '.join(go_ver)}",
        f"- knowledge base copied from `{G.real_kb.name}`: {len(G.session_ids)} sessions, {len(G.documents)} document(s)",
    ]


def write_report(path: Path, a):
    lines = ["# Parity run", ""] + run_facts() + [""]
    by = {}
    for r in RESULTS:
        by.setdefault(r["section"], []).append(r)
    for sec, rs in by.items():
        n = {s: sum(1 for r in rs if r["status"] == s) for s in ("same", "expected", "diff")}
        lines += [f"## {sec}: {n['same']} identical, {n['expected']} expected differences, {n['diff']} differing", ""]
        for r in rs:
            mark = {"same": "same", "expected": "EXPECTED", "diff": "**DIFF**"}[r["status"]]
            detail = r["detail"].replace("\n", " / ")[:400] if r["status"] != "same" else r["detail"]
            lines.append(f"- {mark}: {r['case']} — {detail}")
        lines.append("")
    if G.resnap_info:
        lines += ["## re-snapshot vs the snapshot in the record", ""]
        for sid, info in G.resnap_info.items():
            lines.append(f"- {sid}: py {info.get('py')}; go {info.get('go')}")
        lines.append("")
    path.write_text("\n".join(lines), encoding="utf-8")


if __name__ == "__main__":
    sys.exit(main())
