"""Generate the PDF corpus in testdata/corpus/ (Python stdlib only).

Every file is assembled by hand so each one exercises a specific part of
pypdf's text extraction: fonts/encodings, text positioning, file structure.
Run from internal/pdftext:   python testdata/gen_corpus.py
"""

import base64
import binascii
import os
import zlib

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "corpus")


def pdfstr_utf16(s: str) -> bytes:
    return b"<" + binascii.hexlify(b"\xfe\xff" + s.encode("utf-16-be")).upper() + b">"


def lzw_encode(data: bytes) -> bytes:
    """LZW as PDF uses it (9-12 bit codes, EarlyChange 1)."""
    codes = [256]
    table = {bytes([i]): i for i in range(256)}
    nxt = 258
    width = 9
    widths = [9]
    w = b""
    for c in data:
        wc = w + bytes([c])
        if wc in table:
            w = wc
            continue
        codes.append(table[w])
        widths.append(width)
        table[wc] = nxt
        nxt += 1
        if nxt + 1 > (1 << width) and width < 12:
            width += 1
        if nxt >= 4094:
            codes.append(256)
            widths.append(width)
            table = {bytes([i]): i for i in range(256)}
            nxt = 258
            width = 9
        w = bytes([c])
    if w:
        codes.append(table[w])
        widths.append(width)
    codes.append(257)
    widths.append(width)
    bits = 0
    nbits = 0
    out = bytearray()
    for code, wd in zip(codes, widths):
        bits = (bits << wd) | code
        nbits += wd
        while nbits >= 8:
            nbits -= 8
            out.append((bits >> nbits) & 0xFF)
    if nbits:
        out.append((bits << (8 - nbits)) & 0xFF)
    return bytes(out)


def runlength_encode(data: bytes) -> bytes:
    out = bytearray()
    i = 0
    while i < len(data):
        j = i
        while j < len(data) and j - i < 128 and data[j] == data[i]:
            j += 1
        if j - i >= 3:
            out += bytes([257 - (j - i), data[i]])
            i = j
            continue
        j = i
        while j < len(data) and j - i < 128 and not (j + 2 < len(data) and data[j] == data[j + 1] == data[j + 2]):
            j += 1
        out += bytes([j - i - 1]) + data[i:j]
        i = j
    return bytes(out) + b"\x80"


class PDF:
    def __init__(self):
        self.objs = {}  # num -> body bytes (without "obj"/"endobj")
        self.n = 0

    def reserve(self) -> int:
        self.n += 1
        return self.n

    def add(self, body, num=None) -> int:
        if num is None:
            num = self.reserve()
        if isinstance(body, str):
            body = body.encode("latin-1")
        self.objs[num] = body
        return num

    def stream(self, data: bytes, extra: str = "", filters=(), num=None, raw_length=None) -> int:
        enc = data
        names = []
        for f in filters:
            if f == "Fl":
                enc = zlib.compress(enc)
                names.append("/FlateDecode")
            elif f == "AHx":
                enc = binascii.hexlify(enc) + b">"
                names.append("/ASCIIHexDecode")
            elif f == "A85":
                enc = base64.a85encode(enc, adobe=True)[2:]
                names.append("/ASCII85Decode")
            elif f == "LZW":
                enc = lzw_encode(enc)
                names.append("/LZWDecode")
            elif f == "RL":
                enc = runlength_encode(enc)
                names.append("/RunLengthDecode")
        names.reverse()
        filt = ""
        if len(names) == 1:
            filt = " /Filter " + names[0]
        elif names:
            filt = " /Filter [" + " ".join(names) + "]"
        length = len(enc) if raw_length is None else raw_length
        body = ("<< /Length %d%s %s>>\nstream\n" % (length, filt, extra)).encode("latin-1") + enc + b"\nendstream"
        return self.add(body, num)

    def write(self, path, root, info=None, mode="table", extra_trailer="", eol=b"\n"):
        out = bytearray(b"%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
        offsets = {}
        if mode == "objstm":
            return self._write_objstm(path, root, info, out)
        for num in sorted(self.objs):
            offsets[num] = len(out)
            out += b"%d 0 obj\n" % num + self.objs[num] + b"\nendobj\n"
        size = max(self.objs) + 1
        xref_at = len(out)
        out += b"xref\n0 %d\n" % size
        out += b"0000000000 65535 f" + (b" \n" if eol == b"\n" else b"\r\n")
        for num in range(1, size):
            if num in offsets:
                out += b"%010d 00000 n" % offsets[num] + (b" \n" if eol == b"\n" else b"\r\n")
            else:
                out += b"0000000000 65535 f" + (b" \n" if eol == b"\n" else b"\r\n")
        tr = "/Size %d /Root %d 0 R" % (size, root)
        if info:
            tr += " /Info %d 0 R" % info
        tr += extra_trailer
        out += b"trailer\n<< " + tr.encode("latin-1") + b" >>\nstartxref\n%d\n%%%%EOF\n" % xref_at
        with open(path, "wb") as fh:
            fh.write(bytes(out))
        return bytes(out)

    def _write_objstm(self, path, root, info, out):
        # Streams stay top-level; everything else goes in one object stream.
        in_stm = [n for n in sorted(self.objs) if b"\nstream\n" not in self.objs[n]]
        top = [n for n in sorted(self.objs) if n not in in_stm]
        stm_num = max(self.objs) + 1
        xref_num = stm_num + 1
        header = []
        body = bytearray()
        for n in in_stm:
            header.append("%d %d" % (n, len(body)))
            body += self.objs[n] + b"\n"
        head = (" ".join(header) + "\n").encode()
        data = head + bytes(body)
        comp = zlib.compress(data)
        offsets = {}
        for n in top:
            offsets[n] = len(out)
            out += b"%d 0 obj\n" % n + self.objs[n] + b"\nendobj\n"
        offsets[stm_num] = len(out)
        out += (b"%d 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d /Filter /FlateDecode >>\nstream\n"
                % (stm_num, len(in_stm), len(head), len(comp))) + comp + b"\nendstream\nendobj\n"
        xref_at = len(out)
        size = xref_num + 1
        rows = bytearray()
        for num in range(size):
            if num == 0:
                rows += bytes([0]) + (0).to_bytes(4, "big") + (65535).to_bytes(2, "big")
            elif num in in_stm:
                rows += bytes([2]) + stm_num.to_bytes(4, "big") + in_stm.index(num).to_bytes(2, "big")
            elif num in offsets:
                rows += bytes([1]) + offsets[num].to_bytes(4, "big") + (0).to_bytes(2, "big")
            elif num == xref_num:
                rows += bytes([1]) + xref_at.to_bytes(4, "big") + (0).to_bytes(2, "big")
            else:
                rows += bytes([0]) + (0).to_bytes(4, "big") + (0).to_bytes(2, "big")
        # PNG Up predictor, as many producers write it
        cols = 7
        pred = bytearray()
        prev = bytes(cols)
        for i in range(0, len(rows), cols):
            row = rows[i:i + cols]
            pred += b"\x02" + bytes((row[j] - prev[j]) % 256 for j in range(cols))
            prev = row
        comp = zlib.compress(bytes(pred))
        tr = "/Type /XRef /Size %d /W [1 4 2] /Root %d 0 R" % (size, root)
        if info:
            tr += " /Info %d 0 R" % info
        tr += " /DecodeParms << /Predictor 12 /Columns 7 >> /Filter /FlateDecode /Length %d" % len(comp)
        out += b"%d 0 obj\n<< " % xref_num + tr.encode() + b" >>\nstream\n" + comp + b"\nendstream\nendobj\n"
        out += b"startxref\n%d\n%%%%EOF\n" % xref_at
        with open(path, "wb") as fh:
            fh.write(bytes(out))
        return bytes(out)


def pages_tree(pdf, page_nums, pages_num, extra=""):
    kids = " ".join("%d 0 R" % n for n in page_nums)
    pdf.add("<< /Type /Pages /Kids [%s] /Count %d %s>>" % (kids, len(page_nums), extra), pages_num)


def catalog(pdf, pages_num):
    return pdf.add("<< /Type /Catalog /Pages %d 0 R >>" % pages_num)


def simple_doc(name, contents, fonts, title=None, filters=(), mode="table", resources_extra="",
               info_extra="", page_extra="", eol=b"\n"):
    """contents: list of bytes (one per page) or None for a page without /Contents."""
    pdf = PDF()
    pages_num = pdf.reserve()
    font_entries = []
    for res_name, body in fonts.items():
        fnum = pdf.add(body) if not isinstance(body, int) else body
        font_entries.append("/%s %d 0 R" % (res_name, fnum))
    res = pdf.add("<< /Font << %s >> %s>>" % (" ".join(font_entries), resources_extra))
    page_nums = []
    for c in contents:
        if c is None:
            page_nums.append(pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R %s>>"
                                     % (pages_num, res, page_extra)))
            continue
        cnum = pdf.stream(c, filters=filters)
        page_nums.append(pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R /Contents %d 0 R %s>>"
                                 % (pages_num, res, cnum, page_extra)))
    pages_tree(pdf, page_nums, pages_num)
    root = catalog(pdf, pages_num)
    info = None
    if title is not None or info_extra:
        t = b"" if title is None else b"/Title " + title
        info = pdf.add(b"<< " + t + b" /Producer (gen_corpus.py) " + info_extra.encode("latin-1") + b">>")
    pdf.write(os.path.join(OUT, name), root, info, mode=mode, eol=eol)


HELV = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"
TIMES = "<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman >>"
COURIER = "<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>"
# A TrueType font with explicit widths and a (fake, absent) font program.
WIDTHS_ARIAL = ("<< /Type /Font /Subtype /TrueType /BaseFont /ArialMT /FirstChar 32 /LastChar 126 /Encoding /WinAnsiEncoding "
                "/Widths [278 278 355 556 556 889 667 191 333 333 389 584 278 333 278 278 556 556 556 556 556 556 556 556 556 556 "
                "278 278 584 584 584 556 1015 667 667 722 722 667 611 778 722 278 500 667 556 833 722 778 667 778 722 667 611 722 "
                "667 944 667 667 611 278 278 278 469 556 333 556 556 500 556 556 278 556 556 222 222 500 222 833 556 556 556 556 "
                "333 500 278 556 500 722 500 500 500 334 260 334 584] "
                "/FontDescriptor << /Type /FontDescriptor /FontName /ArialMT /Flags 32 /FontBBox [-665 -325 2000 1040] "
                "/ItalicAngle 0 /Ascent 905 /Descent -212 /CapHeight 716 /StemV 80 >> >>")


def bt(*lines):
    return ("BT\n" + "\n".join(lines) + "\nET\n").encode("latin-1")


def gen_all():
    os.makedirs(OUT, exist_ok=True)

    # 1. plain lines with Td / TL / T* / TD, title in PDFDocEncoding
    simple_doc("plain_lines.pdf", [bt(
        "/F1 12 Tf", "72 720 Td", "(The quick brown fox) Tj",
        "0 -14 Td", "(jumps over the lazy dog.) Tj",
        "14 TL", "T*", "(Third line via T-star.) Tj",
        "0 -28 TD", "(Fourth line via TD.) Tj", "T*", "(Fifth line, TD leading.) Tj",
    )], {"F1": HELV}, title=b"(Plain lines \\351t\\351)")

    # 2. several pages, including an empty one and one without /Contents
    simple_doc("multipage.pdf", [
        bt("/F1 11 Tf", "72 720 Td", "(Page one, first line) Tj", "0 -13 Td", "(Page one, second line) Tj"),
        b"",
        None,
        bt("/F1 11 Tf", "72 720 Td", "(Page four after two empty pages) Tj"),
    ], {"F1": TIMES}, title=b"(Multi page)")

    # 3. kerned TJ arrays with small and large offsets
    simple_doc("tj_kerning.pdf", [bt(
        "/F1 12 Tf", "72 720 Td",
        "[(W) 120 (orld) -20 (wide) -250 (web) -300 (x) -1000 (y) 50 (z)] TJ",
        "0 -20 Td",
        "[(A) -110 (V) -80 (A) -120.5 (T) 400 (a) 1200 (b)] TJ",
        "0 -20 Td",
        "[(no) -90 (space) -95 (here) -96 (maybe)] TJ",
        "0 -20 Td", "[(trailing space ) -500 (next)] TJ",
        "0 -20 Td", "[] TJ [(after empty)] TJ",
    )], {"F1": HELV}, title=None)

    # 4. word spacing, Tc, Tz, and the ' and " operators
    simple_doc("word_spacing.pdf", [bt(
        "/F1 12 Tf", "14 TL", "72 720 Td",
        "5 Tw (Words with word spacing) Tj",
        "0 Tw 2 Tc (Char spacing applied) '",
        "0 Tc 150 Tz (Horizontal scaling) '",
        "100 Tz 3 1 (Double quote operator) \"",
        "(Single quote operator) '",
        "() ' ( ) '",
    )], {"F1": WIDTHS_ARIAL})

    # 5. Tm on the same line and new lines, scaled matrices
    simple_doc("tm_positioning.pdf", [bt(
        "/F1 12 Tf",
        "1 0 0 1 72 700 Tm (Left) Tj",
        "1 0 0 1 120 700 Tm (gap of a few points) Tj",
        "1 0 0 1 230.3 700 Tm (adjacent) Tj",
        "1 0 0 1 280 700.5 Tm (tiny rise) Tj",
        "1 0 0 1 72 680 Tm (Next line by Tm) Tj",
        "/F1 1 Tf 12 0 0 12 72 650 Tm (Scaled font via matrix) Tj",
        "12 0 0 12 300 650 Tm (same line far right) Tj",
        "12 0 0 12 72 630 Tm (and below) Tj",
        "/F1 10 Tf 1 0 0 1 400 600 Tm (backwards) Tj 1 0 0 1 72 600 Tm (moved left) Tj",
    )], {"F1": HELV})

    # 6. Differences encoding (and a glyph name that is not in the AGL)
    diff_font = ("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Type /Encoding /BaseEncoding /WinAnsiEncoding "
                 "/Differences [65 /B /C 97 /eacute /ccedilla 1 /space /fi /fl /odd.glyph /uni263A] >> >>")
    simple_doc("differences.pdf", [bt(
        "/F1 12 Tf", "72 720 Td", "(ABCabc) Tj",
        "0 -14 Td", "(word\\001word\\002\\003 \\004\\005) Tj",
        "0 -14 Td", "(Plain text stays) Tj",
    )], {"F1": diff_font}, title=b"(Differences)")

    # 7. Type0 / Identity-H font with a ToUnicode CMap
    pdf = PDF()
    pages_num = pdf.reserve()
    cmap = b"""/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
3 beginbfchar
<0003> <0020>
<0101> <00E9>
<0200> <D83DDE00>
endbfchar
3 beginbfrange
<0024> <003D> <0041>
<0044> <005D> <0061>
<0201> <0202> [<00660069> <00660066>]
endbfrange
2 beginbfchar
<0300> <05D0>
<0301> <05D1>
endbfchar
endcmap
CMapName currentdict /CMap defineresource pop
end
end
"""
    tu = pdf.stream(cmap, filters=("Fl",))
    desc = pdf.add("<< /Type /FontDescriptor /FontName /FakeCID /Flags 4 /FontBBox [0 -200 1000 900] /ItalicAngle 0 "
                   "/Ascent 900 /Descent -200 /CapHeight 700 /StemV 80 >>")
    cid = pdf.add("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /FakeCID /CIDSystemInfo << /Registry (Adobe) "
                  "/Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW 1000 "
                  "/W [3 [250] 36 61 600 68 [500 520 480 510 500 300 500 510 230 230 480 230 780 510 510 510 510 340 400 300 510 450 700 450 450 420]] >>" % desc)
    f0 = pdf.add("<< /Type /Font /Subtype /Type0 /BaseFont /FakeCID /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>" % (cid, tu))
    res = pdf.add("<< /Font << /F0 %d 0 R /F1 %d 0 R >> >>" % (f0, pdf.add(HELV)))

    def gids(s):
        out = []
        for ch in s:
            if ch == " ":
                out.append(3)
            elif "A" <= ch <= "Z":
                out.append(0x24 + ord(ch) - 65)
            elif "a" <= ch <= "z":
                out.append(0x44 + ord(ch) - 97)
        return "<" + "".join("%04X" % g for g in out) + ">"
    c = bt("/F0 14 Tf", "72 720 Td", gids("Hello World") + " Tj",
           "0 -20 Td", "[" + gids("Kerned") + " -400 " + gids("Text") + "] TJ",
           "0 -20 Td", "<0101 0200 0201 0202> Tj",
           "0 -20 Td", gids("Hebrew next") + " Tj <03000301> Tj " + gids("after") + " Tj",
           "0 -20 Td", "<00240025002> Tj",
           "/F1 12 Tf 0 -20 Td (Back to Helvetica) Tj")
    cn = pdf.stream(c, filters=("Fl",))
    p1 = pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R /Contents %d 0 R >>" % (pages_num, res, cn))
    pages_tree(pdf, [p1], pages_num)
    root = catalog(pdf, pages_num)
    info = pdf.add(b"<< /Title " + pdfstr_utf16("Unicode title: café – 日本語 \U0001F600") + b" >>")
    pdf.write(os.path.join(OUT, "type0_identity.pdf"), root, info)

    # 8. Flate-compressed content and a chain of filters
    simple_doc("flate.pdf", [
        bt("/F1 12 Tf", "72 720 Td", "(Compressed with FlateDecode) Tj", "0 -14 Td", "(second line) Tj"),
    ], {"F1": HELV}, filters=("Fl",), title=b"<466C61746520746974 6C65>")
    simple_doc("filter_chain.pdf", [
        bt("/F1 12 Tf", "72 720 Td", "(ASCII85 over Flate) Tj"),
    ], {"F1": HELV}, filters=("Fl", "A85"), title=b"(Chain)")
    simple_doc("ascii_hex.pdf", [
        bt("/F1 12 Tf", "72 720 Td", "(ASCIIHex encoded content) Tj"),
    ], {"F1": HELV}, filters=("AHx",))

    # 9. xref stream + object stream (PDF 1.5 style), UTF-16BE title
    simple_doc("xref_stream.pdf", [
        bt("/F1 12 Tf", "72 720 Td", "(Objects live in an object stream) Tj", "0 -14 Td", "(and the xref is a stream) Tj"),
        bt("/F1 12 Tf", "72 720 Td", "(Second page) Tj"),
    ], {"F1": HELV}, filters=("Fl",), mode="objstm", title=pdfstr_utf16("Über été — Жизнь"))

    # 10. WinAnsi accented letters, quotes, euro, octal escapes
    simple_doc("winansi.pdf", [bt(
        "/F1 12 Tf", "72 720 Td",
        "(Caf\\351 cr\\350me br\\373l\\351e, na\\357ve \\374ber) Tj",
        "0 -14 Td", "(\\223Smart quotes\\224 and \\221single\\222 \\226 dash \\227 em) Tj",
        "0 -14 Td", "(Euro \\200 pound \\243 yen \\245 section \\247 degree \\260) Tj",
        "0 -14 Td", "(\\(parens\\) back\\\\slash \\101\\102C tab\\there line\\\ncontinued) Tj",
        "0 -14 Td", "<48657820737472696E67> Tj",
        "0 -14 Td", "(\\0101 short octal) Tj",
    )], {"F1": HELV}, title=b"(Accents \\351\\350 \\200)")

    # 11. no title (no /Info at all), standard fonts without /Encoding
    simple_doc("no_title.pdf", [bt(
        "/F1 12 Tf", "72 720 Td", "(Times without encoding entry) Tj",
        "/F2 10 Tf", "0 -14 Td", "(Courier fixed pitch    spaces) Tj",
        "0 -14 Td", "(\\351 latin1 via charmap) Tj",
    )], {"F1": TIMES, "F2": COURIER})

    # 12. empty title, title with only spaces, indirect title
    simple_doc("empty_title.pdf", [bt("/F1 12 Tf", "72 720 Td", "(Empty title string) Tj")], {"F1": HELV}, title=b"()")
    simple_doc("space_title.pdf", [bt("/F1 12 Tf", "72 720 Td", "(Spaces title) Tj")], {"F1": HELV}, title=b"(  Padded Title  )")

    # 13. form XObject, image XObject, q/Q/cm, rotation
    pdf = PDF()
    pages_num = pdf.reserve()
    helv = pdf.add(HELV)
    form = pdf.stream(bt("/F1 10 Tf", "0 0 Td", "(Text inside a form XObject) Tj", "0 -12 Td", "(second form line) Tj"),
                      extra="/Type /XObject /Subtype /Form /BBox [0 0 300 100] /Resources << /Font << /F1 %d 0 R >> >> " % helv)
    form_nores = pdf.stream(bt("/F1 10 Tf", "(Form without resources) Tj"),
                            extra="/Type /XObject /Subtype /Form /BBox [0 0 300 100] ")
    img = pdf.stream(b"\x00\xff\x00\xff", extra="/Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 8 ")
    res = pdf.add("<< /Font << /F1 %d 0 R >> /XObject << /Fm1 %d 0 R /Fm2 %d 0 R /Im1 %d 0 R >> >>" % (helv, form, form_nores, img))
    c = (bt("/F1 12 Tf", "72 720 Td", "(Before the form) Tj")
         + b"q 1 0 0 1 72 650 cm /Fm1 Do Q\n"
         + bt("/F1 12 Tf", "72 600 Td", "(Between) Tj")
         + b"q 20 0 0 20 72 500 cm /Im1 Do Q\n"
         + b"BT /F1 12 Tf 72 480 Td (Text then image ) Tj ET q 10 0 0 10 200 480 cm /Im1 Do Q\n"
         + b"/Fm2 Do\n"
         + b"q 0 1 -1 0 300 300 cm BT /F1 12 Tf 0 0 Td (Rotated ninety) Tj 0 -14 Td (second rotated) Tj ET Q\n"
         + b"q 2 0 0 2 0 0 cm BT /F1 6 Tf 36 100 Td (Scaled by cm) Tj 0 -7 Td (next scaled line) Tj ET Q\n"
         + bt("/F1 12 Tf", "72 60 Td", "(After everything) Tj"))
    cn = pdf.stream(c)
    p1 = pdf.add("<< /Type /Page /Parent %d 0 R /Resources %d 0 R /Contents %d 0 R >>" % (pages_num, res, cn))
    pages_tree(pdf, [p1], pages_num, extra="/MediaBox [0 0 612 792] ")
    root = catalog(pdf, pages_num)
    title = pdf.add("(Indirect title object)")
    info = pdf.add("<< /Title %d 0 R >>" % title)
    pdf.write(os.path.join(OUT, "xobjects.pdf"), root, info)

    # 14. contents array, inherited resources, nested page tree, rotate
    pdf = PDF()
    pages_num = pdf.reserve()
    inner = pdf.reserve()
    helv = pdf.add(HELV)
    res = pdf.add("<< /Font << /F1 %d 0 R >> >>" % helv)
    s1 = pdf.stream(b"BT /F1 12 Tf 72 720 Td (First part of)")
    s2 = pdf.stream(b"Tj ( split across streams) Tj ET", filters=("Fl",))
    s3 = pdf.stream(b"BT /F1 12 Tf 72 700 Td (third stream) Tj ET")
    p1 = pdf.add("<< /Type /Page /Parent %d 0 R /Contents [%d 0 R %d 0 R %d 0 R] >>" % (inner, s1, s2, s3))
    p2c = pdf.stream(bt("/F1 12 Tf", "72 720 Td", "(Rotated page, inherited resources) Tj"))
    p2 = pdf.add("<< /Type /Page /Parent %d 0 R /Contents %d 0 R /Rotate 90 >>" % (inner, p2c))
    p3c = pdf.stream(bt("/F1 12 Tf", "72 720 Td", "(Third page directly under root) Tj"))
    p3 = pdf.add("<< /Type /Page /Parent %d 0 R /Contents %d 0 R >>" % (pages_num, p3c))
    pdf.add("<< /Type /Pages /Parent %d 0 R /Kids [%d 0 R %d 0 R] /Count 2 >>" % (pages_num, p1, p2), inner)
    pdf.add("<< /Type /Pages /Kids [%d 0 R %d 0 R] /Count 3 /Resources %d 0 R /MediaBox [0 0 612 792] >>" % (inner, p3, res), pages_num)
    root = catalog(pdf, pages_num)
    pdf.write(os.path.join(OUT, "page_tree.pdf"), root, None)

    # 15. simple font with ToUnicode CMap, unknown font, font without metrics
    pdf = PDF()
    pages_num = pdf.reserve()
    cmap1 = b"""/CIDInit /ProcSet findresource begin 12 dict begin begincmap
/CMapName /Custom def 1 begincodespacerange <00> <FF> endcodespacerange
2 beginbfchar <21> <0058> <22> <0059> endbfchar
1 beginbfrange <41> <43> <0061> endbfrange
endcmap CMapName currentdict /CMap defineresource pop end end"""
    tu = pdf.stream(cmap1)
    f1 = pdf.add("<< /Type /Font /Subtype /TrueType /BaseFont /CustomTT /FirstChar 32 /LastChar 90 /Widths [%s] /ToUnicode %d 0 R >>"
                 % (" ".join(["500"] * 59), tu))
    f2 = pdf.add("<< /Type /Font /Subtype /Type1 /BaseFont /NotAStandardFont /Encoding /MacRomanEncoding >>")
    res = pdf.add("<< /Font << /F1 %d 0 R /F2 %d 0 R >> >>" % (f1, f2))
    c = bt("/F1 12 Tf", "72 720 Td", "(ABC!\"DEF) Tj",
           "/F2 12 Tf", "0 -14 Td", "(Mac Roman \\216\\217 text) Tj",
           "/F9 12 Tf", "0 -14 Td", "(Unknown font resource) Tj",
           "/F2 12 Tf", "0 -14 Td", "(Back to known) Tj")
    cn = pdf.stream(c)
    p1 = pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R /Contents %d 0 R >>" % (pages_num, res, cn))
    pages_tree(pdf, [p1], pages_num)
    root = catalog(pdf, pages_num)
    pdf.write(os.path.join(OUT, "tounicode_simple.pdf"), root, None)

    # 16. incremental update: page content and title replaced
    pdf = PDF()
    pages_num = pdf.reserve()
    helv = pdf.add(HELV)
    res = pdf.add("<< /Font << /F1 %d 0 R >> >>" % helv)
    cn = pdf.stream(bt("/F1 12 Tf", "72 720 Td", "(Original text) Tj"))
    p1 = pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R /Contents %d 0 R >>" % (pages_num, res, cn))
    pages_tree(pdf, [p1], pages_num)
    root = catalog(pdf, pages_num)
    info = pdf.add("<< /Title (Original title) >>")
    base = pdf.write(os.path.join(OUT, "incremental.pdf"), root, info)
    prev_xref = int(base.rsplit(b"startxref\n", 1)[1].split(b"\n")[0])
    out = bytearray(base)
    new_c = bt("/F1 12 Tf", "72 720 Td", "(Updated text after incremental save) Tj")
    off_c = len(out)
    out += b"%d 0 obj\n<< /Length %d >>\nstream\n" % (cn, len(new_c)) + new_c + b"\nendstream\nendobj\n"
    off_i = len(out)
    out += b"%d 0 obj\n<< /Title (Updated title) >>\nendobj\n" % info
    xref_at = len(out)
    out += b"xref\n0 1\n0000000000 65535 f \n%d 1\n%010d 00000 n \n%d 1\n%010d 00000 n \n" % (cn, off_c, info, off_i)
    out += b"trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n" % (
        info + 1, root, info, prev_xref, xref_at)
    with open(os.path.join(OUT, "incremental.pdf"), "wb") as fh:
        fh.write(bytes(out))

    # 17. CRLF xref rows and shifted offsets (pypdf repairs them by search)
    simple_doc("crlf_xref.pdf", [bt("/F1 12 Tf", "72 720 Td", "(CRLF xref entries) Tj")], {"F1": HELV},
               title=b"(CRLF)", eol=b"\r\n")
    path = os.path.join(OUT, "crlf_xref.pdf")
    data = open(path, "rb").read()
    old_sx = int(data.rsplit(b"startxref", 1)[1].split()[0])
    # Shift every object by inserting a comment after the header; the xref
    # rows now point a few bytes early and pypdf repairs them by searching.
    pad = b"% padding comment that moves every object\n"
    head_end = data.index(b"\n", data.index(b"\n") + 1) + 1
    data = data[:head_end] + pad + data[head_end:]
    data = data.replace(b"startxref\n%d\n" % old_sx, b"startxref\n%d\n" % (old_sx + len(pad)))
    with open(os.path.join(OUT, "shifted_offsets.pdf"), "wb") as fh:
        fh.write(data)

    # 18. Bidi / RTL text through a ToUnicode font and numbers
    # (covered in type0_identity), plus vertical moves of various sizes.
    simple_doc("line_moves.pdf", [bt(
        "/F1 10 Tf", "72 720 Td", "(Line A) Tj",
        "0 -7 Td (small drop) Tj",
        "0 -9 Td (larger drop) Tj",
        "0 20 Td (jump up) Tj",
        "50 0 Td (right) Tj",
        "3 0 Td (close) Tj",
        "/F1 20 Tf 0 -30 Td (Big font) Tj",
        "/F1 8 Tf 0 -10 Td (small font) Tj",
        "0 -0.5 Td (half point) Tj",
    ), bt("/F1 12 Tf 100 700 Td (second) Tj") + bt("/F1 12 Tf 100 700 Td (same place new BT) Tj")
      + bt("/F1 12 Tf 100 680 Td (lower BT) Tj")],
        {"F1": HELV})


def gen_more():
    # 19. LZW and RunLength encoded content, inline image between text
    simple_doc("lzw.pdf", [bt("/F1 12 Tf", "72 720 Td", "(LZW encoded content stream with some repeated repeated repeated words) Tj",
                              "0 -14 Td", "(" + "abcabcabc " * 60 + ") Tj")],
               {"F1": HELV}, filters=("LZW",))
    simple_doc("runlength.pdf", [bt("/F1 12 Tf", "72 720 Td", "(RunLength       encoded      text) Tj")],
               {"F1": HELV}, filters=("RL",))
    simple_doc("inline_image.pdf", [
        bt("/F1 12 Tf", "72 720 Td", "(Text before the inline image) Tj")
        + b"q 10 0 0 10 72 690 cm BI /W 4 /H 1 /BPC 8 /CS /G ID \x00EI\xffE EI Q\n"
        + bt("/F1 12 Tf", "72 670 Td", "(Text after the inline image) Tj")
    ], {"F1": HELV})

    # 20. Type3 font with CharProcs and a Differences encoding
    pdf = PDF()
    pages_num = pdf.reserve()
    procs = {}
    for name in ["A", "B", "space", "eacute", "custom1"]:
        procs[name] = pdf.stream(b"500 0 0 0 400 600 d1 0 0 400 600 re f")
    t3 = pdf.add("<< /Type /Font /Subtype /Type3 /FontBBox [0 0 500 700] /FontMatrix [0.001 0 0 0.001 0 0] "
                 "/CharProcs << %s >> /Encoding << /Type /Encoding /Differences [65 /A /B 32 /space 200 /eacute /custom1] >> "
                 "/FirstChar 32 /LastChar 201 /Widths [%s] >>"
                 % (" ".join("/%s %d 0 R" % (k, v) for k, v in procs.items()), " ".join(["500"] * 170)))
    res = pdf.add("<< /Font << /T3 %d 0 R >> >>" % t3)
    cn = pdf.stream(bt("/T3 12 Tf", "72 720 Td", "(AB BA \310\311) Tj", "0 -14 Td", "[(A) -600 (B)] TJ"))
    p1 = pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R /Contents %d 0 R >>" % (pages_num, res, cn))
    pages_tree(pdf, [p1], pages_num)
    root = catalog(pdf, pages_num)
    info = pdf.add(b"<< /Title <FFFE4C0069007400740006016501200065006E006400690061006E00> >>")
    pdf.write(os.path.join(OUT, "type3.pdf"), root, info)

    # 21. titles that are not PDFDocEncoding text (pypdf keeps bytes and
    # str() decodes them as UTF-16, UTF-8 or Latin-1)
    simple_doc("title_bytes_even.pdf", [bt("/F1 12 Tf", "72 720 Td", "(Even length byte title) Tj")], {"F1": HELV},
               title=b"(Bad\237byte)")
    simple_doc("title_bytes_odd.pdf", [bt("/F1 12 Tf", "72 720 Td", "(Odd length byte title) Tj")], {"F1": HELV},
               title=b"(Bad \237 bytes)")
    simple_doc("title_bytes_gbk.pdf", [bt("/F1 12 Tf", "72 720 Td", "(GBK bytes in the title) Tj")], {"F1": HELV},
               title=b"(\237\241x)")
    simple_doc("title_utf8.pdf", [bt("/F1 12 Tf", "72 720 Td", "(UTF-8 bytes in the title) Tj")], {"F1": HELV},
               title=b"(Caf\303\251\177!)")
    simple_doc("title_name.pdf", [bt("/F1 12 Tf", "72 720 Td", "(Title is a name object) Tj")], {"F1": HELV},
               title=b"/Some#20Name")

    # 22. upside-down and vertical text, text after Q restores a font
    simple_doc("orientations.pdf", [bt(
        "/F1 12 Tf", "1 0 0 1 72 720 Tm (Upright) Tj",
        "-1 0 0 -1 300 600 Tm (Upside down) Tj (more) Tj",
        "0 1 -1 0 400 400 Tm (Vertical up) Tj",
        "0 -1 1 0 450 400 Tm (Vertical down) Tj",
        "1 0 0 1 72 300 Tm (Upright again) Tj",
    ) + b"q BT /F2 10 Tf 72 200 Td (Courier inside q) Tj ET Q BT 72 180 Td (after Q) Tj ET\n"],
        {"F1": HELV, "F2": COURIER})


def gen_hybrid():
    """A hybrid-reference file: classic xref table whose trailer points with
    /XRefStm to an xref stream that locates objects in an object stream."""
    out = bytearray(b"%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
    offs = {}

    def obj(num, body):
        offs[num] = len(out)
        out.extend(b"%d 0 obj\n" % num + body + b"\nendobj\n")

    content = bt("/F1 12 Tf", "72 720 Td", "(Hybrid reference file) Tj", "0 -14 Td", "(font found via XRefStm) Tj")
    obj(1, b"<< /Type /Pages /Kids [2 0 R] /Count 1 >>")
    obj(2, b"<< /Type /Page /Parent 1 0 R /MediaBox [0 0 612 792] /Resources 4 0 R /Contents 5 0 R >>")
    obj(5, b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream")
    obj(6, b"<< /Type /Catalog /Pages 1 0 R >>")
    o3 = HELV.encode()
    o4 = b"<< /Font << /F1 3 0 R >> >>"
    head = b"3 0 4 %d\n" % (len(o3) + 1)
    data = head + o3 + b"\n" + o4 + b"\n"
    comp = zlib.compress(data)
    obj(7, b"<< /Type /ObjStm /N 2 /First %d /Length %d /Filter /FlateDecode >>\nstream\n" % (len(head), len(comp))
        + comp + b"\nendstream")
    rows = b""
    for num in range(9):
        if num in (3, 4):
            rows += bytes([2]) + (7).to_bytes(2, "big") + bytes([num - 3])
        else:
            rows += bytes([0, 0, 0, 0])
    obj(8, b"<< /Type /XRef /Size 9 /W [1 2 1] /Length %d >>\nstream\n" % len(rows) + rows + b"\nendstream")
    xref_at = len(out)
    out += b"xref\n0 9\n0000000000 65535 f \n"
    for num in range(1, 9):
        if num in offs:
            out += b"%010d 00000 n \n" % offs[num]
        else:
            out += b"0000000000 00001 f \n"
    out += b"trailer\n<< /Size 9 /Root 6 0 R /XRefStm %d >>\nstartxref\n%d\n%%%%EOF\n" % (offs[8], xref_at)
    with open(os.path.join(OUT, "hybrid_xref.pdf"), "wb") as fh:
        fh.write(bytes(out))


if __name__ == "__main__":
    gen_hybrid()
    gen_more()
    gen_all()
    print("wrote", sorted(os.listdir(OUT)))
