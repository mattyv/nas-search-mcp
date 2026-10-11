#!/usr/bin/env python3
"""Retrieve an original NAS file through authenticated MCP, without OCR conversion."""
import argparse
import base64
import importlib.util
import os
from pathlib import Path
import sys
import tempfile

_spec = importlib.util.spec_from_file_location('bridge', Path(__file__).with_name('mcp_bridge.py'))
bridge = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bridge)


class DownloadError(Exception):
    pass


def download(remote, source, output, replace=False):
    output = Path(output)
    if output.is_symlink() or (output.exists() and not replace):
        raise DownloadError('output exists; choose a new path or use --replace')
    reply = remote.request({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {'protocolVersion': '2025-03-26', 'capabilities': {}, 'clientInfo': {'name': 'nas-search-download', 'version': '0.1.0'}}})
    if 'error' in reply:
        raise DownloadError('MCP initialization failed')
    remote.request({'jsonrpc': '2.0', 'method': 'notifications/initialized'})
    fd, temporary = tempfile.mkstemp(prefix='.nas-download-', dir=output.parent)
    offset, version, size, identifier = 0, None, None, 2
    try:
        with os.fdopen(fd, 'wb') as stream:
            while True:
                args = {'path': source, 'offset': offset, 'max_bytes': 262144}
                if version is not None:
                    args['expected_version'] = version
                reply = remote.request({'jsonrpc': '2.0', 'id': identifier, 'method': 'tools/call', 'params': {'name': 'fetch_file', 'arguments': args}})
                identifier += 1
                result = reply.get('result', {})
                if 'error' in reply or result.get('isError'):
                    raise DownloadError('original-file retrieval failed')
                chunk = result.get('structuredContent', {})
                try:
                    data = base64.b64decode(chunk['data'], validate=True)
                    reported_size = chunk['size']
                    if not isinstance(reported_size, int) or not 0 <= reported_size <= 32 * 1024 * 1024:
                        raise ValueError()
                    if chunk['offset'] != offset or (version is not None and chunk['version'] != version) or (size is not None and reported_size != size):
                        raise ValueError()
                    version, size = chunk['version'], reported_size
                    if offset + len(data) > size:
                        raise ValueError()
                    next_offset = chunk.get('next_offset')
                    if next_offset is not None and (not data or next_offset != offset + len(data)):
                        raise ValueError()
                    if next_offset is None and (offset + len(data) != size or not chunk.get('complete')):
                        raise ValueError()
                except (KeyError, ValueError, TypeError) as exc:
                    raise DownloadError('invalid or changed original-file response') from exc
                stream.write(data)
                offset += len(data)
                if next_offset is None:
                    break
            stream.flush()
            os.fsync(stream.fileno())
        if not replace:
            # Exclusive publication avoids overwriting files created during transfer.
            os.link(temporary, output)
        else:
            os.replace(temporary, output)
        return offset
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


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
