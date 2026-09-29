#!/usr/bin/env python3
"""Entry point: python <Kent install>/kb.py <command> [...]"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from kb.__main__ import main  # noqa: E402

if __name__ == "__main__":
    sys.exit(main())
