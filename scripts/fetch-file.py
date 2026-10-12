#!/usr/bin/env python3
"""Retrieve an original NAS file through authenticated MCP, without OCR conversion."""
import argparse
import importlib.util
from pathlib import Path
import sys

_spec = importlib.util.spec_from_file_location('bridge', Path(__file__).with_name('mcp_bridge.py'))
bridge = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bridge)


DownloadError = bridge.DownloadError
download = bridge.download


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True)
    parser.add_argument('--token-file', required=True)
    parser.add_argument('--path', required=True)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--replace', action='store_true')
    args = parser.parse_args()
    try:
        remote = bridge.Bridge(args.url, bridge.read_token(args.token_file, require_private=True))
        size = download(remote, args.path, args.output, args.replace)
        print('Downloaded original file (' + str(size) + ' bytes) to ' + str(args.output))
    except (DownloadError, bridge.BridgeError, OSError) as exc:
        print('Download: ' + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
