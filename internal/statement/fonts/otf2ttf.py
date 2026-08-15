# CFF -> glyf. Every mainstream Go PDF library (gopdf, fpdf, maroto) embeds
# TrueType outlines only, and Noto Sans CJK ships as CFF, so the cubic curves
# have to be approximated by quadratics before the font is any use to us.
import sys
from fontTools.ttLib import TTFont, newTable
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.pens.cu2quPen import Cu2QuPen

MAX_ERR = 1.0  # in font units (upem 1000) — visually lossless at print sizes

src, dst = sys.argv[1], sys.argv[2]
f = TTFont(src)
upem = f["head"].unitsPerEm
glyphset = f.getGlyphSet()
order = f.getGlyphOrder()

glyf = newTable("glyf")
glyf.glyphOrder = order
glyf.glyphs = {}
for name in order:
    pen = TTGlyphPen(glyphset)
    glyphset[name].draw(Cu2QuPen(pen, MAX_ERR * upem / 1000.0))
    glyf.glyphs[name] = pen.glyph()
f["glyf"] = glyf

# maxp.recalc reads each glyph's bbox, which only exists after an explicit
# recalcBounds — a freshly penned glyph carries none.
for name in order:
    glyf.glyphs[name].recalcBounds(glyf)

# loca is derived from glyf on compile; maxp must become the TrueType flavour.
f["maxp"].tableVersion = 0x00010000
for attr, val in [
    ("maxPoints", 0), ("maxContours", 0), ("maxCompositePoints", 0),
    ("maxCompositeContours", 0), ("maxZones", 2), ("maxTwilightPoints", 0),
    ("maxStorage", 0), ("maxFunctionDefs", 0), ("maxInstructionDefs", 0),
    ("maxStackElements", 0), ("maxSizeOfInstructions", 0),
    ("maxComponentElements", 0), ("maxComponentDepth", 0),
]:
    setattr(f["maxp"], attr, val)
f["maxp"].recalc(f)
f["loca"] = newTable("loca")
f["head"].indexToLocFormat = 0
f["post"].formatType = 2.0
f["post"].extraNames = []
f["post"].mapping = {}
f["post"].glyphOrder = order

for t in ("CFF ", "VORG"):
    if t in f:
        del f[t]
f.sfntVersion = "\000\001\000\000"
f.save(dst)
print("saved", dst)
