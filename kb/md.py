"""Tiny Markdown-to-HTML for the renderer (stdlib only). Covers what statements and turns use:
paragraphs, headings, bullet/numbered lists, fenced code, blockquotes, tables, inline code/bold/italic/links,
and S-#### / <session>#<uuid> references as links."""
from __future__ import annotations

import html
import re

STMT_REF = re.compile(r"\b(S-\d{4,})\b")
PTR_REF = re.compile(r"\b([0-9a-f]{8})[0-9a-f-]*#([0-9a-f]{8})[0-9a-f-]*\b")


def inline(s: str) -> str:
    s = html.escape(s, quote=False)
    s = re.sub(r"`([^`]+)`", r"<code>\1</code>", s)
    s = re.sub(r"\*\*([^*]+)\*\*", r"<b>\1</b>", s)
    s = re.sub(r"(?<![\w*])\*([^*\n]+)\*(?![\w*])", r"<i>\1</i>", s)
    s = re.sub(r"\[([^\]]+)\]\((https?://[^)\s]+)\)", r'<a href="\2">\1</a>', s)
    s = re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", r"\1", s)  # relative links: show text only
    s = STMT_REF.sub(r'<a href="/s/\1">\1</a>', s)
    return s


def render_markdown(text: str) -> str:
    out: list[str] = []
    lines = text.replace("\r\n", "\n").split("\n")
    i = 0
    para: list[str] = []

    def flush():
        if para:
            out.append("<p>" + inline(" ".join(para)) + "</p>")
            para.clear()

    while i < len(lines):
        line = lines[i]
        if line.startswith("```"):
            flush()
            j = i + 1
            buf = []
            while j < len(lines) and not lines[j].startswith("```"):
                buf.append(lines[j]); j += 1
            out.append("<pre><code>" + html.escape("\n".join(buf)) + "</code></pre>")
            i = j + 1
            continue
        m = re.match(r"^(#{1,6})\s+(.*)$", line)
        if m:
            flush()
            lvl = min(len(m.group(1)) + 2, 6)
            out.append(f"<h{lvl}>{inline(m.group(2))}</h{lvl}>")
            i += 1
            continue
        if re.match(r"^\s*[-*]\s+", line) or re.match(r"^\s*\d+\.\s+", line):
            flush()
            ordered = bool(re.match(r"^\s*\d+\.", line))
            tag = "ol" if ordered else "ul"
            items = []
            while i < len(lines) and (re.match(r"^\s*[-*]\s+", lines[i]) or re.match(r"^\s*\d+\.\s+", lines[i]) or (lines[i].startswith("  ") and items)):
                l = lines[i]
                if re.match(r"^\s*([-*]|\d+\.)\s+", l):
                    items.append(re.sub(r"^\s*([-*]|\d+\.)\s+", "", l))
                else:
                    items[-1] += " " + l.strip()
                i += 1
            out.append(f"<{tag}>" + "".join(f"<li>{inline(x)}</li>" for x in items) + f"</{tag}>")
            continue
        if line.startswith(">"):
            flush()
            buf = []
            while i < len(lines) and lines[i].startswith(">"):
                buf.append(lines[i][1:].strip()); i += 1
            out.append("<blockquote>" + render_markdown("\n".join(buf)) + "</blockquote>")
            continue
        if line.startswith("|") and i + 1 < len(lines) and re.match(r"^\|[\s:|-]+\|$", lines[i + 1]):
            flush()
            hdr = [c.strip() for c in line.strip("|").split("|")]
            i += 2
            rows = []
            while i < len(lines) and lines[i].startswith("|"):
                rows.append([c.strip() for c in lines[i].strip("|").split("|")]); i += 1
            out.append("<table><tr>" + "".join(f"<th>{inline(h)}</th>" for h in hdr) + "</tr>"
                       + "".join("<tr>" + "".join(f"<td>{inline(c)}</td>" for c in r) + "</tr>" for r in rows) + "</table>")
            continue
        if not line.strip():
            flush()
            i += 1
            continue
        para.append(line.strip())
        i += 1
    flush()
    return "".join(out)
