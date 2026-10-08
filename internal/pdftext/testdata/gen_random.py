"""Generate random text-operator soups for differential testing.

Usage: python -I testdata/gen_random.py OUTDIR COUNT [SEED]
Each file has a few pages of randomly chosen text operators (Tf/Td/TD/Tm/
T*/TL/Tw/Tc/Tz/cm/q/Q/Tj/TJ/'/"/Do) over a mix of fonts, so the spacing
and newline decisions of pypdf's extractor get exercised with arbitrary
numbers. Compare with:  PDFTEXT_EXTRA=OUTDIR go test -run TestCorpusExtra
"""

import os
import random
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_corpus import HELV, TIMES, COURIER, WIDTHS_ARIAL, PDF, catalog, pages_tree  # noqa: E402

WORDS = ["alpha", "beta", "Gamma", "delta", "x", "", " ", "  ", "the", "of", "Text", "1234", "3.14",
         "caf\\351", "\\223q\\224", "a-b", "(p)", "\\\\", "end.", "W", "fi", "\\240nbsp", "tab\\t", "\\200uro"]


def num(r, lo, hi, frac=True):
    v = r.uniform(lo, hi)
    if not frac or r.random() < 0.4:
        return str(int(v))
    return ("%.3f" % v).rstrip("0").rstrip(".")


def text(r):
    return " ".join(r.choice(WORDS) for _ in range(r.randint(1, 4)))


def gids(r):
    return "<" + "".join("%04X" % r.choice([3, 3, 36, 37, 38, 68, 69, 70, 71, 0x101, 0x200, 0x201, 0x300, 999])
                         for _ in range(r.randint(0, 6))) + ">"


def op(r, fonts):
    k = r.random()
    if k < 0.12:
        f = r.choice(fonts)
        size = r.choice(["12", "10", "1", "0", "-12", num(r, 0.5, 30)])
        return "/%s %s Tf" % (f, size)
    if k < 0.25:
        return "%s %s Td" % (num(r, -60, 120), r.choice(["0", "0", num(r, -30, 30), num(r, -3, 3)]))
    if k < 0.29:
        return "%s %s TD" % (num(r, -20, 60), num(r, -20, 5))
    if k < 0.37:
        a = r.choice(["1", "12", "0.5", "-1", "0", num(r, -2, 14)])
        b = r.choice(["0", "0", "0", "1", "-1", num(r, -1, 1)])
        c = r.choice(["0", "0", "0", "-1", "1", num(r, -1, 1)])
        d = r.choice(["1", "12", "-1", "0", a, num(r, -2, 14)])
        return "%s %s %s %s %s %s Tm" % (a, b, c, d, num(r, 0, 500), num(r, 0, 800))
    if k < 0.41:
        return "T*"
    if k < 0.44:
        return "%s TL" % num(r, -5, 20)
    if k < 0.47:
        return "%s Tw" % num(r, -2, 10)
    if k < 0.49:
        return "%s Tc" % num(r, -1, 3)
    if k < 0.50:
        return "%s Tz" % num(r, 50, 150)
    if k < 0.53:
        return "q %s 0 0 %s %s %s cm" % (r.choice(["1", "2", "0.5", "-1"]), r.choice(["1", "2", "0.5", "-1"]),
                                         num(r, -50, 50), num(r, -50, 50))
    if k < 0.56:
        return "Q"
    if k < 0.58:
        return r.choice(["BT", "ET", "ET BT"])
    if k < 0.70:
        return "(%s) Tj" % text(r)
    if k < 0.73:
        return "%s Tj" % gids(r)
    if k < 0.86:
        parts = []
        for _ in range(r.randint(0, 6)):
            if r.random() < 0.5:
                parts.append("(%s)" % text(r))
            else:
                parts.append(r.choice(["-250", "-100", "100", "-1000", "1000", num(r, -1500, 1500), num(r, -200, 200)]))
        return "[%s] TJ" % " ".join(parts)
    if k < 0.90:
        return "(%s) '" % text(r)
    if k < 0.93:
        return "%s %s (%s) \"" % (num(r, 0, 5), num(r, 0, 2), text(r))
    if k < 0.96:
        return r.choice(["/Fm1 Do", "/Im1 Do", "/Fm2 Do"])
    return "% a comment line"


def make(path, r):
    pdf = PDF()
    pages_num = pdf.reserve()
    f = {}
    f["F1"] = pdf.add(HELV)
    f["F2"] = pdf.add(TIMES)
    f["F3"] = pdf.add(WIDTHS_ARIAL)
    f["F6"] = pdf.add(COURIER)
    f["F5"] = pdf.add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Differences [32 /space 97 /eacute /space] >> >>")
    tu = pdf.stream(b"""begincmap 1 begincodespacerange <0000> <FFFF> endcodespacerange
2 beginbfchar <0003> <0020> <0101> <00E9> endbfchar
3 beginbfrange <0024> <003D> <0041> <0044> <005D> <0061> <0200> <0201> [<D83DDE00> <00660069>] endbfrange
1 beginbfchar <0300> <05D0> endbfchar endcmap""")
    cid = pdf.add("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /R /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> "
                  "/DW 900 /W [3 [280] 36 [600 610 620] 68 70 500] /FontDescriptor << /Type /FontDescriptor /FontName /R /Flags 4 >> >>")
    f["F4"] = pdf.add("<< /Type /Font /Subtype /Type0 /BaseFont /R /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>" % (cid, tu))
    fonts_dict = " ".join("/%s %d 0 R" % (k, v) for k, v in f.items())
    form = pdf.stream(("BT /F1 9 Tf 5 5 Td (%s) Tj [(%s) -300 (%s)] TJ ET" % (text(r), text(r), text(r))).encode("latin-1"),
                      extra="/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources << /Font << %s >> >> " % fonts_dict)
    form2 = pdf.stream(b"BT /F1 9 Tf (no resources) Tj ET", extra="/Type /XObject /Subtype /Form /BBox [0 0 1 1] ")
    img = pdf.stream(b"\x00", extra="/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 ")
    res = pdf.add("<< /Font << %s >> /XObject << /Fm1 %d 0 R /Fm2 %d 0 R /Im1 %d 0 R >> >>" % (fonts_dict, form, form2, img))
    names = list(f) + ["F9"]
    page_nums = []
    for _ in range(r.randint(1, 3)):
        ops = ["BT", "/F1 12 Tf", "72 720 Td"]
        ops += [op(r, names) for _ in range(r.randint(5, 60))]
        ops.append("ET")
        cn = pdf.stream("\n".join(ops).encode("latin-1"), filters=("Fl",) if r.random() < 0.5 else ())
        page_nums.append(pdf.add("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources %d 0 R /Contents %d 0 R >>"
                                 % (pages_num, res, cn)))
    pages_tree(pdf, page_nums, pages_num)
    root = catalog(pdf, pages_num)
    pdf.write(path, root, None)


def main():
    out, count = sys.argv[1], int(sys.argv[2])
    seed = int(sys.argv[3]) if len(sys.argv) > 3 else 1
    os.makedirs(out, exist_ok=True)
    for i in range(count):
        r = random.Random(seed * 100003 + i)
        make(os.path.join(out, "rand_%05d.pdf" % i), r)


if __name__ == "__main__":
    main()
