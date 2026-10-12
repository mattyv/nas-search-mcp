#!/usr/bin/env python3
"""Dependency-free stdio bridge for this project's stateless Streamable HTTP server.

This bridge forwards requests and notifications. It does not open a GET/SSE
subscription, resume streams, or handle server-initiated client requests.
"""
import argparse
import base64
import tempfile
import json
import http.client
import os
from pathlib import Path
import stat
import sys
import urllib.error
import urllib.parse
import urllib.request

MAX_MESSAGE = 1024 * 1024
MAX_RESPONSE = 16 * 1024 * 1024

class BridgeError(Exception):
    pass

def validate_url(url):
    if any(c.isspace() for c in url):
        raise BridgeError('URL must not contain whitespace')
    try:
        parsed = urllib.parse.urlsplit(url)
        parsed.port
    except ValueError as exc:
        raise BridgeError('invalid endpoint URL') from exc
    if parsed.scheme not in {'http', 'https'} or not parsed.hostname or parsed.username or parsed.password or parsed.fragment:
        raise BridgeError('URL must be an HTTP(S) endpoint without credentials or fragments')
    return url

def read_token(path, require_private=False):
    flags = os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0)
    try:
        fd = os.open(path, flags)
        with os.fdopen(fd, 'r', encoding='utf-8') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode):
                raise BridgeError('token source must be a regular file')
            if require_private and os.name != 'nt' and stat.S_IMODE(info.st_mode) & 0o077:
                raise BridgeError('token file must have owner-only permissions (chmod 600)')
            text = stream.read(65537)
    except (OSError, UnicodeError) as exc:
        raise BridgeError('cannot read token file') from exc
    if len(text) > 65536:
        raise BridgeError('token file is too large')
    token = None
    for line in text.splitlines():
        line = line.strip()
        if line.startswith('export '):
            line = line[7:].strip()
        if line.startswith('NAS_TOKEN='):
            token = line.split('=', 1)[1].strip()
            if len(token) >= 2 and token[0] == token[-1] and token[0] in {'"', "'"}:
                token = token[1:-1]
            break
    if token is None:
        token = text.strip()
    if not token or '\n' in token or '\r' in token or any(ord(c) <= 32 or ord(c) > 126 for c in token):
        raise BridgeError('token file must contain a token or NAS_TOKEN=value')
    return token

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


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

class Bridge:
    def __init__(self, url, token, timeout=600):
        self.url = validate_url(url)
        self.token = token
        self.timeout = timeout
        self.session = None
        self.protocol = None
        self.opener = urllib.request.build_opener(NoRedirect)

    def request(self, message):
        params = message.get('params')
        if message.get('method') == 'tools/call' and isinstance(params, dict) and params.get('name') == 'download_file':
            if 'id' not in message:
                return None
            try:
                args = params.get('arguments', {})
                if not isinstance(args, dict) or set(args) != {'path', 'output'} or not isinstance(args['path'], str) or not args['path'] or not isinstance(args['output'], str) or not Path(args['output']).is_absolute():
                    raise DownloadError('provide a NAS path and an absolute local output path; overwrites are disabled')
                output = Path(args['output'])
                size = download(Bridge(self.url, self.token, self.timeout), args['path'], output)
                result = {'content': [{'type': 'text', 'text': 'Downloaded original file to ' + str(output)}], 'structuredContent': {'path': str(output), 'bytes': size}}
            except (DownloadError, BridgeError, OSError, ValueError) as exc:
                result = {'isError': True, 'content': [{'type': 'text', 'text': str(exc)}]}
            return {'jsonrpc': '2.0', 'id': message['id'], 'result': result}
        reply = self._request(message)
        if message.get('method') == 'tools/list' and isinstance(reply, dict) and isinstance(reply.get('result'), dict) and isinstance(reply['result'].get('tools'), list):
            reply['result']['tools'].append({
                'name': 'download_file',
                'description': 'Download an original NAS file (up to 32 MiB) to this MCP bridge host, where the LLM client runs. Provide a NAS path from search/browse and an absolute local output filename in an existing directory. Never overwrites existing files. This writes locally, not on the NAS; subsequent local access requires the client filesystem tools.',
                'inputSchema': {'type': 'object', 'properties': {'path': {'type': 'string', 'minLength': 1, 'description': 'NAS file path from search or browse'}, 'output': {'type': 'string', 'description': 'Absolute local output filename in an existing directory'}}, 'required': ['path', 'output'], 'additionalProperties': False},
                'annotations': {'readOnlyHint': False, 'destructiveHint': False, 'idempotentHint': False, 'openWorldHint': True},
            })
        return reply

    def _request(self, message):
        headers = {'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json', 'Accept': 'application/json, text/event-stream'}
        if self.session:
            headers['Mcp-Session-Id'] = self.session
        if self.protocol:
            headers['Mcp-Protocol-Version'] = self.protocol
        request = urllib.request.Request(self.url, data=json.dumps(message, ensure_ascii=False).encode('utf-8'), headers=headers, method='POST')
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                if response.headers.get('Mcp-Session-Id'):
                    self.session = response.headers['Mcp-Session-Id']
                if 'id' not in message:
                    return None
                body = response.read(MAX_RESPONSE + 1)
                if len(body) > MAX_RESPONSE:
                    raise BridgeError('server response exceeds bridge limit')
                content_type = response.headers.get('Content-Type', '').split(';')[0].strip().lower()
        except urllib.error.HTTPError as exc:
            code = exc.code
            exc.close()
            raise BridgeError('MCP endpoint returned HTTP ' + str(code)) from exc
        except (urllib.error.URLError, TimeoutError, OSError, http.client.HTTPException) as exc:
            raise BridgeError('cannot reach MCP endpoint') from exc
        try:
            if content_type == 'text/event-stream':
                replies = []
                data = []
                for line in body.decode('utf-8').splitlines() + ['']:
                    if not line:
                        if data:
                            replies.append(json.loads('\n'.join(data)))
                            data = []
                    elif line.startswith('data:'):
                        data.append(line[5:].lstrip(' '))
                matches = [reply for reply in replies if isinstance(reply, dict) and reply.get('id') == message['id']]
                if not matches:
                    raise BridgeError('MCP stream contained no matching response')
                reply = matches[-1]
            else:
                reply = json.loads(body)
            if not isinstance(reply, dict) or reply.get('jsonrpc') != '2.0' or reply.get('id') != message['id'] or ('result' not in reply and 'error' not in reply):
                raise BridgeError('invalid MCP response')
            if message.get('method') == 'initialize' and isinstance(reply.get('result'), dict):
                protocol = reply['result'].get('protocolVersion')
                if not isinstance(protocol, str) or any(ord(c) < 32 for c in protocol):
                    raise BridgeError('invalid MCP protocol version')
                self.protocol = protocol
            return reply
        except (ValueError, UnicodeError) as exc:
            raise BridgeError('invalid MCP response encoding') from exc


def rpc_error(identifier, code, message):
    return {'jsonrpc': '2.0', 'id': identifier, 'error': {'code': code, 'message': message}}

def serve_stdio(bridge, incoming=sys.stdin, outgoing=sys.stdout):
    while True:
        line = incoming.readline(MAX_MESSAGE + 1)
        if not line:
            return
        if len(line) > MAX_MESSAGE:
            while line and not line.endswith('\n'):
                line = incoming.readline(MAX_MESSAGE + 1)
            reply = rpc_error(None, -32600, 'message exceeds bridge limit')
        else:
            try:
                message = json.loads(line)
            except ValueError:
                reply = rpc_error(None, -32700, 'invalid JSON')
            else:
                if not isinstance(message, dict) or message.get('jsonrpc') != '2.0' or not isinstance(message.get('method'), str) or ('id' in message and (isinstance(message['id'], bool) or not isinstance(message['id'], (str, int)))):
                    reply = rpc_error(None, -32600, 'invalid JSON-RPC request; batches are unsupported')
                else:
                    try:
                        reply = bridge.request(message)
                    except BridgeError as exc:
                        reply = rpc_error(message['id'], -32000, str(exc)) if 'id' in message else None
                        if reply is None:
                            print('MCP notification could not be delivered', file=sys.stderr)
        if reply is not None:
            outgoing.write(json.dumps(reply, ensure_ascii=False) + '\n')
            outgoing.flush()

def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True)
    parser.add_argument('--token-file', required=True)
    parser.add_argument('--timeout', type=float, default=600)
    args = parser.parse_args(argv)
    try:
        if args.timeout <= 0:
            raise BridgeError('timeout must be positive')
        bridge = Bridge(args.url, read_token(args.token_file, require_private=True), args.timeout)
        serve_stdio(bridge)
        return 0
    except (BridgeError, BrokenPipeError) as exc:
        print('MCP bridge: ' + str(exc), file=sys.stderr)
        return 1

if __name__ == '__main__':
    raise SystemExit(main())
