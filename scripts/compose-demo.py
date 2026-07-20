#!/usr/bin/env python3
"""compose-demo.py — build docs/demo.svg from the terminal cast + screenshots.

One self-contained animated SVG, three timed slides:

  [0,    D)    the svg-term rendering of docs/demo.cast (build + serve)
  [D,   1.5D)  the lesson page screenshot (terminal + quiz)
  [1.5D, 2D)   the scoreboard screenshot (standings)

The screenshots hold for half the cast's duration each: they are single
still frames, so they read instantly, while the cast needs its full length
to type through. That also keeps the master loop an exact multiple of the
cast duration (2xD), which matters — the cast has its own infinite
animation, and only a whole-multiple master loop keeps the two phase-aligned
forever (it replays hidden during the screenshot slides and lands back on
frame zero exactly when its slide returns).

Screenshots are embedded as base64 JPEG, so the README needs no external
hosting.

Usage: scripts/compose-demo.py <term.svg> <lesson.jpg> <scoreboard.jpg> <out.svg>
"""
import base64
import re
import sys

WIDTH = 968.0

# Slide weights as fractions of the cast duration D. Must sum to a whole
# number so the master loop stays an exact multiple of D (see above).
WEIGHTS = (1.0, 0.5, 0.5)

term_svg_path, lesson_jpg, score_jpg, out_path = sys.argv[1:5]

term = open(term_svg_path).read()
m = re.match(r'<svg[^>]*width="([\d.]+)" height="([\d.]+)"', term)
tw, th = float(m.group(1)), float(m.group(2))
dur = float(re.search(r'animation-duration:([\d.]+)s', term).group(1))

# Screenshots are 2560x1140 → height at our width.
HEIGHT = round(WIDTH * 1140 / 2560, 2)
total = round(sum(WEIGHTS) * dur, 3)


def slide_image(path, cls):
    b64 = base64.b64encode(open(path, 'rb').read()).decode()
    return (f'<g class="{cls}"><image href="data:image/jpeg;base64,{b64}" '
            f'x="0" y="0" width="{WIDTH}" height="{HEIGHT}" '
            f'preserveAspectRatio="xMidYMid meet"/></g>')


# Strip the terminal svg's XML decl (if any) and inline it, centred.
term = re.sub(r'^<\?xml[^>]*\?>', '', term).strip()
ty = round((HEIGHT - th) / 2, 2)
tx = round((WIDTH - tw) / 2, 2)
term_layer = f'<g class="s0"><g transform="translate({tx},{ty})">{term}</g></g>'

# Percent boundaries of each slide within the master loop.
bounds, acc = [], 0.0
for w in WEIGHTS:
    bounds.append((acc * 100 / sum(WEIGHTS), (acc + w) * 100 / sum(WEIGHTS)))
    acc += w

fade = 1.5  # percent of the loop
css = ['.s0,.s1,.s2{opacity:0}']
for i, (a, b) in enumerate(bounds):
    name = f'v{i}'
    css.append(
        f'@keyframes {name}{{'
        f'0%{{opacity:{1 if i == 0 else 0}}}'
        + (f'{max(a - fade, 0):.2f}%{{opacity:0}}{a + fade:.2f}%{{opacity:1}}' if i else '')
        + f'{b - fade:.2f}%{{opacity:1}}'
        + (f'{min(b + fade, 100):.2f}%{{opacity:0}}' if i < len(bounds) - 1 else f'100%{{opacity:1}}')
        + ('100%{opacity:0}}' if i < len(bounds) - 1 else '}')
    )
    css.append(f'.s{i}{{animation:{name} {total}s linear infinite}}')

out = (
    f'<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" '
    f'width="{WIDTH:.0f}" height="{HEIGHT}" viewBox="0 0 {WIDTH:.0f} {HEIGHT}">'
    f'<style>{"".join(css)}</style>'
    f'<rect width="{WIDTH:.0f}" height="{HEIGHT}" rx="5" fill="#282d35"/>'
    f'{term_layer}'
    f'{slide_image(lesson_jpg, "s1")}'
    f'{slide_image(score_jpg, "s2")}'
    f'</svg>'
)
open(out_path, 'w').write(out)
holds = ' + '.join(f'{w * dur:.1f}s' for w in WEIGHTS)
print(f'{out_path}: {len(out)//1024} KiB, loop {total}s ({holds}), canvas {WIDTH:.0f}x{HEIGHT}')
