"""Reference side of the htmltext differential test.

Usage: python -I pyextract.py INPUTS.json
INPUTS.json is a JSON list of base64 strings (raw file bytes). Prints a JSON list, one entry
per input: {"lines": [...], "title": str-or-null} or {"error": "<ExceptionClassName>", "message": str(exc)}.

It runs _HTMLText and the body of extract_html up to chunk_units, as the Python recorder had them in
kb/ingest.py (copied here when the Python version was retired, after commit d1333a5).
"""
import base64
import json
import re
import sys
from html.parser import HTMLParser


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
