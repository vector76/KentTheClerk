"""kb — the recorder of Kent the Clerk. Run as: python <Kent install>/kb.py <command> ...

The knowledge base it operates on is named by KENT_KB, or found in the current folder or above."""
from __future__ import annotations

import argparse
import sys

from . import attest, check, index, ingest, record, serve, show, snapshot, version, where


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(prog="kb", description="conversation-grounded knowledge base tools")
    sub = ap.add_subparsers(dest="cmd", required=True)
    for mod in (snapshot, ingest, show, attest, record, check, index, serve, version, where):
        mod.add_parser(sub)
    args = ap.parse_args(argv)
    rc = args.func(args)
    return int(rc or 0)


if __name__ == "__main__":
    sys.exit(main())
