"""Measure how far `pdftotext -layout FILE -` is from pypdf, page by page.

Usage: python -I testdata/compare_pdftotext.py FILE.pdf...
Both outputs are normalised the way kb/ingest.py stores chunks
(text.replace("\\r\\n", "\\n").strip("\\n")), then compared:
  exact     identical stored chunk text
  ws        identical after collapsing all whitespace runs to one space
  ratio     difflib similarity of the stored texts (1.0 = identical)
  wsratio   difflib similarity after whitespace collapsing
"""

import difflib
import re
import shutil
import site
import subprocess
import sys

try:
    import pypdf
except ImportError:
    sys.path.append(site.getusersitepackages())
    import pypdf
import logging

logging.disable(logging.CRITICAL)


def norm(t):
    return t.replace("\r\n", "\n").strip("\n")


def ws(t):
    return re.sub(r"\s+", " ", t).strip()


def pypdf_pages(path):
    r = pypdf.PdfReader(path)
    return [norm(p.extract_text() or "") for p in r.pages]


def pdftotext_pages(exe, path):
    out = subprocess.run([exe, "-layout", path, "-"], capture_output=True)
    if out.returncode != 0:
        return None
    pages = out.stdout.decode("utf-8", errors="replace").split("\f")
    if pages and not pages[-1].strip():
        pages.pop()
    return [norm(p) for p in pages]


def main():
    exe = shutil.which("pdftotext")
    if not exe:
        sys.exit("pdftotext not found")
    tot = {"pages": 0, "exact": 0, "ws": 0, "count_mismatch": 0}
    ratios = []
    wsratios = []
    for path in sys.argv[1:]:
        try:
            a = pypdf_pages(path)
        except Exception as e:
            print("%s: pypdf error %s" % (path, e))
            continue
        b = pdftotext_pages(exe, path)
        if b is None:
            print("%s: pdftotext failed" % path)
            continue
        if len(a) != len(b):
            tot["count_mismatch"] += 1
        exact = wsame = 0
        rs = []
        wrs = []
        for i in range(max(len(a), len(b))):
            x = a[i] if i < len(a) else ""
            y = b[i] if i < len(b) else ""
            tot["pages"] += 1
            if x == y:
                exact += 1
            if ws(x) == ws(y):
                wsame += 1
            r = difflib.SequenceMatcher(None, x, y, autojunk=False).ratio() if (x or y) else 1.0
            rs.append(r)
            wrs.append(difflib.SequenceMatcher(None, ws(x), ws(y), autojunk=False).ratio() if (x or y) else 1.0)
        tot["exact"] += exact
        tot["ws"] += wsame
        ratios += rs
        wsratios.extend(wrs)
        print("%-40s pages pypdf=%d pdftotext=%d exact=%d ws-equal=%d ratio=%.3f wsratio=%.3f"
              % (path.replace("\\", "/").split("/")[-1], len(a), len(b), exact, wsame, sum(rs) / max(1, len(rs)),
                 sum(wrs) / max(1, len(wrs))))
    print("TOTAL pages=%(pages)d exact=%(exact)d ws-equal=%(ws)d page-count-mismatch-files=%(count_mismatch)d" % tot,
          "ratio=%.3f wsratio=%.3f" % (sum(ratios) / max(1, len(ratios)), sum(wsratios) / max(1, len(wsratios))))


if __name__ == "__main__":
    main()
