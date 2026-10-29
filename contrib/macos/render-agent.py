#!/usr/bin/env python3
"""Render an explicitly selected Grove LaunchAgent without installing it."""
import argparse
import os
import plistlib
import re
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("kind", choices=("daemon", "tray"))
parser.add_argument("--instance", required=True)
parser.add_argument("--app", required=True)
parser.add_argument("--config")
parser.add_argument("--profile")
parser.add_argument("--name", default="Grove")
parser.add_argument("--output", required=True)
args = parser.parse_args()
if not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", args.instance):
    parser.error("instance must contain only letters, digits, underscores or hyphens")
required_paths = [args.app, args.output, args.config if args.kind == "daemon" else args.profile]
if any(not path or not os.path.isabs(path) for path in required_paths):
    parser.error("app, output and selected config/profile must be absolute paths")
if not args.name or any(ord(char) < 32 or ord(char) == 127 for char in args.name):
    parser.error("name must be nonempty and contain no control characters")
base = Path(__file__).resolve().parent
name = "com.torfstack.groved.plist.in" if args.kind == "daemon" else "com.torfstack.grove-tray.plist.in"
with (base / "launchagents" / name).open("rb") as source:
    agent = plistlib.load(source)
values = {
    "@GROVE_LABEL@": "com.torfstack.groved." + args.instance,
    "@GROVED_PATH@": os.path.join(args.app, "Contents/Helpers/groved"),
    "@GROVE_CONFIG@": args.config or "",
    "@GROVE_INSTANCE@": args.instance,
    "@GROVE_APP@": args.app,
    "@GROVE_PROFILE@": args.profile or "",
    "@GROVE_NAME@": args.name,
}
placeholders = re.compile("|".join(re.escape(key) for key in values))

def substitute(value):
    if isinstance(value, str):
        return placeholders.sub(lambda match: values[match.group()], value)
    if isinstance(value, list):
        return [substitute(item) for item in value]
    if isinstance(value, dict):
        return {key: substitute(item) for key, item in value.items()}
    return value

rendered = plistlib.dumps(substitute(agent), fmt=plistlib.FMT_XML, sort_keys=False)
fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "wb") as output:
    output.write(rendered)
