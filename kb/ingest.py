"""kb ingest — bring an external document into evidence/ (or off-record/documents/) as a frozen projection
beside its original (S-0004, S-0038, S-0039).

Two files are written:
  originals/<sha256-hex><ext>   the original bytes, named by their content hash; the extension is
                                kept so the file opens in ordinary programs. Same bytes twice land in
                                one file, which is how a duplicate is noticed.
  <date>-doc-<id8>.jsonl        the projection: a kb-document header (original hash, name, size, media
                                type, extractor, who ingested it, when, system stamp) followed by chunk
                                records, each with a uuid. Citations point into the projection:
                                <document-id>#<chunk-uuid>.

Ids are deterministic: document_id = uuid5(hash, extractor), chunk uuid = uuid5(hash, extractor, seq).
A projection is never rewritten (S-0038): re-running the same extractor over the same bytes is refused;
a different extractor is a new evidence item pointing at the same original.

Extractors are deterministic projections, chosen by extension unless --extractor is given:
  text-v1    UTF-8 text (markdown, txt, csv, json, source): sections split at markdown headings,
             else paragraph groups; locator = 1-based line range of the original text
  html-v1    HTML: tags stripped, headings kept as markdown headings, then as text-v1;
             locator = line range of the extracted text, not of the source
  pdf-v1     one chunk per page (pypdf if importable, else the pdftotext command); locator = page
  docx-v1    Word: paragraphs from word/document.xml, Heading styles as markdown headings, tables as
             pipe rows; locator = paragraph range of the extracted text
  opaque-v1  anything else (images, audio, binaries): one chunk carrying the media type and an optional
             --caption; the projection has no text of its own
Chunks are role "document", author "document": citable as background, never an attesting turn (S-0037).
"""
from __future__ import annotations

import json
import mimetypes
import re
import shutil
import subprocess
import sys
import uuid
import zipfile
from datetime import datetime, timezone
from html.parser import HTMLParser
from pathlib import Path

from .common import (DOCUMENT_VERSION, EXTRACTORS, MODES, current_user, die, rule, evidence_dirs,
                     originals_dir, repo_root, sha256_file, system_stamp)

# The namespace string is part of every document id; it keeps its original value so ids stay stable.
DOC_NS = uuid.uuid5(uuid.NAMESPACE_URL, "convkb:document")
HEADING_RE = re.compile(r"^#{1,6}\s+\S")
TEXT_EXTS = {".md", ".markdown", ".txt", ".text", ".csv", ".tsv", ".json", ".jsonl", ".yaml", ".yml",
             ".rst", ".log", ".py", ".js", ".ts", ".toml", ".ini", ".cfg", ".xml", ".sql", ".sh", ".bat"}
MAX_CHUNK_CHARS = 3000


def pick_extractor(path: Path) -> str:
    ext = path.suffix.lower()
    if ext in TEXT_EXTS:
        return "text-v1"
    if ext in (".html", ".htm"):
        return "html-v1"
    if ext == ".pdf":
        return "pdf-v1"
    if ext == ".docx":
        return "docx-v1"
    return "opaque-v1"


# ---------------------------------------------------------------- chunking

def chunk_units(units: list[str], unit_name: str) -> list[dict]:
    """Split a list of text units (lines or paragraphs) into chunks. Sections start at markdown
    headings; a run with no headings is grouped by blank-line paragraphs up to MAX_CHUNK_CHARS.
    Returns [{"locator": {unit_name: [start, end], "heading"?: str}, "text": str}], 1-based inclusive."""
    has_headings = any(HEADING_RE.match(u) for u in units)
    chunks: list[dict] = []
    if has_headings:
        start = 0
        for i in range(1, len(units) + 1):
            if i == len(units) or HEADING_RE.match(units[i]):
                block = units[start:i]
                text = "\n".join(block).strip("\n")
                if text.strip():
                    loc = {unit_name: [start + 1, i]}
                    if HEADING_RE.match(block[0]):
                        loc["heading"] = block[0].lstrip("#").strip()[:120]
                    chunks.append({"locator": loc, "text": text})
                start = i
        return chunks
    # paragraph groups
    i = 0
    n = len(units)
    while i < n:
        while i < n and not units[i].strip():
            i += 1
        if i >= n:
            break
        start = i
        size = 0
        while i < n:
            j = i
            while j < n and units[j].strip():
                j += 1
            para_len = sum(len(u) + 1 for u in units[i:j])
            if size and size + para_len > MAX_CHUNK_CHARS:
                break
            size += para_len
            i = j
            while i < n and not units[i].strip():
                i += 1
        end = i
        while end > start and not units[end - 1].strip():
            end -= 1
        chunks.append({"locator": {unit_name: [start + 1, end]}, "text": "\n".join(units[start:end])})
    return chunks


# ---------------------------------------------------------------- extractors

def extract_text(path: Path) -> tuple[list[dict], str | None]:
    raw = path.read_bytes()
    try:
        text = raw.decode("utf-8-sig")
    except UnicodeDecodeError:
        die(f"{path.name} is not UTF-8 text; pass --extractor opaque-v1 to store it without a text projection")
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    title = None
    for l in lines:
        if HEADING_RE.match(l):
            title = l.lstrip("#").strip()
            break
    return chunk_units(lines, "lines"), title


class _HTMLText(HTMLParser):
    BLOCK = {"p", "div", "br", "li", "ul", "ol", "tr", "table", "section", "article", "header", "footer",
             "pre", "blockquote", "hr", "main", "nav", "aside", "dd", "dt", "figure", "figcaption"}
    SKIP = {"script", "style", "noscript", "head", "template"}

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.out: list[str] = []
        self.title: str | None = None
        self._in_title = False
        self._skip = 0
        self._heading = False

    def handle_starttag(self, tag, attrs):
        if tag in self.SKIP:
            self._skip += 1
        elif tag == "title":
            self._in_title = True
        elif tag in ("h1", "h2", "h3", "h4", "h5", "h6"):
            self.out.append("\n\n" + "#" * int(tag[1]) + " ")
            self._heading = True
        elif tag in ("td", "th"):
            self.out.append(" | ")
        elif tag in self.BLOCK:
            self.out.append("\n" if tag == "br" else "\n\n")

    def handle_endtag(self, tag):
        if tag in self.SKIP:
            self._skip = max(0, self._skip - 1)
        elif tag == "title":
            self._in_title = False
        elif tag in ("h1", "h2", "h3", "h4", "h5", "h6"):
            self.out.append("\n\n")
            self._heading = False
        elif tag in self.BLOCK:
            self.out.append("\n\n")

    def handle_data(self, data):
        if self._skip:
            return
        if self._in_title:
            self.title = (self.title or "") + data.strip()
            return
        self.out.append(data.strip() if self._heading else re.sub(r"\s+", " ", data))


def extract_html(path: Path) -> tuple[list[dict], str | None]:
    raw = path.read_bytes()
    try:
        src = raw.decode("utf-8-sig")
    except UnicodeDecodeError:
        src = raw.decode("latin-1")
    p = _HTMLText()
    p.feed(src)
    text = "".join(p.out)
    text = re.sub(r"[ \t]+\n", "\n", text)
    text = re.sub(r"\n[ \t]+", "\n", text)
    text = re.sub(r"\n{3,}", "\n\n", text).strip("\n")
    lines = [l.strip() for l in text.split("\n")]
    return chunk_units(lines, "lines"), (p.title or None)


def extract_pdf(path: Path) -> tuple[list[dict], str | None]:
    pages: list[str] = []
    title = None
    try:
        import pypdf  # type: ignore
        reader = pypdf.PdfReader(str(path))
        try:
            title = (reader.metadata or {}).get("/Title") or None
        except Exception:
            title = None
        for pg in reader.pages:
            pages.append(pg.extract_text() or "")
    except ImportError:
        if not shutil.which("pdftotext"):
            die("pdf-v1 needs the pypdf package or the pdftotext command; neither is available")
        out = subprocess.run(["pdftotext", "-layout", str(path), "-"], capture_output=True)
        if out.returncode != 0:
            die(f"pdftotext failed: {out.stderr.decode(errors='replace').strip()}")
        pages = out.stdout.decode("utf-8", errors="replace").split("\f")
        if pages and not pages[-1].strip():
            pages.pop()
    chunks = []
    for n, txt in enumerate(pages, 1):
        chunks.append({"locator": {"page": n}, "text": txt.replace("\r\n", "\n").strip("\n")})
    return chunks, (str(title).strip() if title else None)


def extract_docx(path: Path) -> tuple[list[dict], str | None]:
    import xml.etree.ElementTree as ET
    W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
    try:
        with zipfile.ZipFile(path) as z:
            xml = z.read("word/document.xml")
    except (zipfile.BadZipFile, KeyError) as e:
        die(f"{path.name} is not a Word document: {e}")
    root = ET.fromstring(xml)
    body = root.find(f"{W}body")
    paras: list[str] = []
    title = None

    def para_text(p) -> str:
        parts = []
        for el in p.iter():
            if el.tag == f"{W}t":
                parts.append(el.text or "")
            elif el.tag == f"{W}tab":
                parts.append("\t")
            elif el.tag == f"{W}br":
                parts.append("\n")
        return "".join(parts)

    for el in (body if body is not None else []):
        if el.tag == f"{W}p":
            text = para_text(el)
            style = el.find(f"{W}pPr/{W}pStyle")
            sval = style.get(f"{W}val", "") if style is not None else ""
            m = re.match(r"(?i)heading(\d)", sval)
            if m and text.strip():
                text = "#" * int(m.group(1)) + " " + text.strip()
                title = title or text.lstrip("#").strip()
            elif sval.lower() == "title" and text.strip():
                title = title or text.strip()
                text = "# " + text.strip()
            paras.append(text)
        elif el.tag == f"{W}tbl":
            for tr in el.iter(f"{W}tr"):
                cells = [para_text(tc).replace("\n", " ").strip() for tc in tr.iter(f"{W}tc")]
                paras.append("| " + " | ".join(cells) + " |")
            paras.append("")
    return chunk_units(paras, "paragraphs"), title


def extract_opaque(path: Path, caption: str | None, media_type: str) -> tuple[list[dict], str | None]:
    content = [{"type": "file", "media_type": media_type, "size": path.stat().st_size}]
    if caption:
        content.append({"type": "text", "text": caption})
    return [{"locator": {"whole": True}, "content": content}], None


# ---------------------------------------------------------------- run

def run(args) -> None:
    root = repo_root()
    user = current_user(args.user)
    if args.mode not in MODES:
        die(f"mode must be one of {MODES}")
    src = Path(args.path)
    if not src.is_file():
        die(f"no such file: {src}")
    extractor = args.extractor or pick_extractor(src)
    if extractor not in EXTRACTORS:
        die(f"extractor must be one of {EXTRACTORS}")
    if args.caption and extractor != "opaque-v1":
        die("--caption applies to opaque-v1 only; other extractors take their text from the document")

    hexdigest = sha256_file(src)
    ext = src.suffix.lower() if re.match(r"^\.[A-Za-z0-9]{1,12}$", src.suffix) else ""
    media_type = mimetypes.guess_type(src.name)[0] or "application/octet-stream"
    doc_id = str(uuid.uuid5(DOC_NS, f"{hexdigest}:{extractor}"))

    # one mode per original, as for sessions
    other_mode = "off-record" if args.mode == "on-record" else "on-record"
    other = originals_dir(root, other_mode)
    if other.is_dir() and any(p.name.startswith(hexdigest) for p in other.iterdir()):
        die(f"these bytes are already in evidence as {other_mode}; a document has one mode ({rule('mode-fixed')})")
    # frozen projection (S-0038): same bytes + same extractor is the same evidence item
    for d, _ in evidence_dirs(root):
        for p in d.glob(f"*-doc-{doc_id[:8]}.jsonl"):
            die(f"already ingested under extractor {extractor}: {p.relative_to(root)} "
                f"(projections are frozen; a different --extractor makes a new item) ({rule('frozen-projection')})")

    if extractor == "text-v1":
        chunks, title = extract_text(src)
    elif extractor == "html-v1":
        chunks, title = extract_html(src)
    elif extractor == "pdf-v1":
        chunks, title = extract_pdf(src)
    elif extractor == "docx-v1":
        chunks, title = extract_docx(src)
    else:
        chunks, title = extract_opaque(src, args.caption, media_type)
    if not chunks:
        die("extractor produced no chunks; pass --extractor opaque-v1 to store the file without a text projection")

    records = []
    for seq, c in enumerate(chunks, 1):
        content = c.get("content") or [{"type": "text", "text": c["text"]}]
        records.append({
            "type": "chunk",
            "uuid": str(uuid.uuid5(DOC_NS, f"{hexdigest}:{extractor}:{seq}")),
            "seq": seq,
            "role": "document",
            "author": "document",
            "locator": c["locator"],
            "content": content,
        })

    odir = originals_dir(root, args.mode)
    odir.mkdir(parents=True, exist_ok=True)
    original = odir / f"{hexdigest}{ext}"
    same_hash = [p for p in odir.iterdir() if p.name.startswith(hexdigest)]
    if same_hash and original not in same_hash:
        original = same_hash[0]  # same bytes under another extension: reuse, do not duplicate
    duplicate = original.exists()
    if not duplicate:
        shutil.copyfile(src, original)
        if sha256_file(original) != hexdigest:
            original.unlink()
            die("copy did not verify; nothing written")

    dest_dir = root / "evidence" if args.mode == "on-record" else root / "off-record" / "documents"
    dest_dir.mkdir(parents=True, exist_ok=True)
    now = datetime.now(timezone.utc)
    dest = dest_dir / f"{now.date().isoformat()}-doc-{doc_id[:8]}.jsonl"
    header = {
        "type": "kb-document",
        "version": DOCUMENT_VERSION,
        "document_id": doc_id,
        "title": args.title or title or src.name,
        "mode": args.mode,
        "extractor": extractor,
        "original": {
            "hash": f"sha256:{hexdigest}",
            "path": f"originals/{original.name}",
            "name": src.name,
            "size": src.stat().st_size,
            "media_type": media_type,
        },
        "ingested_by": user,
        "ingested_at": now.isoformat(timespec="seconds"),
        **system_stamp(root),
        "chunk_count": len(records),
    }
    tmp = dest.with_suffix(".jsonl.tmp")
    with tmp.open("w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps(header, ensure_ascii=False) + "\n")
        for r in records:
            fh.write(json.dumps(r, ensure_ascii=False) + "\n")
    tmp.replace(dest)
    print(f"wrote {dest.relative_to(root)}: {len(records)} chunk(s), extractor={extractor}, mode={args.mode}")
    print(f"original {'already present' if duplicate else 'stored'} as {original.relative_to(root)} "
          f"({header['original']['size']} bytes, {media_type})")
    print(f"document id {doc_id}; cite chunks as {doc_id[:8]}#<chunk-uuid> (kb show {doc_id[:8]})")
    if args.mode == "off-record":
        print("note: off-record/ is gitignored; this document is at risk of loss by design", file=sys.stderr)


def add_parser(sub) -> None:
    p = sub.add_parser("ingest", help="bring an external document into evidence as a frozen projection + original")
    p.add_argument("path", help="file to ingest (any format; the text projection depends on the extractor)")
    p.add_argument("--mode", default="on-record", help="on-record (evidence/) or off-record (off-record/documents/)")
    p.add_argument("--extractor", help="|".join(EXTRACTORS) + " (default: by extension)")
    p.add_argument("--title", help="title for the projection header (default: first heading, else file name)")
    p.add_argument("--caption", help="opaque-v1 only: a line of text to carry with the file")
    p.add_argument("--user", help="who brings the document in (else KB_USER / ~/.kenttheclerk.json)")
    p.set_defaults(func=run)
