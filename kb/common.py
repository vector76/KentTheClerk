"""Shared helpers: repo layout, user identity, statement file parsing, evidence access."""
from __future__ import annotations

import json
import os
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

SNAPSHOT_VERSION = 1
DOCUMENT_VERSION = 1

# The system (S-0030): the rules an act was made under, meaning Kent the Clerk itself: the tool code,
# the rules, the procedure, the introduction and the front-end blocks. SYSTEM_VERSION is bumped by hand
# on a rules change; the hash is content-addressed over SYSTEM_FILES, relative to the product folder,
# so any modification, versioned or not, is detectable. The project's own CLAUDE.md is not hashed.
SYSTEM_VERSION = "0.7.0"
PRODUCT_DIR = Path(__file__).resolve().parent.parent
SYSTEM_FILES = ("kb.py", "kb/*.py", "rules/*.md", "procedure.md", "README.md", "front-ends/*/*")
# Kent's rules (S-0057, S-0058, S-0077): every refusal cites the rule it enforces, by number, so an
# agent is never refused by an unwritten rule. The numbers are those in rules/; this table is the one
# place the code holds them.
PRODUCT_RULES = {
    "write-through-kb": 1, "append-only": 2, "no-pending-statements": 3, "change-by-supersession": 4,
    "documents-cannot-attest": 8, "frozen-projection": 9, "mode-fixed": 11, "on-record-only": 14,
    "fork-ancestry": 17, "own-turn": 21, "after-proposal": 22, "same-session": 23, "exact-text": 25,
    "attest-once": 26, "title-and-answers": 27, "agent-from-conversation": 31, "agent-own-turn": 32,
    "seen-in-full": 34, "agent-revision": 35, "agent-no-attestations": 37, "system-stamp": 44,
}


def rule(key: str) -> str:
    """How a message cites a rule, e.g. "Kent Rule 7"."""
    return f"Kent Rule {PRODUCT_RULES[key]}"


SYSTEM_HASH_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
FILTER_PROFILES = ("human-visible-v1", "full-v1")
MODES = ("on-record", "off-record")
EXTRACTORS = ("text-v1", "html-v1", "pdf-v1", "docx-v1", "opaque-v1")
ORIGINAL_HASH_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
ORIGINAL_NAME_RE = re.compile(r"^(?P<hex>[0-9a-f]{64})(?P<ext>\.[A-Za-z0-9]{1,12})?$")
ATTESTATION_FORMS = ("proposed-and-agreed", "agreed-with-modification", "self", "endorsement")
RELATIONS = ("supersedes", "amends", "clarifies")
MARK_KEY = {"supersedes": "superseded_by", "amends": "amended_by", "clarifies": "clarified_by"}
# Human statements live in attested/ as S-####; agent statements (S-0065) in agent/ as A-####.
STATEMENT_ID_RE = re.compile(r"^(?P<kind>[SA])-\d{4,}$")
STATEMENT_DIRS = {"S": "attested", "A": "agent"}


# Kent is installed outside the customer's repositories, and the knowledge folder usually sits beside
# the code repository rather than in it (S-0084). Its location comes per user and per project from
# KENT_KB (S-0085); without it, kb looks for the record in the current folder and above.
KB_ENV = "KENT_KB"


def is_kb_folder(p: Path) -> bool:
    return (p / "attested").is_dir() and (p / "evidence").is_dir()


def find_kb_root(start: Path | None = None) -> tuple[Path | None, str]:
    """Where the knowledge base is and how it was found: (path or None, explanation)."""
    env = os.environ.get(KB_ENV)
    if env:
        p = Path(env).expanduser().resolve()
        if is_kb_folder(p):
            return p, f"from {KB_ENV}"
        return None, f"{KB_ENV} names {p}, which has no attested/ and evidence/ folders"
    p = (start or Path.cwd()).resolve()
    for cand in (p, *p.parents):
        if is_kb_folder(cand):
            return cand, "from the current folder" if cand == p else "from a folder above the current one"
    return None, (f"{KB_ENV} is not set, and no attested/ and evidence/ folders were found in the current "
                  "folder or above")


def repo_root(start: Path | None = None) -> Path:
    root, how = find_kb_root(start)
    if root is None:
        die(f"no knowledge base found: {how}")
    return root


def die(msg: str, code: int = 1) -> None:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(code)


def warn(msg: str) -> None:
    print(f"warning: {msg}", file=sys.stderr)


# ---------------------------------------------------------------- system identity

def system_files() -> list[Path]:
    out: list[Path] = []
    for pat in SYSTEM_FILES:
        out.extend(p for p in PRODUCT_DIR.glob(pat) if p.is_file() and "__pycache__" not in p.parts)
    return sorted(set(out), key=lambda p: p.relative_to(PRODUCT_DIR).as_posix())


def system_hash() -> str:
    """sha256 over (path relative to the product folder, content) of every system file, in path order.
    Line endings are normalised to LF so the hash is the same whichever platform checked the files out."""
    import hashlib
    h = hashlib.sha256()
    for p in system_files():
        h.update(p.relative_to(PRODUCT_DIR).as_posix().encode("utf-8") + b"\0")
        h.update(p.read_bytes().replace(b"\r\n", b"\n") + b"\0")
    return "sha256:" + h.hexdigest()


def system_stamp(root: Path | None = None) -> dict:
    """Fields every recorded act carries (S-0030). The system is the product, wherever the record is."""
    return {"system_version": SYSTEM_VERSION, "system_hash": system_hash()}


# ---------------------------------------------------------------- identity

def current_user(explicit: str | None = None) -> str:
    """User id: --user flag, else KB_USER env, else ~/.kenttheclerk.json {"user": ...}. The older
    ~/.convkb.json is still read if the new file is absent."""
    if explicit:
        return explicit
    if os.environ.get("KB_USER"):
        return os.environ["KB_USER"]
    for name in (".kenttheclerk.json", ".convkb.json"):
        cfg = Path.home() / name
        if cfg.exists():
            try:
                u = json.loads(cfg.read_text(encoding="utf-8")).get("user")
                if u:
                    return u
            except json.JSONDecodeError:
                pass
    die("no user id: pass --user, set KB_USER, or write ~/.kenttheclerk.json with {\"user\": \"<id>\"}")


# ---------------------------------------------------------------- pointers

POINTER_RE = re.compile(r"^(?P<session>[0-9a-f-]{8,})#(?P<uuid>[0-9a-f-]{8,})$")


@dataclass(frozen=True)
class Pointer:
    session: str
    uuid: str

    @classmethod
    def parse(cls, s: str) -> "Pointer":
        m = POINTER_RE.match(s.strip())
        if not m:
            die(f"bad evidence pointer {s!r}; expected <session-id>#<turn-uuid>")
        return cls(m.group("session"), m.group("uuid"))

    def __str__(self) -> str:
        return f"{self.session}#{self.uuid}"


# ---------------------------------------------------------------- evidence

@dataclass
class Session:
    """One evidence item: a conversation snapshot (turns) or a document projection (chunks; S-0038).
    Both are a header record followed by uuid-keyed records, so pointers, resolution, indexing and
    citation work the same way. `kind` tells them apart where it matters (documents cannot attest, S-0037)."""
    path: Path
    header: dict
    turns: list[dict] = field(default_factory=list)

    @property
    def id(self) -> str:
        return self.header.get("session_id") or self.header["document_id"]

    @property
    def kind(self) -> str:
        return "document" if self.header.get("type") == "kb-document" else "session"

    @property
    def mode(self) -> str:
        return self.header.get("mode", "on-record")

    def turn(self, uuid: str) -> dict | None:
        for t in self.turns:
            if t["uuid"] == uuid or t["uuid"].startswith(uuid):
                return t
        return None

    def turn_index(self, uuid: str) -> int | None:
        for i, t in enumerate(self.turns):
            if t["uuid"] == uuid or t["uuid"].startswith(uuid):
                return i
        return None

    @property
    def branch_point(self) -> str | None:
        """Uuid of the parent-session turn this session forked from, else None. Snapshots record it in
        the header; older ones fall back to the first turn's parent."""
        if "branch_point" in self.header:
            return self.header["branch_point"] or None
        if not self.turns:
            return None
        return self.turns[0].get("parent") or None


def load_session(path: Path) -> Session:
    header = None
    turns: list[dict] = []
    with path.open(encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            if rec.get("type") in ("kb-snapshot", "kb-document"):
                header = rec
            else:
                turns.append(rec)
    if header is None:
        die(f"{path} has no kb-snapshot / kb-document header")
    return Session(path, header, turns)


def evidence_dirs(root: Path) -> list[tuple[Path, str]]:
    """(directory, mode) pairs to scan. off-record/sessions and off-record/documents are scanned when
    present (client-side; never part of the record)."""
    out = [(root / "evidence", "on-record")]
    for sub in ("sessions", "documents"):
        off = root / "off-record" / sub
        if off.is_dir():
            out.append((off, "off-record"))
    return out


def originals_dir(root: Path, mode: str) -> Path:
    """Where original document bytes live, named by content hash (S-0039)."""
    return root / ("evidence" if mode == "on-record" else "off-record") / "originals"


def sha256_file(path: Path) -> str:
    import hashlib
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for block in iter(lambda: fh.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def load_all_sessions(root: Path) -> dict[str, Session]:
    out: dict[str, Session] = {}
    for d, mode in evidence_dirs(root):
        for p in sorted(d.glob("*.jsonl")):
            s = load_session(p)
            if s.mode != mode:
                warn(f"{p}: header mode {s.mode!r} disagrees with directory ({mode})")
            if s.id in out:
                warn(f"duplicate session {s.id}: {p} and {out[s.id].path}")
            out[s.id] = s
    return out


def session_by_prefix(sessions: dict[str, Session], prefix: str) -> Session | None:
    for sid, s in sessions.items():
        if sid.startswith(prefix):
            return s
    return None


def ancestry(sessions: dict[str, Session], s: Session) -> list[tuple[Session, int]]:
    """A fork's history beyond its own turns (S-0034): [(ancestor, cutoff), ...] nearest first, where
    cutoff is the index of the branch-point turn in that ancestor; turns after it are a sibling branch.
    Stops (silently) where a branch point resolves in no loaded session."""
    out: list[tuple[Session, int]] = []
    seen = {s.id}
    cur = s
    while (bp := cur.branch_point):
        hit = None
        for cand in sessions.values():
            if cand.id in seen:
                continue
            i = cand.turn_index(bp)
            if i is not None:
                hit = (cand, i)
                break
        if hit is None:
            break
        out.append(hit)
        seen.add(hit[0].id)
        cur = hit[0]
    return out


def resolve_pointer(sessions: dict[str, Session], ptr: Pointer) -> tuple[Session | None, dict | None]:
    """Resolve <session>#<uuid> to (session-holding-the-turn, turn). The named session is tried
    first; failing that, its ancestry chain up to each branch point. Returns (named-or-None, None)
    when the turn is not in the named session's history."""
    s = session_by_prefix(sessions, ptr.session)
    if s is None:
        return None, None
    t = s.turn(ptr.uuid)
    if t is not None:
        return s, t
    for anc, cutoff in ancestry(sessions, s):
        i = anc.turn_index(ptr.uuid)
        if i is not None and i <= cutoff:
            return anc, anc.turns[i]
    return s, None


def find_session(root: Path, session_id: str) -> Session | None:
    for d, _ in evidence_dirs(root):
        for p in d.glob("*.jsonl"):
            if session_id in p.name:
                return load_session(p)
    # fall back to full scan for prefix matches
    for s in load_all_sessions(root).values():
        if s.id.startswith(session_id):
            return s
    return None


def turn_text(turn: dict) -> str:
    """Visible text of a turn (text blocks joined)."""
    parts = []
    for b in turn.get("content", []):
        if b.get("type") == "text":
            parts.append(b.get("text", ""))
    return "\n".join(parts)


# ---------------------------------------------------------------- statements

FRONTMATTER_RE = re.compile(r"^---\r?\n(.*?)\r?\n---\r?\n?(.*)$", re.S)


@dataclass
class Statement:
    path: Path
    meta: dict
    body: str

    @property
    def id(self) -> str | None:
        return self.meta.get("id")


def _parse_scalar(v: str):
    v = v.strip()
    if v.startswith(("'", '"')) and v.endswith(v[0]) and len(v) >= 2:
        return v[1:-1]
    if v in ("null", "~", ""):
        return None
    if v == "true":
        return True
    if v == "false":
        return False
    return v


def parse_frontmatter(text: str) -> tuple[dict, str]:
    """Minimal YAML subset: key: scalar | key: [a, b] | key:\\n  - item | key:\\n  - k: v\\n    k2: v2"""
    m = FRONTMATTER_RE.match(text)
    if not m:
        return {}, text
    raw, body = m.group(1), m.group(2)
    meta: dict = {}
    lines = raw.splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        if not line.strip() or line.lstrip().startswith("#"):
            i += 1
            continue
        if line.startswith((" ", "\t")):
            die(f"unexpected indentation in frontmatter: {line!r}")
        key, _, rest = line.partition(":")
        key = key.strip()
        rest = rest.strip()
        if rest.startswith("[") and rest.endswith("]"):
            inner = rest[1:-1].strip()
            meta[key] = [_parse_scalar(x) for x in inner.split(",")] if inner else []
            i += 1
            continue
        if rest:
            meta[key] = _parse_scalar(rest)
            i += 1
            continue
        # block list
        items = []
        i += 1
        while i < len(lines) and (lines[i].startswith((" ", "\t")) or not lines[i].strip()):
            l = lines[i]
            if not l.strip():
                i += 1
                continue
            s = l.strip()
            if s.startswith("- "):
                s = s[2:]
                if ":" in s and not s.startswith(("'", '"')):
                    obj = {}
                    k, _, v = s.partition(":")
                    obj[k.strip()] = _parse_scalar(v)
                    i += 1
                    while i < len(lines) and lines[i].startswith((" ", "\t")) and not lines[i].strip().startswith("- ") and lines[i].strip():
                        k, _, v = lines[i].strip().partition(":")
                        obj[k.strip()] = _parse_scalar(v)
                        i += 1
                    items.append(obj)
                    continue
                items.append(_parse_scalar(s))
            i += 1
        meta[key] = items
    return meta, body


def dump_frontmatter(meta: dict) -> str:
    def q(v) -> str:
        if v is None:
            return "null"
        if isinstance(v, bool):
            return "true" if v else "false"
        s = str(v)
        if s == "" or any(c in s for c in ":#[]{},\"'") or s != s.strip():
            return json.dumps(s, ensure_ascii=False)
        return s

    out = ["---"]
    for k, v in meta.items():
        if isinstance(v, list):
            if not v:
                out.append(f"{k}: []")
                continue
            out.append(f"{k}:")
            for item in v:
                if isinstance(item, dict):
                    first = True
                    for ik, iv in item.items():
                        out.append(("  - " if first else "    ") + f"{ik}: {q(iv)}")
                        first = False
                else:
                    out.append(f"  - {q(item)}")
        else:
            out.append(f"{k}: {q(v)}")
    out.append("---")
    return "\n".join(out) + "\n"


def load_statement(path: Path) -> Statement:
    meta, body = parse_frontmatter(path.read_text(encoding="utf-8"))
    return Statement(path, meta, body.strip("\n"))


def load_all_statements(root: Path) -> list[Statement]:
    return [load_statement(p) for p in sorted((root / "attested").glob("S-*.md"))]


def load_all_agent_statements(root: Path) -> list[Statement]:
    return [load_statement(p) for p in sorted((root / "agent").glob("A-*.md"))]


def next_statement_id(root: Path, kind: str = "S") -> str:
    d = root / STATEMENT_DIRS[kind]
    nums = [int(m.group(1)) for p in d.glob(f"{kind}-*.md")
            if (m := re.match(rf"{kind}-(\d+)", p.name))]
    return f"{kind}-{(max(nums) + 1 if nums else 1):04d}"


def statement_path(root: Path, sid: str) -> Path | None:
    """File of a human (S-) or agent (A-) statement by id, or None."""
    m = STATEMENT_ID_RE.match(str(sid or ""))
    if not m:
        return None
    hits = list((root / STATEMENT_DIRS[m.group("kind")]).glob(f"{sid}-*.md"))
    return hits[0] if len(hits) == 1 else None


def mark_relation(root: Path, old_id: str, relation: str, new_id: str) -> None:
    """Non-modification marks on the original (S-0016): superseded_by is single; amended_by/clarified_by accumulate."""
    p = statement_path(root, old_id)
    if p is None:
        warn(f"{relation} {old_id}: statement not found; not marking")
        return
    st = load_statement(p)
    key = MARK_KEY[relation]
    if relation == "supersedes":
        st.meta[key] = new_id
    else:
        lst = st.meta.get(key) or []
        if new_id not in lst:
            lst.append(new_id)
        st.meta[key] = lst
    p.write_text(dump_frontmatter(st.meta) + "\n" + st.body + "\n", encoding="utf-8")
    print(f"{old_id} marked {key} {new_id}")


def slugify(s: str, limit: int = 60) -> str:
    s = re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")
    return s[:limit].rstrip("-")


def version_tuple(v: str) -> tuple:
    """"0.3.0" -> (0, 3, 0); tolerant of junk (returns (0,))."""
    try:
        return tuple(int(x) for x in str(v).split("."))
    except ValueError:
        return (0,)


def normalize_ws(s: str) -> str:
    return re.sub(r"\s+", " ", s).strip()


def text_contained(needle: str, haystack: str) -> bool:
    """Whitespace-insensitive substring test used for verified copies."""
    return normalize_ws(needle) in normalize_ws(haystack)
