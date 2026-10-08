"""Reference side of the htmltext differential test.

Usage: python -I pyextract.py INPUTS.json
INPUTS.json is a JSON list of base64 strings (raw file bytes). Prints a JSON list, one entry
per input: {"lines": [...], "title": str-or-null} or {"error": "<ExceptionClassName>", "message": str(exc)}.

It runs the real _HTMLText from kb/ingest.py and the body of extract_html up to chunk_units.
"""
import base64
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from kb.ingest import _HTMLText  # noqa: E402


def extract(raw: bytes):
    # --- extract_html body, minus chunk_units ---
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
    return lines, (p.title or None)


def main() -> None:
    with open(sys.argv[1], encoding="utf-8") as f:
        inputs = json.load(f)
    results = []
    for b in inputs:
        try:
            lines, title = extract(base64.b64decode(b))
            results.append({"lines": lines, "title": title})
        except Exception as e:  # mirrored by htmltext.ExtractErr
            results.append({"error": type(e).__name__, "message": str(e)})
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stdout.write(json.dumps(results))


if __name__ == "__main__":
    main()
