"""kb attest — the only write path into attested/.

Forms:
  proposed-and-agreed  the statement text appears verbatim in an agent turn (--proposed), and a later
                       human turn (--turn) by the attesting user agrees.
  agreed-with-modification
                       the human turn (--turn) agrees subject to a stated change; the draft body is the
                       proposal (--proposed) with that change applied. Not mechanically verifiable; the
                       record keeps both turns so a reader can check (S-0006, S-0007).
  self                 the statement text is (a span of) the human's own turn (--turn); it attests itself.
  endorsement          an existing attested statement gains another attestation: --statement S-#### --turn.

Every attestation turn must be in on-record evidence. Off-record evidence may be cited as background only.
Document chunks (kb ingest) are background only: nothing in a document can attest (S-0037).
Every attestation records the system it was made under: system_version and system_hash (S-0030).
A human statement may rest on agent statements (`rests_on`, S-0066) and may supersede one, which is how
a human overrides an agent statement or adopts it as their own (S-0065, S-0066).
A draft must say what question it answers and in what role (frontmatter `answers`), and its text
must carry that context itself, so the statement means the same thing lifted out (S-0043).
"""
from __future__ import annotations

import shutil
from datetime import datetime, timezone

from .common import (ATTESTATION_FORMS, RELATIONS, STATEMENT_ID_RE, Pointer, current_user, die,
                     rule,
                     dump_frontmatter, find_session, load_all_sessions, load_statement, mark_relation,
                     next_statement_id, repo_root, resolve_pointer, slugify, statement_path, system_stamp,
                     text_contained, turn_text, warn)


def _resolve_on_record(root, ptr: Pointer, what: str):
    s = find_session(root, ptr.session)
    if s is None:
        die(f"{what}: no evidence session {ptr.session}")
    if s.mode != "on-record":
        die(f"{what}: session {s.id[:8]} is {s.mode}; attestation evidence must be on-record ({rule('on-record-only')})")
    if s.kind == "document":
        die(f"{what}: {s.id[:8]} is a document; documents cannot attest. A document chunk may be cited as "
            f"background; the attesting turn must be a human's turn in an on-record conversation "
            f"({rule('documents-cannot-attest')})")
    t = s.turn(ptr.uuid)
    if t is None:
        die(f"{what}: no turn {ptr.uuid} in session {s.id[:8]}")
    return s, t


def run(args) -> None:
    root = repo_root()
    user = current_user(args.user)
    form = args.form
    if form not in ATTESTATION_FORMS:
        die(f"form must be one of {ATTESTATION_FORMS}")
    agree_ptr = Pointer.parse(args.turn)
    sess, agree = _resolve_on_record(root, agree_ptr, "--turn")
    if agree["role"] != "user" or agree["author"] != user:
        die(f"--turn {agree_ptr} is authored by {agree['author']!r}, not the attesting user {user!r} ({rule('own-turn')})")

    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    record = {"by": user, "at": now, "turn": str(Pointer(sess.id, agree["uuid"])), "form": form,
              **system_stamp(root)}

    if form == "endorsement":
        if not args.statement:
            die("endorsement requires --statement S-####")
        if str(args.statement).startswith("A-"):
            die(f"{args.statement} is an agent statement, which carries no attestations; to adopt it, attest a "
                f"human statement that supersedes it ({rule('agent-no-attestations')})")
        hits = list((root / "attested").glob(f"{args.statement}-*.md"))
        if len(hits) != 1:
            die(f"statement {args.statement} not found (or ambiguous)")
        st = load_statement(hits[0])
        if any(a.get("by") == user for a in st.meta.get("attestations", [])):
            die(f"{user} has already attested {st.id} ({rule('attest-once')})")
        st.meta.setdefault("attestations", []).append(record)
        hits[0].write_text(dump_frontmatter(st.meta) + "\n" + st.body + "\n", encoding="utf-8")
        print(f"{st.id}: endorsement by {user} recorded")
        return

    if not args.draft:
        die("proposed-and-agreed / self require a draft file")
    draft = load_statement(root / args.draft if not args.draft.startswith(("/", "\\")) and not args.draft[1:2] == ":" else args.draft)
    meta, body = dict(draft.meta), draft.body.strip()
    if not body:
        die("draft has an empty body")
    if meta.get("id"):
        die(f"draft already carries an id; drafts are unnumbered until attested ({rule('no-pending-statements')})")
    if not meta.get("title"):
        die(f"draft needs a title ({rule('title-and-answers')})")
    if not str(meta.get("answers") or "").strip():
        die(f"draft needs an answers line: the question this statement answers and the role it plays in "
            f"answering (an example, a working position, a decision, a belief stated directly) "
            f"({rule('title-and-answers')})")

    if form in ("proposed-and-agreed", "agreed-with-modification"):
        if not args.proposed:
            die(f"{form} requires --proposed <session>#<uuid> (the agent turn containing the proposal)")
        prop_ptr = Pointer.parse(args.proposed)
        psess, prop = _resolve_on_record(root, prop_ptr, "--proposed")
        if psess.id != sess.id:
            die(f"proposal and agreement must be in the same session ({rule('same-session')})")
        contained = text_contained(body, turn_text(prop))
        if form == "proposed-and-agreed" and not contained:
            die(f"statement text is not contained verbatim in the proposal turn (whitespace-insensitive); "
                f"use --form agreed-with-modification if the human agreed subject to a change "
                f"({rule('exact-text')})")
        if form == "agreed-with-modification" and contained:
            warn("text is contained verbatim in the proposal; proposed-and-agreed would be the stronger form")
        if prop.get("timestamp", "") >= agree.get("timestamp", ""):
            die(f"agreement turn must come after the proposal turn ({rule('after-proposal')})")
        meta["originator"] = prop["author"]
        meta["proposed_in"] = str(Pointer(psess.id, prop["uuid"]))
        if form == "agreed-with-modification":
            meta["modified_in"] = str(Pointer(sess.id, agree["uuid"]))
    else:  # self
        if not text_contained(body, turn_text(agree)):
            die(f"self-attesting statement text must be contained verbatim in the human's turn ({rule('exact-text')})")
        meta["originator"] = user
        meta["proposed_in"] = str(Pointer(sess.id, agree["uuid"]))

    # background citations: allowed to point anywhere, incl. through fork ancestry (S-0034); warn if unresolvable
    cites = meta.get("cites", []) or []
    if cites:
        sessions = load_all_sessions(root)
        for c in cites:
            _, t = resolve_pointer(sessions, Pointer.parse(c))
            if t is None:
                warn(f"background citation {c} does not resolve here (off-record or missing); kept as-is")

    # relations to existing statements
    rels = {r: meta.get(r) for r in RELATIONS if meta.get(r)}
    for r, target in rels.items():
        if statement_path(root, target) is None:
            die(f"{r}: {target} does not exist")
        if str(target).startswith("A-") and r != "supersedes":
            die(f"{r}: a human statement relates to an agent statement only by superseding it "
                f"(overriding or adopting it; {rule('agent-no-attestations')})")
    rests_on = [str(x) for x in (meta.get("rests_on") or [])]
    for x in rests_on:
        if not STATEMENT_ID_RE.match(x) or statement_path(root, x) is None:
            die(f"rests_on: {x} is not an existing statement id")

    sid = next_statement_id(root)
    ordered = {"id": sid, "title": meta["title"], "answers": str(meta["answers"]).strip(),
               "originator": meta["originator"], "proposed_in": meta["proposed_in"]}
    if meta.get("modified_in"):
        ordered["modified_in"] = meta["modified_in"]
    ordered["cites"] = meta.get("cites", []) or []
    if rests_on:
        ordered["rests_on"] = rests_on
    ordered.update(rels)
    ordered["topics"] = meta.get("topics", []) or []
    ordered["attestations"] = [record]
    dest = root / "attested" / f"{sid}-{slugify(meta['title'])}.md"
    dest.write_text(dump_frontmatter(ordered) + "\n" + body + "\n", encoding="utf-8")
    if args.keep_draft:
        pass
    else:
        try:
            draft.path.unlink()
        except OSError:
            pass
    print(f"attested {sid} ({form}) by {user}: {dest.relative_to(root)}")
    for r, target in rels.items():
        mark_relation(root, target, r, sid)


def add_parser(sub) -> None:
    p = sub.add_parser("attest", help="record an attestation (the only write path into attested/)")
    p.add_argument("draft", nargs="?", help="draft statement file (not for endorsement)")
    p.add_argument("--form", default="proposed-and-agreed", help="|".join(ATTESTATION_FORMS))
    p.add_argument("--turn", required=True, help="<session>#<uuid> of the human's agreeing turn")
    p.add_argument("--proposed", help="<session>#<uuid> of the agent turn containing the exact text")
    p.add_argument("--statement", help="S-#### to endorse (endorsement form)")
    p.add_argument("--user", help="attesting user id")
    p.add_argument("--keep-draft", action="store_true", help="do not delete the draft after attesting")
    p.set_defaults(func=run)
