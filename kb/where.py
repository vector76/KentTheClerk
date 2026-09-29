"""kb where — print where Kent the Clerk is installed and which knowledge base kb operates on.

The agent reads the record's files directly (S-0072), so it needs the knowledge folder's location as
much as kb does; asking kb avoids reading the variable in shell-specific syntax.
"""
from __future__ import annotations

from .common import PRODUCT_DIR, find_kb_root


def run(args) -> int:
    print(f"kent: {PRODUCT_DIR}")
    root, how = find_kb_root()
    if root is None:
        print(f"knowledge base: none ({how})")
        return 1
    print(f"knowledge base: {root} ({how})")
    return 0


def add_parser(sub) -> None:
    p = sub.add_parser("where", help="print where Kent is installed and which knowledge base kb uses")
    p.set_defaults(func=run)
