#!/usr/bin/env python3
"""Contrast test for the operator console palette.

Checks every text/background pairing the UI actually uses against WCAG AA in
BOTH themes. This is not ceremony: the house palette has a true-black dark
theme where mid-greys that "look fine" measure as washed out, and two separate
gold tokens precisely because one gold cannot be a fill behind white text, a
bright mark, AND small text on paper.

Run: python3 tools/check_contrast.py
"""
import re
import sys
from pathlib import Path

TOKENS = Path(__file__).resolve().parent.parent / "dist/www/app/css/tokens.css"

AA_NORMAL = 4.5   # body text
AA_LARGE = 3.0    # >=24px, or >=18.66px bold
AA_UI = 3.0       # non-text UI boundaries (borders, marks)


def srgb_to_lin(c):
    c = c / 255
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def luminance(hexstr):
    h = hexstr.lstrip("#")
    if len(h) == 3:
        h = "".join(ch * 2 for ch in h)
    r, g, b = (int(h[i:i + 2], 16) for i in (0, 2, 4))
    return 0.2126 * srgb_to_lin(r) + 0.7152 * srgb_to_lin(g) + 0.0722 * srgb_to_lin(b)


def ratio(fg, bg):
    a, b = luminance(fg), luminance(bg)
    hi, lo = max(a, b), min(a, b)
    return (hi + 0.05) / (lo + 0.05)


def parse_theme(css, selector):
    """Pull `--name: #rrggbb;` declarations out of one rule block."""
    i = css.find(selector)
    if i < 0:
        sys.exit(f"FATAL: selector {selector!r} not found in {TOKENS}")
    block = css[css.index("{", i) + 1: css.index("}", i)]
    return dict(re.findall(r"--([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\s*;", block))


# (foreground token, background token, threshold, what it is used for)
PAIRINGS = [
    ("text",            "canvas",       AA_NORMAL, "body text on the page ground"),
    ("text",            "surface",      AA_NORMAL, "body text on a card"),
    ("text",            "surface-sunk", AA_NORMAL, "body text on a sunken panel"),
    ("text-muted",      "canvas",       AA_NORMAL, "secondary text on the ground"),
    ("text-muted",      "surface",      AA_NORMAL, "secondary text on a card"),
    ("text-faint",      "surface",      AA_LARGE,  "tertiary/label text on a card"),
    ("brass",           "canvas",       AA_NORMAL, "gold text/link on the ground"),
    ("brass",           "surface",      AA_NORMAL, "gold text/link on a card"),
    ("on-brass",        "brass",        AA_NORMAL, "button label on a brass fill"),
    ("shine",           "surface",      AA_UI,     "bright gold mark (no text)"),
    ("shine",           "canvas",       AA_UI,     "bright gold mark on the ground"),
    ("ok",              "surface",      AA_NORMAL, "success text"),
    ("warn",            "surface",      AA_NORMAL, "warning text"),
    ("danger",          "surface",      AA_NORMAL, "error text"),
    ("on-danger",       "danger",       AA_NORMAL, "label on a danger fill"),
    ("border-strong",   "surface",      AA_UI,     "emphasised border / divider"),
    ("border-strong",   "canvas",       AA_UI,     "emphasised border on the ground"),
    ("mono",            "surface-sunk", AA_NORMAL, "measured values in mono"),
]


def main():
    css = TOKENS.read_text()
    themes = {
        "light": parse_theme(css, ":root"),
        "dark": parse_theme(css, '[data-theme="dark"]'),
    }

    failures = []
    print(f"{'pairing':<46} {'light':>7} {'dark':>7}  min")
    print("-" * 72)
    for fg, bg, threshold, label in PAIRINGS:
        row = {}
        for tname, tokens in themes.items():
            if fg not in tokens:
                failures.append(f"{tname}: token --{fg} is not defined")
                row[tname] = None
                continue
            if bg not in tokens:
                failures.append(f"{tname}: token --{bg} is not defined")
                row[tname] = None
                continue
            r = ratio(tokens[fg], tokens[bg])
            row[tname] = r
            if r < threshold:
                failures.append(
                    f"{tname}: {fg} on {bg} = {r:.2f}:1, needs {threshold}:1  ({label})")
        l = f"{row['light']:.2f}" if row.get("light") else "  --"
        d = f"{row['dark']:.2f}" if row.get("dark") else "  --"
        mark = " " if not failures or all(
            (row.get(t) or 0) >= threshold for t in ("light", "dark")) else "X"
        print(f"{mark} {fg + ' on ' + bg:<44} {l:>7} {d:>7}  {threshold}")

    # Every colour token must exist in both themes -- the house rule is that a
    # colour is declared as a (light, dark) pair so the dark theme cannot be
    # forgotten.
    only_light = set(themes["light"]) - set(themes["dark"])
    only_dark = set(themes["dark"]) - set(themes["light"])
    for t in sorted(only_light):
        failures.append(f"--{t} is defined for light but MISSING from dark")
    for t in sorted(only_dark):
        failures.append(f"--{t} is defined for dark but MISSING from light")

    print()
    if failures:
        print(f"FAIL -- {len(failures)} problem(s):")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"PASS -- {len(PAIRINGS)} pairings x 2 themes meet WCAG AA, "
          f"{len(themes['light'])} tokens paired in both themes.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
