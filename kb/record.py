"""kb record / seen / withdraw — the only write path into agent/.

An agent statement (S-0065) is a working choice: an extrapolation of human intent, tentative but
operational until the humans decide otherwise. It has an author, the agent and its model, and no
attestations. It is numbered A-#### and lives in agent/, beside the human statements in attested/.

  record    mint A-#### from a draft. --turn names the agent's turn, in an on-record conversation, where
            the choice was made; the author is taken from that turn. If the statement text appears in
            that turn it is recorded as seen there. A draft may rest on statements (`rests_on`, S- or A-)
            and may supersede, amend or clarify agent statements; never a human statement, which only
            humans change (S-0065, S-0067).
  seen      record that the full text was shown in an on-record turn (S-0066). Least weight goes to an
            agent statement no human has seen.
  withdraw  retire an agent statement with nothing in its place, citing the on-record turn that says so.

Every act records the system it was made under (S-0030). A draft needs an answers line (S-0043).
"""
from __future__ import annotations

from datetime import datetime, timezone

from .common import (RELATIONS, STATEMENT_ID_RE, Pointer, die, rule, dump_frontmatter, find_session,
                     load_all_sessions, load_statement, mark_relation, next_statement_id, repo_root,
                     resolve_pointer, slugify, statement_path, system_stamp, text_contained, turn_text,
                     warn)


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _on_record_turn(root, ptr_str: str, what: str):
    ptr = Pointer.parse(ptr_str)
    s = find_session(root, ptr.session)
    if s is None:
        die(f"{what}: no evidence session {ptr.session}")
    if s.mode != "on-record":
        die(f"{what}: session {s.id[:8]} is {s.mode}; an agent statement must come from an on-record "
            f"session. Propose going on the record first ({rule('on-record-only')})")
    if s.kind == "document":
        die(f"{what}: {s.id[:8]} is a document; agent statements come from conversations "
            f"({rule('agent-from-conversation')})")
    t = s.turn(ptr.uuid)
    if t is None:
        die(f"{what}: no turn {ptr.uuid} in session {s.id[:8]}")
    return s, t


def _load_agent(root, aid: str):
    if not str(aid).startswith("A-"):
        die(f"{aid} is not an agent statement id (A-####)")
    p = statement_path(root, aid)
    if p is None:
        die(f"agent statement {aid} not found")
    return p, load_statement(p)


def _write(p, st) -> None:
    p.write_text(dump_frontmatter(st.meta) + "\n" + st.body + "\n", encoding="utf-8")


def run_record(args) -> None:
    root = repo_root()
    sess, turn = _on_record_turn(root, args.turn, "--turn")
    if turn["role"] != "assistant" or not str(turn["author"]).startswith("agent:"):
        die(f"--turn {args.turn} is authored by {turn['author']!r}; an agent statement is made in an agent's "
            f"turn (a human's own words go through kb attest) ({rule('agent-own-turn')})")

    draft = load_statement(root / args.draft if not args.draft.startswith(("/", "\\")) and args.draft[1:2] != ":"
                           else args.draft)
    meta, body = dict(draft.meta), draft.body.strip()
    if not body:
        die("draft has an empty body")
    if meta.get("id"):
        die(f"draft already carries an id; drafts are unnumbered until recorded ({rule('no-pending-statements')})")
    if not meta.get("title"):
        die(f"draft needs a title ({rule('title-and-answers')})")
    if not str(meta.get("answers") or "").strip():
        die(f"draft needs an answers line: the question this choice answers and the role it plays "
            f"({rule('title-and-answers')})")

    rels = {r: meta.get(r) for r in RELATIONS if meta.get(r)}
    for r, target in rels.items():
        if not str(target).startswith("A-"):
            die(f"{r}: {target} is a human statement; an agent statement may revise only agent statements. "
                f"Changing what a human said is the human's to do ({rule('agent-revision')})")
        tp = statement_path(root, target)
        if tp is None:
            die(f"{r}: {target} does not exist")
        old = load_statement(tp)
        if old.meta.get("superseded_by") or old.meta.get("withdrawn_in"):
            die(f"{r}: {target} is no longer in force ({rule('agent-revision')})")
    rests_on = [str(x) for x in (meta.get("rests_on") or [])]
    for x in rests_on:
        if not STATEMENT_ID_RE.match(x) or statement_path(root, x) is None:
            die(f"rests_on: {x} is not an existing statement id")

    cites = meta.get("cites", []) or []
    if cites:
        sessions = load_all_sessions(root)
        for c in cites:
            _, t = resolve_pointer(sessions, Pointer.parse(c))
            if t is None:
                warn(f"background citation {c} does not resolve here (off-record or missing); kept as-is")

    now = _now()
    made_in = str(Pointer(sess.id, turn["uuid"]))
    aid = next_statement_id(root, "A")
    ordered = {"id": aid, "title": meta["title"], "answers": str(meta["answers"]).strip(),
               "author": turn["author"], "made_in": made_in, "recorded_at": now, **system_stamp(root)}
    if rests_on:
        ordered["rests_on"] = rests_on
    ordered["cites"] = cites
    ordered.update(rels)
    ordered["topics"] = meta.get("topics", []) or []
    shown = text_contained(body, turn_text(turn))
    ordered["seen"] = [{"turn": made_in, "at": now}] if shown else []
    (root / "agent").mkdir(exist_ok=True)
    dest = root / "agent" / f"{aid}-{slugify(meta['title'])}.md"
    dest.write_text(dump_frontmatter(ordered) + "\n" + body + "\n", encoding="utf-8")
    if not args.keep_draft:
        try:
            draft.path.unlink()
        except OSError:
            pass
    print(f"recorded {aid} by {turn['author']}{' (seen in the turn it was made)' if shown else ' (not yet seen)'}: "
          f"{dest.relative_to(root)}")
    for r, target in rels.items():
        mark_relation(root, target, r, aid)


def run_seen(args) -> None:
    root = repo_root()
    p, st = _load_agent(root, args.statement)
    sess, turn = _on_record_turn(root, args.turn, "--turn")
    if not text_contained(st.body, turn_text(turn)):
        die(f"the full text of {st.id} does not appear in {args.turn}; seen means shown in full ({rule('seen-in-full')})")
    ptr = str(Pointer(sess.id, turn["uuid"]))
    seen = st.meta.get("seen") or []
    if any(x.get("turn") == ptr for x in seen):
        die(f"{st.id} is already recorded as seen in {ptr}")
    seen.append({"turn": ptr, "at": _now()})
    st.meta["seen"] = seen
    _write(p, st)
    print(f"{st.id}: seen in {ptr}")


def run_withdraw(args) -> None:
    root = repo_root()
    p, st = _load_agent(root, args.statement)
    if st.meta.get("withdrawn_in") or st.meta.get("superseded_by"):
        die(f"{st.id} is no longer in force ({rule('agent-revision')})")
    sess, turn = _on_record_turn(root, args.turn, "--turn")
    if not args.reason.strip():
        die(f"--reason is required ({rule('agent-revision')})")
    st.meta["withdrawn_in"] = str(Pointer(sess.id, turn["uuid"]))
    st.meta["withdrawn_by"] = turn["author"]
    st.meta["withdrawn_at"] = _now()
    st.meta["withdrawn_reason"] = args.reason.strip()
    stamp = system_stamp(root)
    st.meta["withdrawn_system_version"] = stamp["system_version"]
    st.meta["withdrawn_system_hash"] = stamp["system_hash"]
    _write(p, st)
    print(f"{st.id}: withdrawn by {turn['author']}")


def add_parser(sub) -> None:
    p = sub.add_parser("record", help="record an agent statement (the only write path into agent/)")
    p.add_argument("draft", help="draft statement file")
    p.add_argument("--turn", required=True, help="<session>#<uuid> of the agent's turn where the choice was made")
    p.add_argument("--keep-draft", action="store_true", help="do not delete the draft after recording")
    p.set_defaults(func=run_record)
    s = sub.add_parser("seen", help="record that an agent statement was shown in full in an on-record turn")
    s.add_argument("statement", help="A-####")
    s.add_argument("--turn", required=True, help="<session>#<uuid> of the turn that showed it")
    s.set_defaults(func=run_seen)
    w = sub.add_parser("withdraw", help="retire an agent statement with nothing in its place")
    w.add_argument("statement", help="A-####")
    w.add_argument("--turn", required=True, help="<session>#<uuid> of the on-record turn that withdraws it")
    w.add_argument("--reason", required=True)
    w.set_defaults(func=run_withdraw)
