"""kb show — render one or more evidence turns (or a whole session) as text."""
from __future__ import annotations

from .common import Pointer, die, find_session, load_all_sessions, repo_root, resolve_pointer, turn_text


def label_map(session) -> dict[str, str]:
    """Friendly labels (J1, C1, X1) derived at render time from author; never stored."""
    counts: dict[str, int] = {}
    labels = {}
    for t in session.turns:
        if t["role"] == "assistant":
            prefix = "C"
        elif t["role"] == "document":
            prefix = "D"
        elif t["author"] in ("client", "tool"):
            prefix = "X"
        else:
            prefix = t["author"][:1].upper() or "U"
        counts[prefix] = counts.get(prefix, 0) + 1
        labels[t["uuid"]] = f"{prefix}{counts[prefix]}"
    return labels


def format_turn(session, turn: dict, labels: dict[str, str], width: int = 100) -> str:
    where = turn.get("timestamp", "")
    if turn.get("locator"):
        where = ", ".join(f"{k} {v[0]}-{v[1]}" if isinstance(v, list) else f"{k} {v}"
                          for k, v in turn["locator"].items())
    head = f"[{labels.get(turn['uuid'], '?')}] {turn['author']}  {where}  {session.id[:8]}#{turn['uuid']}"
    lines = [head, "-" * min(len(head), width)]
    for b in turn["content"]:
        t = b["type"]
        if t == "text":
            lines.append(b["text"])
        elif t == "file":
            lines.append(f"  ⟨file: {b.get('media_type')}, {b.get('size')} bytes; see the original⟩")
        elif t == "tool_use":
            d = f" — {b['description']}" if b.get("description") else ""
            lines.append(f"  ⟨tool call: {b['name']}{d}⟩")
        elif t == "tool_result":
            lines.append(f"  ⟨tool result{' omitted' if b.get('omitted') else ''}⟩")
        elif t == "thinking":
            lines.append("  ⟨thinking omitted⟩" if b.get("omitted") else "  ⟨thinking⟩ " + b.get("thinking", ""))
    return "\n".join(lines)


def run(args) -> None:
    root = repo_root()
    if not args.refs:
        for s in load_all_sessions(root).values():
            h = s.header
            if s.kind == "document":
                o = h.get("original", {})
                print(f"{s.id}  {h.get('mode'):9}  {h.get('title') or ''}  (document: {o.get('name')}, "
                      f"{h.get('chunk_count')} chunks, {h.get('extractor')})")
            else:
                print(f"{s.id}  {h.get('mode'):9}  {h.get('title') or ''}  ({h.get('human_turns')} human turns)")
        return
    for ref in args.refs:
        if "#" in ref:
            p = Pointer.parse(ref)
            s, t = resolve_pointer(load_all_sessions(root), p)
            if not s:
                die(f"no evidence session {p.session}")
            if not t:
                die(f"no turn {p.uuid} in session {s.id} or its fork ancestry")
            if not s.id.startswith(p.session):
                print(f"(resolved through fork ancestry: turn is in {s.id[:8]})")
            print(format_turn(s, t, label_map(s)))
        else:
            s = find_session(root, ref)
            if not s:
                die(f"no evidence session {ref}")
            labels = label_map(s)
            if s.kind == "document":
                o = s.header.get("original", {})
                print(f"# {s.header.get('title') or s.id}  mode={s.mode} extractor={s.header.get('extractor')}  "
                      f"original={o.get('name')} ({o.get('hash')}) at {o.get('path')}")
            else:
                print(f"# {s.header.get('title') or s.id}  mode={s.mode} filter={s.header.get('filter')}")
            for t in s.turns:
                if args.human_only and (t["role"] != "user" or t["author"] in ("client", "tool")):
                    continue
                print()
                print(format_turn(s, t, labels))
        print()


def add_parser(sub) -> None:
    p = sub.add_parser("show", help="print evidence turns; no args lists sessions")
    p.add_argument("refs", nargs="*", help="<session>#<uuid> pointers or session ids")
    p.add_argument("--human-only", action="store_true", help="only human/client turns")
    p.set_defaults(func=run)
