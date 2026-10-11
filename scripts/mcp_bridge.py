#!/usr/bin/env python3
"""Dependency-free stdio bridge for this project's stateless Streamable HTTP server.

This bridge forwards requests and notifications. It does not open a GET/SSE
subscription, resume streams, or handle server-initiated client requests.
"""
import argparse
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
