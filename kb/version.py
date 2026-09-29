"""kb version — print the identity of the system (S-0030): version, content hash, and the files hashed."""
from __future__ import annotations

from .common import PRODUCT_DIR, SYSTEM_VERSION, system_files, system_hash


def run(args) -> None:
    print(f"system_version: {SYSTEM_VERSION}")
    print(f"system_hash: {system_hash()}")
    if args.files:
        print(f"files, relative to {PRODUCT_DIR}:")
        for p in system_files():
            print(f"  {p.relative_to(PRODUCT_DIR).as_posix()}")


def add_parser(sub) -> None:
    p = sub.add_parser("version", help="print system version and content hash")
    p.add_argument("--files", action="store_true", help="also list the files the hash covers")
    p.set_defaults(func=run)
