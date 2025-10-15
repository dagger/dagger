#!/usr/bin/env python

import sys

from dagger.mod.cli import app

if __name__ == "__main__":
    sys.exit(app(None, "--register" in sys.argv[1:]))
