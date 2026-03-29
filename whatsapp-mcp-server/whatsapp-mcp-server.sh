#!/bin/bash

REAL_SCRIPT="$(readlink -f "$0")"
SCRIPT_DIR="$(cd "$(dirname "$REAL_SCRIPT")" && pwd)"
uv --directory "$SCRIPT_DIR" run main.py "$@"