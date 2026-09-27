#!/usr/bin/env python3
"""Convert ANSI terminal output to a PNG with a terminal window frame.

Usage: ansi2png.py INPUT.ans OUTPUT.png "window title"

The script converts SGR color codes to HTML, then takes a screenshot of the
HTML with headless Google Chrome. It uses only the Python standard library.
"""
import html
import os
import re
import subprocess
import sys
import tempfile
import unicodedata

CHROME = os.environ.get("CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
FONT_PX = 14
LINE_PX = 20
CH_PX = 8.43  # width of one cell of Menlo at 14 px
PAD = 22
BAR = 34
FG = "#E6E6E6"
BG = "#15181B"

SGR = re.compile(r"\x1b\[([0-9;:]*)m")
# Escape sequences other than SGR (which ends with "m").
OTHER = re.compile(r"\x1b\[[0-9;?]*[A-Za-ln-z]|\x1b\][^\x07]*\x07")


def wide(ch):
    return unicodedata.east_asian_width(ch) in ("W", "F")


def parse_sgr(params, st):
    codes = [int(p) if p else 0 for p in re.split("[;:]", params)] if params else [0]
    i = 0
    while i < len(codes):
        c = codes[i]
        if c == 0:
            st.clear()
        elif c == 1:
            st["bold"] = True
        elif c == 2:
            st["faint"] = True
        elif c == 7:
            st["reverse"] = True
        elif c in (22,):
            st.pop("bold", None)
            st.pop("faint", None)
        elif c in (38, 48) and i + 4 < len(codes) and codes[i + 1] == 2:
            st["fg" if c == 38 else "bg"] = "#%02x%02x%02x" % tuple(codes[i + 2:i + 5])
            i += 4
        elif c == 39:
            st.pop("fg", None)
        elif c == 49:
            st.pop("bg", None)
        i += 1


def style(st):
    parts = []
    if "fg" in st:
        parts.append("color:" + st["fg"])
    if "bg" in st:
        parts.append("background:" + st["bg"])
    if st.get("bold"):
        parts.append("font-weight:700")
    if st.get("faint"):
        parts.append("opacity:.6")
    return ";".join(parts)


def line_html(line):
    out, st, pos = [], {}, 0
    line = OTHER.sub("", line)
    for m in list(SGR.finditer(line)) + [None]:
        end = m.start() if m else len(line)
        text = line[pos:end]
        if text:
            cells = []
            for ch in text:
                e = html.escape(ch)
                if ord(ch) < 0x80 or 0x2500 <= ord(ch) <= 0x257F:
                    cells.append(e)  # Menlo draws these at one cell
                else:
                    # Give other characters a fixed cell width, so a
                    # fallback font cannot move the columns.
                    cells.append('<span class="%s">%s</span>' % ("w2" if wide(ch) else "w1", e))
            s = style(st)
            out.append('<span style="%s">%s</span>' % (s, "".join(cells)) if s else "".join(cells))
        if m:
            parse_sgr(m.group(1), st)
            pos = m.end()
    return "".join(out)


def width_cells(line):
    plain = OTHER.sub("", SGR.sub("", line))
    return sum(2 if wide(c) else 1 for c in plain)


def main():
    src, dst, title = sys.argv[1], sys.argv[2], sys.argv[3]
    lines = open(src, encoding="utf-8").read().split("\n")
    cols = max(width_cells(l) for l in lines)
    width = int(cols * CH_PX + 2 * PAD) + 2
    height = len(lines) * LINE_PX + 2 * PAD + BAR
    body = "\n".join(line_html(l) for l in lines)
    page = f"""<!doctype html><html><head><meta charset="utf-8"><style>
html,body{{margin:0;background:{BG};}}
.bar{{height:{BAR}px;display:flex;align-items:center;padding:0 14px;background:#202428;
  font:13px -apple-system,BlinkMacSystemFont,sans-serif;color:#9AA3A0;position:relative}}
.dot{{width:12px;height:12px;border-radius:50%;margin-right:8px}}
.t{{position:absolute;left:0;right:0;text-align:center;pointer-events:none}}
pre{{margin:0;padding:{PAD}px;color:{FG};font:{FONT_PX}px/{LINE_PX}px Menlo,monospace;
  font-family:Menlo,"Apple Color Emoji",monospace;white-space:pre}}
.w1,.w2{{display:inline-block;text-align:center;vertical-align:top;overflow:hidden}}
.w1{{width:{CH_PX}px}}
.w2{{width:{2*CH_PX}px;font-size:12px}}
</style></head><body>
<div class="bar"><span class="dot" style="background:#FF5F57"></span><span class="dot" style="background:#FEBC2E"></span><span class="dot" style="background:#28C840"></span><span class="t">{html.escape(title)}</span></div>
<pre>{body}</pre></body></html>"""
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, encoding="utf-8") as f:
        f.write(page)
        page_path = f.name
    try:
        subprocess.run([CHROME, "--headless", "--disable-gpu", "--hide-scrollbars",
                        "--force-device-scale-factor=2", f"--window-size={width},{height}",
                        f"--screenshot={os.path.abspath(dst)}", "file://" + page_path],
                       check=True, capture_output=True, timeout=60)
    finally:
        os.unlink(page_path)
    print(f"wrote {dst} ({width}x{height} at 2x)")


if __name__ == "__main__":
    main()
