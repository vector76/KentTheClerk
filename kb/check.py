"""kb check — mechanical invariants over evidence/ and attested/.

Errors (exit 1):
  duplicate statement ids; missing/invalid frontmatter fields; attestation turn missing, not on-record,
  not authored by the attesting user; proposal turn missing or text not contained; supersession chain
  broken (supersedes a missing statement, superseded_by not reciprocal); session header/dir mode mismatch.
  document projection whose original is missing, misnamed, or whose bytes no longer hash to their
  name (damaged or modified, S-0039); a document chunk used as an attesting or proposal turn (S-0037).
Warnings:
  background citation unresolvable (off-record or missing); statement with no attestations; id gaps;
  a fork whose branch point is in no snapshot; attestations made before system stamping (S-0030);
  an original no projection refers to; statements attested before the answers line was required.
Errors also: a statement attested under system 0.3.0 or later with no answers line (S-0043).

Agent statements (agent/A-####, S-0065): errors for a malformed id or filename, missing title, answers
or body, a made_in turn that is missing, off-record, a document, or not authored by the named agent,
a missing system stamp, a seen turn that is not on-record or does not show the full text, a relation
to a human statement, or a withdrawal whose turn does not resolve. Warnings: an agent statement in
force that rests on a statement since superseded or withdrawn (a candidate for cleanup, S-0067).
Both kinds: rests_on naming a statement that does not exist is an error.

Pointers name a session; a background citation also resolves through that session's fork ancestry
(its parent's turns up to the branch point, recursively; S-0034). Proposal and attestation turns
must be in the named session itself (S-0032).
"""
from __future__ import annotations

import re
from pathlib import Path

from .common import (ATTESTATION_FORMS, ORIGINAL_HASH_RE, ORIGINAL_NAME_RE, SYSTEM_HASH_RE, Pointer,
                     ancestry, evidence_dirs, load_all_agent_statements, load_all_sessions,
                     load_all_statements, originals_dir, repo_root, resolve_pointer, session_by_prefix,
                     rule, sha256_file, text_contained, turn_text, version_tuple)


def run(args) -> int:
    root = repo_root()
    errors: list[str] = []
    warnings: list[str] = []
    sessions = load_all_sessions(root)

    def resolve(ptr_str: str, through_ancestry: bool = False):
        """Same-session resolution by default. Background citations may resolve through the
        fork's ancestry chain (S-0034); attestation and proposal turns may not (S-0032)."""
        try:
            p = Pointer.parse(ptr_str)
        except SystemExit:
            return None, None
        if through_ancestry:
            return resolve_pointer(sessions, p)
        s = session_by_prefix(sessions, p.session)
        return (s, s.turn(p.uuid)) if s else (None, None)

    # fork ancestry: a fork whose branch point is in no snapshot cannot have its history followed
    # (S-0034); the parent may just need re-snapshotting. On-record requires on-record ancestry (S-0035).
    for s in sessions.values():
        bp = s.branch_point
        if not bp:
            continue
        chain = ancestry(sessions, s)
        if not chain:
            warnings.append(f"session {s.id[:8]}: forked from turn {bp[:8]}, which is in no snapshot "
                            f"(parent not snapshotted, or its snapshot predates the branch point)")
        elif s.mode == "on-record":
            for anc, _ in chain:
                if anc.mode != "on-record":
                    errors.append(f"session {s.id[:8]} is on-record but its ancestor {anc.id[:8]} is {anc.mode} ({rule('fork-ancestry')})")

    # documents (S-0038, S-0039): the projection names its original by hash; the original's name is its
    # hash; the bytes must still hash to it. Chunks are role "document" and carry no attesting power.
    referenced: set[Path] = set()
    for s in sessions.values():
        if s.kind != "document":
            continue
        o = s.header.get("original") or {}
        h = str(o.get("hash") or "")
        if not ORIGINAL_HASH_RE.match(h):
            errors.append(f"document {s.id[:8]}: header has a missing or malformed original hash")
            continue
        hexdigest = h.split(":", 1)[1]
        rel = o.get("path") or ""
        opath = s.path.parent / rel if s.mode == "on-record" else root / "off-record" / rel
        m = ORIGINAL_NAME_RE.match(opath.name)
        if not m or m.group("hex") != hexdigest:
            errors.append(f"document {s.id[:8]}: original path {rel!r} is not named by its hash ({rule('frozen-projection')})")
        if not opath.is_file():
            errors.append(f"document {s.id[:8]}: original {rel} is missing")
        else:
            referenced.add(opath.resolve())
            actual = sha256_file(opath)
            if actual != hexdigest:
                errors.append(f"document {s.id[:8]}: original {rel} hashes to {actual[:12]}…, not its name; "
                              f"the bytes were damaged or modified ({rule('frozen-projection')})")
        for t in s.turns:
            if t.get("role") != "document" or t.get("author") != "document":
                errors.append(f"document {s.id[:8]}: chunk {t.get('uuid', '?')[:8]} is not role/author 'document'")
        if not s.header.get("extractor"):
            errors.append(f"document {s.id[:8]}: header has no extractor")
    for mode in ("on-record", "off-record"):
        d = originals_dir(root, mode)
        if d.is_dir():
            for p in d.iterdir():
                if p.is_file() and p.resolve() not in referenced:
                    warnings.append(f"original {p.relative_to(root)} is referenced by no projection")

    stmts = load_all_statements(root)
    ids: dict[str, str] = {}
    nums = []
    unstamped: list[str] = []
    no_answers: list[str] = []
    for st in stmts:
        name = st.path.name
        sid = st.meta.get("id")
        if not sid or not re.match(r"^S-\d{4,}$", sid):
            errors.append(f"{name}: missing or malformed id")
            continue
        if not name.startswith(sid + "-"):
            errors.append(f"{name}: filename does not start with its id {sid}")
        if sid in ids:
            errors.append(f"duplicate id {sid}: {name} and {ids[sid]}")
        ids[sid] = name
        nums.append(int(sid[2:]))
        if not st.meta.get("title"):
            errors.append(f"{sid}: missing title ({rule('title-and-answers')})")
        if not st.body.strip():
            errors.append(f"{sid}: empty body")
        # context of the claim (S-0043): required for statements attested under 0.3.0 or later
        if not str(st.meta.get("answers") or "").strip():
            versions = [a.get("system_version") for a in (st.meta.get("attestations") or []) if a.get("system_version")]
            if versions and version_tuple(versions[0]) >= (0, 3, 0):
                errors.append(f"{sid}: no answers line (what question it answers and in what role) ({rule('title-and-answers')})")
            else:
                no_answers.append(sid)

        # proposal / verified copy
        prop = st.meta.get("proposed_in")
        if not prop:
            errors.append(f"{sid}: missing proposed_in")
        else:
            s, t = resolve(prop)
            if t is None:
                errors.append(f"{sid}: proposed_in {prop} does not resolve")
            elif s.kind == "document":
                errors.append(f"{sid}: proposed_in {prop} is a document chunk; documents cannot attest ({rule('documents-cannot-attest')})")
            else:
                if s.mode != "on-record":
                    errors.append(f"{sid}: proposed_in {prop} is in a {s.mode} session ({rule('on-record-only')})")
                forms = {a.get("form") for a in (st.meta.get("attestations") or [])}
                if "agreed-with-modification" in forms:
                    if not st.meta.get("modified_in"):
                        errors.append(f"{sid}: agreed-with-modification but no modified_in turn")
                    else:
                        ms, mt = resolve(st.meta["modified_in"])
                        if mt is None or mt["role"] != "user":
                            errors.append(f"{sid}: modified_in {st.meta['modified_in']} does not resolve to a human turn")
                elif not text_contained(st.body, turn_text(t)):
                    errors.append(f"{sid}: body text is not contained verbatim in proposal turn {prop} ({rule('exact-text')})")
                if st.meta.get("originator") != t["author"]:
                    errors.append(f"{sid}: originator {st.meta.get('originator')!r} != proposal turn author {t['author']!r}")

        atts = st.meta.get("attestations") or []
        if not atts:
            warnings.append(f"{sid}: no attestations")
        seen_by = set()
        for a in atts:
            by, turn, form = a.get("by"), a.get("turn"), a.get("form")
            if form not in ATTESTATION_FORMS:
                errors.append(f"{sid}: attestation form {form!r} invalid")
            if by in seen_by:
                errors.append(f"{sid}: duplicate attestation by {by} ({rule('attest-once')})")
            seen_by.add(by)
            if not turn:
                errors.append(f"{sid}: attestation by {by} has no turn")
                continue
            s, t = resolve(turn)
            if t is None:
                errors.append(f"{sid}: attestation turn {turn} does not resolve")
                continue
            if s.kind == "document":
                errors.append(f"{sid}: attestation turn {turn} is a document chunk; documents cannot attest ({rule('documents-cannot-attest')})")
                continue
            if s.mode != "on-record":
                errors.append(f"{sid}: attestation turn {turn} is in a {s.mode} session (must be on-record) ({rule('on-record-only')})")
            if t["role"] != "user" or t["author"] != by:
                errors.append(f"{sid}: attestation turn {turn} is authored by {t['author']!r}, not {by!r} ({rule('own-turn')})")
            if form == "self" and not text_contained(st.body, turn_text(t)):
                errors.append(f"{sid}: self-attestation but body not contained in {turn} ({rule('exact-text')})")
            # system stamp (S-0030): version + content hash of the rules the act was made under
            h, v = a.get("system_hash"), a.get("system_version")
            if not h and not v:
                unstamped.append(f"{sid}/{by}")
            else:
                if not v:
                    errors.append(f"{sid}: attestation by {by} has system_hash but no system_version ({rule('system-stamp')})")
                if not h or not SYSTEM_HASH_RE.match(str(h)):
                    errors.append(f"{sid}: attestation by {by} has a missing or malformed system_hash ({rule('system-stamp')})")

        for c in st.meta.get("cites") or []:
            s, t = resolve(c, through_ancestry=True)
            if t is None:
                warnings.append(f"{sid}: background citation {c} does not resolve (off-record or missing)")

    # agent statements (S-0065..S-0067)
    agents = load_all_agent_statements(root)
    for st in agents:
        name = st.path.name
        aid = st.meta.get("id")
        if not aid or not re.match(r"^A-\d{4,}$", aid):
            errors.append(f"agent/{name}: missing or malformed id")
            continue
        if not name.startswith(aid + "-"):
            errors.append(f"agent/{name}: filename does not start with its id {aid}")
        if aid in ids:
            errors.append(f"duplicate id {aid}: {name} and {ids[aid]}")
        ids[aid] = name
        for k in ("title", "answers", "author"):
            if not str(st.meta.get(k) or "").strip():
                errors.append(f"{aid}: missing {k}")
        if not st.body.strip():
            errors.append(f"{aid}: empty body")
        if st.meta.get("attestations"):
            errors.append(f"{aid}: an agent statement carries no attestations ({rule('agent-no-attestations')})")
        if not SYSTEM_HASH_RE.match(str(st.meta.get("system_hash") or "")) or not st.meta.get("system_version"):
            errors.append(f"{aid}: missing or malformed system stamp ({rule('system-stamp')})")
        s, t = resolve(st.meta.get("made_in") or "")
        if t is None:
            errors.append(f"{aid}: made_in {st.meta.get('made_in')} does not resolve")
        elif s.kind == "document" or s.mode != "on-record":
            errors.append(f"{aid}: made_in {st.meta.get('made_in')} is not in an on-record conversation ({rule('on-record-only')})")
        elif t["author"] != st.meta.get("author") or t["role"] != "assistant":
            errors.append(f"{aid}: author {st.meta.get('author')!r} != made_in turn author {t['author']!r} ({rule('agent-own-turn')})")
        for x in st.meta.get("seen") or []:
            s, t = resolve(x.get("turn") or "")
            if t is None or s.kind == "document" or s.mode != "on-record":
                errors.append(f"{aid}: seen turn {x.get('turn')} does not resolve to an on-record turn ({rule('on-record-only')})")
            elif not text_contained(st.body, turn_text(t)):
                errors.append(f"{aid}: seen turn {x.get('turn')} does not show the full text ({rule('seen-in-full')})")
        if st.meta.get("withdrawn_in"):
            s, t = resolve(st.meta["withdrawn_in"])
            if t is None or s.mode != "on-record":
                errors.append(f"{aid}: withdrawn_in {st.meta['withdrawn_in']} does not resolve to an on-record turn ({rule('on-record-only')})")
        for rel in ("supersedes", "amends", "clarifies"):
            if st.meta.get(rel) and not str(st.meta[rel]).startswith("A-"):
                errors.append(f"{aid}: {rel} {st.meta[rel]}, a human statement; agents revise only agent statements ({rule('agent-revision')})")
        for c in st.meta.get("cites") or []:
            s, t = resolve(c, through_ancestry=True)
            if t is None:
                warnings.append(f"{aid}: background citation {c} does not resolve (off-record or missing)")

    # rests_on (S-0066): statements a statement builds on; stale when what it rests on is no longer in force
    by_id = {x.meta.get("id"): x for x in stmts + agents}
    for st in stmts + agents:
        sid = st.meta.get("id")
        in_force = not st.meta.get("superseded_by") and not st.meta.get("withdrawn_in")
        for x in st.meta.get("rests_on") or []:
            dep = by_id.get(x)
            if dep is None:
                errors.append(f"{sid}: rests_on {x}, which does not exist")
            elif in_force and str(sid).startswith("A-") and (dep.meta.get("superseded_by") or dep.meta.get("withdrawn_in")):
                gone = f"superseded by {dep.meta['superseded_by']}" if dep.meta.get("superseded_by") else "withdrawn"
                warnings.append(f"{sid}: rests on {x}, which is {gone}; review in cleanup ({rule('agent-revision')})")

    # relation reciprocity: supersedes/superseded_by (single), amends/amended_by and clarifies/clarified_by (lists)
    marks = {"supersedes": "superseded_by", "amends": "amended_by", "clarifies": "clarified_by"}
    for st in stmts + agents:
        sid = st.meta.get("id")
        for rel, mark in marks.items():
            old = st.meta.get(rel)
            if old:
                if old not in by_id:
                    errors.append(f"{sid}: {rel} {old}, which does not exist")
                else:
                    m = by_id[old].meta.get(mark)
                    ok = (m == sid) if rel == "supersedes" else (sid in (m or []))
                    if not ok:
                        errors.append(f"{old}: not marked {mark} {sid}")
            m = st.meta.get(mark)
            if rel == "supersedes":
                targets = [m] if m else []
            else:
                targets = m or []
            for new in targets:
                if new not in by_id or by_id[new].meta.get(rel) != sid:
                    errors.append(f"{sid}: {mark} {new} is not reciprocal")
        n_marks = len(st.meta.get("amended_by") or []) + len(st.meta.get("clarified_by") or [])
        if n_marks > 2 and not st.meta.get("superseded_by"):
            warnings.append(f"{sid}: {n_marks} amendment/clarification marks; consider consolidating by supersession ({rule('change-by-supersession')})")

    if unstamped:
        warnings.append(f"{len(unstamped)} attestation(s) predate system stamping (no system_version/hash): "
                        f"{unstamped[0]} .. {unstamped[-1]}")

    if no_answers:
        warnings.append(f"{len(no_answers)} statement(s) predate the answers line ({rule('title-and-answers')}): "
                        f"{no_answers[0]} .. {no_answers[-1]}")

    if nums:
        missing = sorted(set(range(1, max(nums) + 1)) - set(nums))
        if missing:
            warnings.append(f"id gaps: {', '.join(f'S-{n:04d}' for n in missing)}")

    for w in warnings:
        print(f"warning: {w}")
    for e in errors:
        print(f"ERROR: {e}")
    if errors:
        print(f"errors in files kb writes mean they were changed outside kb ({rule('write-through-kb')})")
    n_docs = sum(1 for s in sessions.values() if s.kind == "document")
    unseen = sum(1 for a in agents if not a.meta.get("seen"))
    print(f"{len(sessions) - n_docs} session(s), {n_docs} document(s), {len(stmts)} statement(s), "
          f"{len(agents)} agent statement(s) ({unseen} unseen): {len(errors)} error(s), {len(warnings)} warning(s)")
    return 1 if errors else 0


def add_parser(sub) -> None:
    p = sub.add_parser("check", help="verify invariants over evidence/ and attested/")
    p.set_defaults(func=run)
