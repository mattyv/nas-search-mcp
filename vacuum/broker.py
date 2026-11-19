"""Robot-vacuum broker: owns the Dreame connection and serves a tiny JSON API
on a Unix socket for the MCP service.

The MCP service validates every command in the verified core before it reaches
this process. The broker only maps the fixed command set onto the vendor
library and reports status; it has no other entry points.
"""

import json
import logging
import os
import socketserver
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler

LOG = logging.getLogger('vacuum-broker')
MAX_BODY = 64 * 1024
SUCTION_NAMES = {0: 'quiet', 1: 'standard', 2: 'strong', 3: 'turbo'}


class Unavailable(Exception):
    pass


class BadRequest(Exception):
    pass


class Refused(Exception):
    pass


class ConfigError(Exception):
    pass


def _text(value, limit=256):
    text = '' if value is None else str(value)
    text = ''.join(ch for ch in text if ch.isprintable())
    return text[:limit]


def _count(value, limit=100000):
    try:
        number = int(value)
    except (TypeError, ValueError):
        return 0
    return max(0, min(number, limit))


def _is_int(value):
    return isinstance(value, int) and not isinstance(value, bool)


class Broker:
    def __init__(self, device, area_key=None, time_key=None, refusal_types=()):
        self.device = device
        self.area_key = area_key
        self.time_key = time_key
        self.refusal_types = tuple(refusal_types)
        self.lock = threading.Lock()

    def _device(self):
        if self.device is None:
            raise Unavailable('vacuum not connected yet')
        return self.device

    def _property(self, device, key):
        if key is None:
            return None
        try:
            return device.get_property(key)
        except Exception:
            return None

    def status(self):
        device = self._device()
        status = device.status
        room = getattr(status, 'current_room', None)
        return {
            'name': _text(device.name),
            'connected': bool(device.available),
            'state': _text(status.state_name),
            'battery': _count(status.battery_level, 100),
            'docked': bool(status.docked),
            'error': _text(status.error_name) if status.has_error else '',
            'suction': _text(status.suction_level_name),
            'current_room': _text(getattr(room, 'name', '')) if room else '',
            'cleaned_area_m2': _count(self._property(device, self.area_key)),
            'cleaning_minutes': _count(self._property(device, self.time_key)),
        }

    def _segments(self, device):
        return getattr(device.status, 'current_segments', None) or {}

    def rooms(self):
        device = self._device()
        segments = self._segments(device)
        rooms = [{'id': int(seg_id), 'name': _text(getattr(seg, 'name', '') or f'Room {seg_id}')}
                 for seg_id, seg in segments.items() if _is_int(seg_id) and 0 < seg_id <= 2147483647]
        return {'rooms': sorted(rooms, key=lambda room: room['id'])[:256]}

    def command(self, body):
        if not isinstance(body, dict) or set(body) != {'action', 'rooms', 'repeats', 'suction'}:
            raise BadRequest('command needs action, rooms, repeats and suction')
        action, rooms, repeats, suction = body['action'], body['rooms'], body['repeats'], body['suction']
        if not isinstance(action, str) or not isinstance(rooms, list) or not all(_is_int(r) for r in rooms) \
                or not _is_int(repeats) or not _is_int(suction):
            raise BadRequest('malformed command')
        device = self._device()
        simple = {
            'start': (device.start, 'Started cleaning'),
            'pause': (device.pause, 'Paused'),
            'stop': (device.stop, 'Stopped'),
            'dock': (device.return_to_base, 'Returning to the dock'),
            'locate': (device.locate, 'Playing the locate sound'),
        }
        with self.lock:
            try:
                if action in simple:
                    call, message = simple[action]
                    call()
                elif action == 'clean_rooms':
                    names = {seg_id: getattr(seg, 'name', '') for seg_id, seg in self._segments(device).items()}
                    device.clean_segment(rooms, cleaning_times=repeats, suction_level=None if suction < 0 else suction)
                    listed = ', '.join(_text(names.get(r) or f'room {r}', 64) for r in rooms)
                    message = f'Cleaning {listed}'
                    if repeats > 1:
                        message += f' ({repeats} passes)'
                    if suction in SUCTION_NAMES:
                        message += f' on {SUCTION_NAMES[suction]}'
                else:
                    raise BadRequest('unsupported action')
            except self.refusal_types as error:
                raise Refused(_text(error)) from error
        return {'accepted': True, 'message': _text(message, 512)}


class Holder:
    """The current broker; empty until the vacuum connects."""

    def __init__(self):
        self._broker = None
        self._lock = threading.Lock()

    def set(self, value):
        with self._lock:
            self._broker = value

    def get(self):
        with self._lock:
            return self._broker


def make_server(path, holder):
    routes = {'/status': lambda b, _: b.status(), '/rooms': lambda b, _: b.rooms(),
              '/command': lambda b, body: b.command(body)}

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def _send(self, code, payload):
            data = json.dumps(payload).encode()
            self.send_response(code)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            self._send(405, {'error': 'use POST'})

        def do_POST(self):
            route = routes.get(self.path)
            if route is None:
                return self._send(404, {'error': 'not found'})
            try:
                length = int(self.headers.get('Content-Length', '0'))
            except ValueError:
                return self._send(400, {'error': 'bad length'})
            if length < 0 or length > MAX_BODY:
                self.close_connection = True
                if 0 < length <= 16 * MAX_BODY:
                    self.rfile.read(length)  # drain so the client sees the reply, not a reset
                return self._send(413, {'error': 'body too large'})
            try:
                body = json.loads(self.rfile.read(length) or b'{}')
            except ValueError:
                return self._send(400, {'error': 'body must be JSON'})
            current = holder.get()
            try:
                if current is None:
                    raise Unavailable('vacuum not connected yet')
                return self._send(200, route(current, body))
            except BadRequest as error:
                return self._send(400, {'error': _text(error)})
            except Refused as error:
                return self._send(409, {'error': _text(error)})
            except Unavailable:
                return self._send(503, {'error': 'vacuum unavailable'})
            except Exception:
                LOG.exception('vacuum request failed')
                return self._send(503, {'error': 'vacuum unavailable'})

    class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
        daemon_threads = True

    if os.path.exists(path):
        os.unlink(path)
    old = os.umask(0o117)
    try:
        server = Server(path, Handler)
    finally:
        os.umask(old)
    os.chmod(path, 0o660)
    return server


def select_device(devices, configured_id):
    """Pick the configured vacuum, or the only one on the account."""
    if configured_id:
        for device in devices:
            if str(device.get('did')) == configured_id:
                return device
        raise ConfigError('DREAME_DEVICE_ID does not match a vacuum on this account')
    if len(devices) == 1:
        return devices[0]
    if not devices:
        raise ConfigError('no supported vacuum found on this account')
    names = ', '.join(f"{d.get('name', 'vacuum')} (id {d.get('did')})" for d in devices)
    raise ConfigError(f'several vacuums found; set DREAME_DEVICE_ID to one of: {names}')


def read_password(env):
    """Read the vendor password from its mounted secret file, never the environment."""
    path = env.get('DREAME_PASSWORD_FILE', '/run/secrets/dreame_password')
    try:
        with open(path, encoding='utf-8') as stream:
            password = stream.read(4096).rstrip('\r\n')
    except OSError:
        raise ConfigError('cannot read the dreame_password file; check it exists in the install folder') from None
    if not password:
        raise ConfigError('the dreame_password file is empty')
    return password


def connect_from_env(env):  # pragma: no cover - needs the vendor library and a real account
    """Log in to the vendor cloud and connect to the configured vacuum."""
    import base64
    import zlib
    sys.path.insert(0, env.get('DREAME_LIBRARY', '/opt/dreame'))
    import dreame
    from dreame import DreameVacuumDevice, DreameVacuumProtocol, DreameVacuumProperty, DEVICE_INFO
    from dreame.exceptions import DeviceException

    account = env.get('DREAME_ACCOUNT_TYPE', 'dreame')
    if account not in ('dreame', 'mova', 'mi'):
        raise ConfigError('DREAME_ACCOUNT_TYPE must be dreame, mova or mi')
    username = env.get('DREAME_USERNAME', '')
    country = env.get('DREAME_COUNTRY', 'eu')
    if not username:
        raise ConfigError('set DREAME_USERNAME')
    password = read_password(env)

    protocol = DreameVacuumProtocol(username=username, password=password, country=country,
                                    prefer_cloud=True, account_type=account)
    protocol.cloud.login()
    if not protocol.cloud.logged_in:
        reason = getattr(protocol.cloud, 'login_error', None) or 'check the account, password, and country'
        raise ConfigError(f'login failed: {reason}')

    info = json.loads(zlib.decompress(base64.b64decode(DEVICE_INFO), zlib.MAX_WBITS | 32))
    prefixes = {1: 'xiaomi', 2: 'mova', 3: 'trouver'}
    models = {}
    for key in info[3]:
        entry = info[0][info[3][key]]
        if entry:
            models[f"{prefixes.get(entry[0], 'dreame')}.vacuum.{key}"] = entry[1]
    supported, _ = protocol.cloud.get_supported_devices(models, None, None, env.get('DREAME_DEVICE_ID') or None)
    for device in supported:
        device.setdefault('name', device.get('customName') or device.get('model', 'vacuum'))
    chosen = select_device(supported, env.get('DREAME_DEVICE_ID', ''))
    auth_key = protocol.cloud.auth_key if getattr(protocol.cloud, 'auth_key', None) else None
    protocol.disconnect()

    if account == 'mi':
        host, token = chosen.get('localip'), chosen.get('token')
    else:
        host, token = chosen.get('bindDomain'), ' '
    device = DreameVacuumDevice(chosen['name'], host, token, chosen.get('mac'), username, password,
                                country, True, account, chosen.get('did'), auth_key)
    device.update()
    device.schedule_update()
    LOG.info('connected to vacuum (library %s)', dreame.VERSION)
    return Broker(device, area_key=DreameVacuumProperty.CLEANED_AREA,
                  time_key=DreameVacuumProperty.CLEANING_TIME, refusal_types=(DeviceException,))


def keep_connected(holder, env, connect=connect_from_env, sleep=time.sleep):  # pragma: no cover
    delay = 30
    while True:
        try:
            holder.set(connect(env))
            return
        except ConfigError as error:
            LOG.error('vacuum setup problem: %s', error)
            delay = 600
        except Exception as error:
            LOG.warning('vacuum connection failed, retrying: %s', _text(error))
        sleep(delay)
        delay = min(delay * 2, 600)


def main():  # pragma: no cover
    logging.basicConfig(level=logging.INFO, format='%(levelname)s %(name)s: %(message)s')
    if len(sys.argv) > 1 and sys.argv[1] == 'health':
        sys.exit(0 if os.path.exists(os.environ.get('NAS_VACUUM_SOCKET', '/ipc/vacuum.sock')) else 1)
    holder = Holder()
    server = make_server(os.environ.get('NAS_VACUUM_SOCKET', '/ipc/vacuum.sock'), holder)
    threading.Thread(target=keep_connected, args=(holder, dict(os.environ)), daemon=True).start()
    server.serve_forever()


if __name__ == '__main__':  # pragma: no cover
    main()
