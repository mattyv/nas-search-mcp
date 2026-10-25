#!/usr/bin/env python3
"""Fixed read-only Personal Space operations through a private Unix socket."""
import collections
import base64
import fcntl
from http.server import BaseHTTPRequestHandler
import json
import os
from pathlib import Path
import pwd
import re
import resource
import socket
import socketserver
import stat
import struct
import subprocess
import sys
import tempfile
import time
import unicodedata

ROOT = Path('/volume1/@nas-photos-broker')
SOCKET = ROOT / 'run' / 'photos.sock'
MAX_OUTPUT = 1024 * 1024
CLI = '/usr/syno/bin/synowebapi'
INSTALL = Path('/volume1/@nas-search-upgrader')


def integer(value, low, high):
    if type(value) is not int or not low <= value <= high:
        raise ValueError('invalid integer')
    return value


def text(value, maximum, nonempty=False):
    if (not isinstance(value, str) or len(value) > maximum or (nonempty and not value)
            or any(unicodedata.category(char).startswith('C') for char in value)):
        raise ValueError('invalid text')
    return value


def parse_response(body):
    if len(body) > MAX_OUTPUT:
        raise ValueError('response too large')
    value = body.decode('utf-8').strip()
    cgi = value.lower().startswith('content-type: application/json')
    if cgi:
        value = value.replace('\r\n', '\n')
        headers, separator, value = value.partition('\n\n')
        if not separator or len(headers) > 8192:
            raise ValueError('invalid CGI response')
    decoder = json.JSONDecoder()
    envelope, end = decoder.raw_decode(value)
    tail = value[end:].strip()
    if tail:
        # Synology Photos1.9.1 writes a successful CGI response directly and the
        # synowebapi wrapper then reports117. Accept only this observed combination.
        trailer = decoder.decode(tail)
        if (not cgi or trailer.get('success') is not False or trailer.get('error') != {'code': 117}
                or set(trailer) - {'success', 'error', 'httpd_restart'}):
            raise ValueError('ambiguous response')
    if not isinstance(envelope, dict) or envelope.get('success') is not True:
        raise ValueError('Photos request failed')
    rows = envelope.get('data', {}).get('list')
    if not isinstance(rows, list) or len(rows) > 100 or any(not isinstance(row, dict) for row in rows):
        raise ValueError('unsupported response')
    return rows


def child_limits():
    resource.setrlimit(resource.RLIMIT_FSIZE, (MAX_OUTPUT, MAX_OUTPUT))
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_CPU, (20, 20))


def invoke(args):
    # A capped temporary output file avoids buffering unbounded CLI output in RAM.
    with tempfile.TemporaryFile() as output:
        try:
            subprocess.run(args, stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.DEVNULL,
                           timeout=25, check=False, preexec_fn=child_limits,
                           env={'PATH': '/usr/syno/bin:/usr/bin:/bin', 'HOME': '/root'})
        except (OSError, subprocess.TimeoutExpired):
            raise ValueError('Photos unavailable') from None
        output.seek(0)
        body = output.read(MAX_OUTPUT + 1)
        if len(body) > MAX_OUTPUT:
            raise ValueError('response too large')
        return body


def call_api(api, method, version, params):
    if (api, method, version) not in (('SYNO.Foto.Browse.Person', 'list', 1),
                                      ('SYNO.Foto.Browse.Item', 'list', 2),
                                      ('SYNO.Foto.Browse.Item', 'get', 2)):
        raise ValueError('unsupported operation')
    args = [CLI, '--exec', 'api=' + api, 'method=' + method, 'version=' + str(version), 'runner=matt']
    args.extend(key + '=' + json.dumps(value, separators=(',', ':')) for key, value in params.items())
    return parse_response(invoke(args))


def jpeg(data):
    if not 4 <= len(data) <= 512 * 1024 or not data.startswith(b'\xff\xd8') or not data.endswith(b'\xff\xd9'):
        raise ValueError('preview unavailable')
    return data


def thumbnail(unit, cache_key):
    integer(unit, 1, 2147483647)
    if not isinstance(cache_key, str) or re.fullmatch(r'[A-Za-z0-9_-]{1,128}', cache_key) is None:
        raise ValueError('invalid thumbnail metadata')
    with tempfile.TemporaryDirectory(prefix='nas-photo-preview-') as directory:
        path = Path(directory) / 'preview.jpg'
        args = [CLI, '--exec', 'api=SYNO.Foto.Thumbnail', 'method=get', 'version=2', 'runner=matt',
                'outfile=' + str(path), 'id=' + str(unit), 'cache_key=' + json.dumps(cache_key),
                'type="unit"', 'size="m"']
        diagnostic = invoke(args)
        if path.exists() and not path.is_symlink():
            info = path.lstat()
            if stat.S_ISREG(info.st_mode) and info.st_size <= 512 * 1024:
                data = path.read_bytes()
                if data.startswith(b'\xff\xd8'):
                    return jpeg(data)
        # Some Photos handlers emit CGI content directly instead of honoring the
        # wrapper's outfile. Accept a bounded JPEG only with explicit MIME framing.
        header, separator, body = diagnostic.partition(b'\r\n\r\n')
        if not separator:
            header, separator, body = diagnostic.partition(b'\n\n')
        if (not separator or len(header) > 8192
                or not header.lower().startswith(b'content-type: image/jpeg')):
            raise ValueError('preview response unsupported')
        end = body.rfind(b'\xff\xd9')
        if end < 0:
            raise ValueError('preview response incomplete')
        tail = body[end + 2:].strip()
        if tail:
            trailer = json.loads(tail)
            if (trailer.get('success') is not False or trailer.get('error') != {'code': 117}
                    or set(trailer) - {'success', 'error', 'httpd_restart'}):
                raise ValueError('preview response ambiguous')
        return jpeg(body[:end + 2])


def belongs(row, person):
    membership = row.get('additional', {}).get('person')
    return (isinstance(membership, list)
            and any(isinstance(item, dict) and type(item.get('id')) is int
                    and item['id'] == person for item in membership))


def read_only_request(path, body, call=call_api, fetch_preview=thumbnail):
    if not isinstance(body, dict) or path not in ('/people', '/photos', '/preview'):
        raise ValueError('invalid operation')
    allowed = ({'offset', 'limit', 'query'} if path == '/people' else
               {'person_id', 'photo_id'} if path == '/preview' else {'offset', 'limit', 'person_id'})
    if set(body) - allowed:
        raise ValueError('unexpected arguments')
    if path == '/preview':
        person = integer(body.get('person_id'), 1, 2147483647)
        identifier = integer(body.get('photo_id'), 1, 2147483647)
        rows = call('SYNO.Foto.Browse.Item', 'get', 2, {'id': [identifier], 'additional': ['person', 'thumbnail']})
        if (len(rows) != 1 or integer(rows[0].get('id'), 1, 2147483647) != identifier
                or not belongs(rows[0], person)):
            raise ValueError('unconfirmed photo access')
        metadata = rows[0].get('additional', {}).get('thumbnail', {})
        if metadata.get('m') != 'ready' or integer(metadata.get('unit_id'), 1, 2147483647) != identifier:
            raise ValueError('preview not ready')
        cache = metadata.get('cache_key')
        if not isinstance(cache, str) or re.fullmatch(r'[A-Za-z0-9_-]{1,128}', cache) is None:
            raise ValueError('invalid thumbnail metadata')
        image = jpeg(fetch_preview(identifier, cache))
        return {'mime_type': 'image/jpeg', 'data': base64.b64encode(image).decode('ascii')}
    offset = integer(body.get('offset', 0), 0, 100000)
    limit = integer(body.get('limit', 20), 1, 100)
    if path == '/people':
        query = text(body.get('query', ''), 100).casefold()
        rows = call('SYNO.Foto.Browse.Person', 'list', 1, {'offset': offset, 'limit': limit})
    else:
        person = integer(body.get('person_id'), 1, 2147483647)
        rows = call('SYNO.Foto.Browse.Item', 'list', 2,
                    {'person_id': person, 'offset': offset, 'limit': limit, 'additional': ['person']})
    if not isinstance(rows, list) or len(rows) > limit:
        raise ValueError('unbounded response')
    result = []
    for row in rows:
        identifier = integer(row.get('id'), 1, 2147483647)
        if path == '/people':
            name = text(row.get('name'), 256)
            if not name or row.get('show') is not True or query not in name.casefold():
                continue
            result.append({'id': identifier, 'name': name,
                           'item_count': integer(row.get('item_count'), 0, 9223372036854775807)})
        else:
            if not belongs(row, person):
                raise ValueError('unconfirmed person filter')
            filename = text(row.get('filename'), 1024, True)
            if filename in ('.', '..') or '/' in filename or '\\' in filename:
                raise ValueError('invalid filename')
            if row.get('type') not in ('photo', 'video'):
                raise ValueError('unsupported item type')
            result.append({'id': identifier, 'filename': filename, 'type': row['type'],
                           'taken_time': integer(row.get('time'), -9223372036854775808, 9223372036854775807)})
    next_offset = offset + len(rows) if len(rows) == limit and offset + len(rows) <= 100000 else None
    return {'people' if path == '/people' else 'photos': result, 'offset': offset,
            'next_offset': next_offset, 'truncated': len(rows) == limit}


def policy_allows(contents):
    values = {}
    for line in contents.splitlines():
        match = re.match(r'^\s*(?:export\s+)?(NAS_ALLOWED_FOLDERS|NAS_DENIED_FOLDERS)\s*=', line)
        if not match:
            continue
        key = match.group(1)
        if not line.startswith(key + '=') or key in values:
            return False
        value = line.split('=', 1)[1].strip()
        if value.startswith(('"', "'")):
            if len(value) < 2 or value[-1] != value[0]:
                return False
            value = value[1:-1]
        if '"' in value or "'" in value:
            return False
        values[key] = [part.strip() for part in value.split(',') if part.strip()]
    return ('Photos' in values.get('NAS_ALLOWED_FOLDERS', [])
            and not any(path == 'Photos' or path.startswith('Photos/')
                        for path in values.get('NAS_DENIED_FOLDERS', [])))


def current_access_allowed():
    try:
        trusted(INSTALL, True)
        fd = os.open(str(INSTALL / '.env'), os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'r', encoding='utf-8') as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
                return False
            contents = stream.read(65537)
            return len(contents) <= 65536 and policy_allows(contents)
    except (OSError, ValueError, UnicodeError):
        return False


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.0'

    def setup(self):
        super().setup()
        self.connection.settimeout(5)

    def log_message(self, *args):
        pass

    def reply(self, status, body):
        data = json.dumps(body, ensure_ascii=True, separators=(',', ':')).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        try:
            _, uid, _ = struct.unpack('3i', self.connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if uid not in (0, self.server.app_uid):
                self.reply(403, {'error': 'Photos access denied'})
                return
            now = time.monotonic()
            while self.server.requests and self.server.requests[0] <= now - 60:
                self.server.requests.popleft()
            if len(self.server.requests) >= 20:
                self.reply(429, {'error': 'Photos request limit reached; retry later'})
                return
            self.server.requests.append(now)
            if self.headers.get('Transfer-Encoding') or self.headers.get('Content-Type') != 'application/json':
                raise ValueError('invalid framing')
            length = int(self.headers.get('Content-Length', '0'))
            if not 1 <= length <= 4096:
                raise ValueError('invalid body')
            body = json.loads(self.rfile.read(length))
            if self.path == '/health' and body == {}:
                self.reply(200, {'status': 'ok'})
                return
            if not self.server.access_allowed():
                self.reply(403, {'error': 'Current folder restrictions block Synology Photos'})
                return
            self.reply(200, read_only_request(self.path, body))
        except (ValueError, KeyError, TypeError, AttributeError, OSError):
            self.reply(503, {'error': 'Photos request failed or response unsupported'})


def trusted(path, directory=False):
    for candidate in (path, *path.parents):
        info = candidate.lstat()
        expected = stat.S_ISDIR if candidate != path or directory else stat.S_ISREG
        if not expected(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('untrusted host installation')


def serve():
    trusted(ROOT, True)
    trusted(ROOT / 'synology-photos-host.py')
    run = ROOT / 'run'
    trusted(run, True)
    lock = os.open(str(ROOT / '.host.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        if SOCKET.exists() or SOCKET.is_symlink():
            if not stat.S_ISSOCK(SOCKET.lstat().st_mode):
                raise ValueError('unexpected socket object')
            SOCKET.unlink()
        with socketserver.UnixStreamServer(str(SOCKET), Handler) as server:
            server.app_uid = pwd.getpwnam('matt').pw_uid
            server.requests = collections.deque()
            server.access_allowed = current_access_allowed
            os.chown(str(SOCKET), server.app_uid, 0)
            SOCKET.chmod(0o600)
            server.serve_forever(poll_interval=1)
    finally:
        os.close(lock)


def main():
    if os.geteuid() != 0 or sys.argv[1:] != ['serve']:
        print('Run the installed Photos broker as root with serve.')
        return 1
    os.umask(0o077)
    try:
        serve()
    except Exception:
        print('Photos broker unavailable; inspect the protected installation.')
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
