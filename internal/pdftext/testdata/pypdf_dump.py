"""Print, as JSON, what kb/ingest.py's pdf-v1 extractor gets from pypdf.

Usage: python -I testdata/pypdf_dump.py FILE.pdf...
Output: a JSON list with one object per file:
  {"file": ..., "pages": [...], "title": str or null, "error": str or null,
   "pypdf": version}
The user site-packages directory is added explicitly because -I hides it
(pypdf is commonly installed with pip --user).
"""

import json
import logging
import site
import sys

try:
    import pypdf
except ImportError:
    sys.path.append(site.getusersitepackages())
    try:
        import pypdf
    except ImportError:
        print(json.dumps({"error": "pypdf not importable"}))
        sys.exit(3)

logging.disable(logging.CRITICAL)


def dump(path):
    res = {"file": path, "pages": None, "title": None, "error": None, "pypdf": pypdf.__version__}
    try:
        pages = []
        title = None
        reader = pypdf.PdfReader(path)
        try:
            title = (reader.metadata or {}).get("/Title") or None
        except Exception:
            title = None
        for pg in reader.pages:
            pages.append(pg.extract_text() or "")
        res["pages"] = pages
        res["title"] = str(title) if title else None
    except Exception as e:  # the CLI would crash here
        res["error"] = "%s: %s" % (type(e).__name__, e)
    return res


def run(paths):
    return [dump(p) for p in paths]


def main():
    # main -> run -> dump puts pypdf at the same Python stack depth as in
    # kb.py (main -> ingest.run -> extract_pdf), so RecursionError strikes
    # at the same nesting levels (see frames.go).
    print(json.dumps(run(sys.argv[1:])))


main()
