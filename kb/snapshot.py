"""kb snapshot — copy a Claude Code session JSONL into evidence/ (or off-record/sessions/) with a filter.

Filter profiles are deterministic projections. The header records which profile was applied so a
reader can tell "absent" from "stripped". Records are kept or dropped whole; uuids never change.

human-visible-v1 keeps what the human could see in the client:
  - human-authored user turns (text), minus <system-reminder> blocks
  - client-generated user notices the human saw (e.g. "[Request interrupted by user]")
  - assistant text blocks
  - the fact and name of each tool call, with its human-facing description when present
  - a marker that a tool result / thinking block occurred, content omitted
and drops: tool result content, thinking content, task notifications, attachments, hook/system
records, queue operations, sidechain (subagent) records, usage/signature metadata.

full-v1 keeps user/assistant records with complete content (tool results and thinking included).
"""
from __future__ import annotations

import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

from .common import (FILTER_PROFILES, MODES, SNAPSHOT_VERSION, current_user, die, rule, repo_root,
                     system_stamp, warn)

SYSTEM_REMINDER_RE = re.compile(r"<system-reminder>.*?</system-reminder>", re.S)
CLIENT_NOTICE_RE = re.compile(r"^\[(Request interrupted by user[^\]]*)\]\s*$")


def claude_projects_dir() -> Path:
    return Path(os.environ.get("CLAUDE_PROJECTS_DIR", Path.home() / ".claude" / "projects"))


# Claude Code sets CLAUDE_CODE_SESSION_ID for the commands it runs. It is undocumented, and whether it
# follows /clear is unknown, so it is trusted only when its file looks like the live conversation: the
# most recently written session file in its project, or written within the last minute. The agent's own
# tool call reaches the file before kb runs; a conversation left behind by /clear goes quiet, and the
# conversation that replaced it is newer.
SESSION_ENV = "CLAUDE_CODE_SESSION_ID"
CURRENT_WITHIN_SECONDS = 60


def current_session_source() -> Path:
    sid = os.environ.get(SESSION_ENV)
    if not sid:
        die(f"no session given, and {SESSION_ENV} is not set; pass the session id")
    src = locate_source(sid)
    mtime = src.stat().st_mtime
    age = datetime.now().timestamp() - mtime
    newest = all(p.stat().st_mtime <= mtime for p in src.parent.glob("*.jsonl"))
    if age > CURRENT_WITHIN_SECONDS and not newest:
        die(f"{SESSION_ENV} names session {sid[:8]}, but its file was last written {int(age)}s ago and a newer "
            f"session file exists beside it, so it may not be this conversation (for example after /clear); "
            f"pass the session id")
    return src


def locate_source(session_id: str) -> Path:
    p = Path(session_id)
    if p.suffix == ".jsonl" and p.exists():
        return p
    hits = list(claude_projects_dir().glob(f"*/{session_id}*.jsonl"))
    hits = [h for h in hits if h.parent.name != "memory"]
    if not hits:
        die(f"no session JSONL matching {session_id!r} under {claude_projects_dir()}")
    if len(hits) > 1:
        die("ambiguous session id; matches:\n  " + "\n  ".join(map(str, hits)))
    return hits[0]


def _tool_call_summary(block: dict, profile: str) -> dict:
    inp = block.get("input") or {}
    out = {"type": "tool_use", "id": block.get("id"), "name": block.get("name")}
    if profile == "full-v1":
        out["input"] = inp
        return out
    # Human-facing bits: most tools expose a description; file tools show a path; otherwise a short preview.
    desc = inp.get("description")
    if not desc:
        for k in ("file_path", "pattern", "query", "prompt", "command", "url"):
            if isinstance(inp.get(k), str):
                desc = inp[k][:160]
                break
    if desc:
        out["description"] = desc
    return out


def _filter_user(rec: dict, profile: str) -> dict | None:
    msg = rec.get("message") or {}
    content = msg.get("content")
    origin = (rec.get("origin") or {}).get("kind")
    blocks: list[dict] = []
    is_human = origin == "human"
    if isinstance(content, str):
        text = content
        if profile != "full-v1":
            text = SYSTEM_REMINDER_RE.sub("", text).strip()
        if not text:
            return None
        if not is_human and not CLIENT_NOTICE_RE.match(text) and profile != "full-v1":
            # client-generated text the human did not type and did not see as a turn (task notifications etc.)
            return None
        blocks.append({"type": "text", "text": text})
    elif isinstance(content, list):
        for b in content:
            t = b.get("type")
            if t == "text":
                text = b.get("text", "")
                if profile != "full-v1":
                    text = SYSTEM_REMINDER_RE.sub("", text).strip()
                    if not text:
                        continue
                    if not is_human and not CLIENT_NOTICE_RE.match(text):
                        continue
                blocks.append({"type": "text", "text": text})
            elif t == "tool_result":
                if profile == "full-v1":
                    blocks.append(b)
                else:
                    raw = json.dumps(b.get("content", ""), ensure_ascii=False)
                    blocks.append({"type": "tool_result", "tool_use_id": b.get("tool_use_id"),
                                   "omitted": True, "size": len(raw)})
            elif t == "image" and profile == "full-v1":
                blocks.append(b)
    if not blocks:
        return None
    author = rec.get("_author")
    if not is_human and all(b["type"] == "tool_result" for b in blocks):
        author = "tool"
    elif not is_human:
        author = "client"
    return {
        "type": "turn",
        "uuid": rec["uuid"],
        "parent": rec.get("parentUuid"),
        "timestamp": rec.get("timestamp"),
        "role": "user",
        "author": author,
        "content": blocks,
    }


def _filter_assistant(rec: dict, profile: str) -> dict | None:
    msg = rec.get("message") or {}
    blocks: list[dict] = []
    for b in msg.get("content", []):
        t = b.get("type")
        if t == "text":
            if b.get("text", "").strip():
                blocks.append({"type": "text", "text": b["text"]})
        elif t == "tool_use":
            blocks.append(_tool_call_summary(b, profile))
        elif t == "thinking":
            if profile == "full-v1":
                blocks.append({"type": "thinking", "thinking": b.get("thinking", "")})
            else:
                blocks.append({"type": "thinking", "omitted": True})
    if not blocks:
        return None
    return {
        "type": "turn",
        "uuid": rec["uuid"],
        "parent": rec.get("parentUuid"),
        "timestamp": rec.get("timestamp"),
        "role": "assistant",
        "author": f"agent:{msg.get('model', 'unknown')}",
        "content": blocks,
    }


def project(records: list[dict], profile: str, human_user: str) -> tuple[list[dict], dict]:
    """Apply the filter. Returns (turns, info) where info carries title and counts."""
    turns: list[dict] = []
    info = {"title": None, "dropped": {}, "human_turns": 0, "agent_turns": 0, "client_turns": 0}

    def drop(kind: str) -> None:
        info["dropped"][kind] = info["dropped"].get(kind, 0) + 1

    for rec in records:
        t = rec.get("type")
        if t == "custom-title":
            info["title"] = rec.get("customTitle")
            continue
        if t == "ai-title" and not info["title"]:
            info["title"] = rec.get("aiTitle")
            continue
        if t not in ("user", "assistant"):
            drop(t)
            continue
        if rec.get("isSidechain"):
            drop("sidechain")
            continue
        rec["_author"] = human_user
        out = _filter_user(rec, profile) if t == "user" else _filter_assistant(rec, profile)
        if out is None:
            drop(f"{t}:empty-after-filter")
            continue
        turns.append(out)
        if out["role"] == "assistant":
            info["agent_turns"] += 1
        elif out["author"] in ("client", "tool"):
            info["client_turns"] += 1
        else:
            info["human_turns"] += 1
    return turns, info


def resolve_branch_point(records: list[dict], turns: list[dict]) -> str | None:
    """The parent-session turn this session forked from, or None. The first kept turn's parent may be
    a record of this same session that the filter dropped, such as the output of /clear; follow the
    chain through those, so that only a parent outside this session's records counts as a fork."""
    if not turns:
        return None
    own = {r["uuid"]: r for r in records if r.get("uuid")}
    p = turns[0].get("parent")
    seen = set()
    while p and p in own and p not in seen:
        seen.add(p)
        p = own[p].get("parentUuid")
    return p or None


def read_source(path: Path) -> list[dict]:
    recs = []
    with path.open(encoding="utf-8") as fh:
        for n, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            try:
                recs.append(json.loads(line))
            except json.JSONDecodeError as e:
                die(f"{path}:{n}: bad JSON ({e})")
    return recs


def write_snapshot(dest: Path, header: dict, turns: list[dict]) -> None:
    tmp = dest.with_suffix(".jsonl.tmp")
    with tmp.open("w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps(header, ensure_ascii=False) + "\n")
        for t in turns:
            fh.write(json.dumps(t, ensure_ascii=False) + "\n")
    tmp.replace(dest)


def run(args) -> None:
    root = repo_root()
    user = current_user(args.user)
    if args.mode not in MODES:
        die(f"mode must be one of {MODES}")
    if args.filter not in FILTER_PROFILES:
        die(f"filter must be one of {FILTER_PROFILES}")
    src = locate_source(args.session) if args.session else current_session_source()
    records = read_source(src)
    session_ids = {r.get("sessionId") for r in records if r.get("sessionId")}
    if len(session_ids) == 1:
        session_id = session_ids.pop()
    else:
        # A forked/resumed session file carries the parent session's records under the parent's
        # sessionId. Snapshot only the requested session; the parent is snapshotted from its own file.
        wanted = [s for s in session_ids if s.startswith(args.session or src.stem)]
        if len(wanted) != 1:
            die(f"multiple sessionIds in {src}: {sorted(session_ids)}; "
                f"pass the id of the one to snapshot")
        session_id = wanted[0]
        records = [r for r in records if r.get("sessionId") in (session_id, None)]

    turns, info = project(records, args.filter, user)
    branch_point = resolve_branch_point(records, turns)
    if info["human_turns"] == 0:
        die("no human turns survived the filter; refusing to write an empty snapshot")

    dest_dir = root / "evidence" if args.mode == "on-record" else root / "off-record" / "sessions"
    dest_dir.mkdir(parents=True, exist_ok=True)
    first_ts = next((t["timestamp"] for t in turns if t.get("timestamp")), None)
    date = (first_ts or "")[:10] or datetime.now(timezone.utc).date().isoformat()
    dest = dest_dir / f"{date}-{session_id[:8]}.jsonl"

    # Append-only guard: a re-snapshot must be a superset of what was there.
    if dest.exists():
        prev_uuids = []
        with dest.open(encoding="utf-8") as fh:
            for line in fh:
                rec = json.loads(line)
                if rec.get("type") == "turn":
                    prev_uuids.append(rec["uuid"])
                elif rec.get("type") == "kb-snapshot":
                    if rec.get("filter") != args.filter and not args.force:
                        die(f"{dest} was taken with filter {rec.get('filter')!r}; pass --force to change profile ({rule('mode-fixed')})")
                    if rec.get("mode") != args.mode:
                        die(f"{dest} has mode {rec.get('mode')!r}; a session's mode does not change ({rule('mode-fixed')})")
        new_uuids = {t["uuid"] for t in turns}
        missing = [u for u in prev_uuids if u not in new_uuids]
        if missing and not args.force:
            die(f"re-snapshot would drop {len(missing)} previously recorded turn(s) (e.g. {missing[0]}); refusing ({rule('append-only')})")
    # Same session must not exist in the other mode either.
    other = root / "off-record" / "sessions" if args.mode == "on-record" else root / "evidence"
    if any(session_id[:8] in p.name for p in other.glob("*.jsonl")):
        die(f"session {session_id[:8]} already exists under {other}; a session has one mode ({rule('mode-fixed')})")

    header = {
        "type": "kb-snapshot",
        "version": SNAPSHOT_VERSION,
        "session_id": session_id,
        "title": args.title or info["title"],
        "mode": args.mode,
        "filter": args.filter,
        "participants": [user],
        "agents": sorted({t["author"] for t in turns if t["role"] == "assistant"}),
        "source": str(src),
        "snapshot_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        **system_stamp(root),
        "branch_point": branch_point,
        "turn_count": len(turns),
        "human_turns": info["human_turns"],
        "dropped": info["dropped"],
    }
    write_snapshot(dest, header, turns)
    rel = dest.relative_to(root)
    print(f"wrote {rel}: {len(turns)} turns ({info['human_turns']} human, {info['agent_turns']} agent, "
          f"{info['client_turns']} client), mode={args.mode}, filter={args.filter}")
    # The two pointers kb attest needs: the human's latest turn, and the agent's last text before it.
    def excerpt(t: dict) -> str:
        text = " ".join(" ".join(b.get("text", "") for b in t["content"] if b.get("type") == "text").split())
        return text[:100] + ("..." if len(text) > 100 else "")

    hi = next((i for i in range(len(turns) - 1, -1, -1)
               if turns[i]["role"] == "user" and turns[i]["author"] not in ("client", "tool")), None)
    if hi is not None:
        print(f"last human turn: {session_id[:8]}#{turns[hi]['uuid']}: {excerpt(turns[hi])}")
        ai = next((i for i in range(hi - 1, -1, -1) if turns[i]["role"] == "assistant"
                   and any(b.get("type") == "text" for b in turns[i]["content"])), None)
        if ai is not None:
            print(f"agent text before it: {session_id[:8]}#{turns[ai]['uuid']}: {excerpt(turns[ai])}")
    if args.mode == "off-record":
        print("note: off-record/ is gitignored; this snapshot is at risk of loss by design", file=sys.stderr)


def add_parser(sub) -> None:
    p = sub.add_parser("snapshot", help="copy a session JSONL into evidence with a filter")
    p.add_argument("session", nargs="?",
                   help=f"session id (or prefix), or path to a .jsonl; default: this conversation, from {SESSION_ENV}")
    p.add_argument("--mode", default="on-record", help="on-record (evidence/) or off-record (off-record/sessions/)")
    p.add_argument("--filter", default="human-visible-v1", help="|".join(FILTER_PROFILES))
    p.add_argument("--user", help="human participant id (else KB_USER / ~/.kenttheclerk.json)")
    p.add_argument("--title", help="override the session title")
    p.add_argument("--force", action="store_true", help="allow changing filter profile / dropping turns")
    p.set_defaults(func=run)
