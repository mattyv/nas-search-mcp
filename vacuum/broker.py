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


class VacuumOffline(Exception):
    """Signed in to the vendor cloud, but the vacuum itself did not answer."""


class PasswordRejected(ConfigError):
    """The vendor rejected the credentials. Retrying would count towards its account lockout."""


class CloudUnreachable(Exception):
    """Sign-in failed for a reason other than the credentials, such as the network."""


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


# Parts whose remaining life the library reports as a percentage.
CONSUMABLES = {
    'main_brush': 'main_brush_life', 'side_brush': 'side_brush_life', 'filter': 'filter_life',
    'sensor': 'sensor_dirty_life', 'mop_pad': 'mop_life', 'tank_filter': 'tank_filter_life',
    'silver_ion': 'silver_ion_life', 'detergent': 'detergent_life', 'squeegee': 'squeegee_life',
    'deodorizer': 'deodorizer_life', 'wheel': 'wheel_dirty_life', 'scale_inhibitor': 'scale_inhibitor_life',
    'onboard_dirty_water_tank': 'onboard_dirty_water_tank_life',
    'dirty_water_channel': 'dirty_water_channel_dirty_life',
}
STATION = {
    'clean_water_tank': 'clean_water_tank_status_name', 'dirty_water_tank': 'dirty_water_tank_status_name',
    'dust_bag': 'dust_bag_status_name', 'detergent': 'detergent_status_name',
    'self_wash_base': 'self_wash_base_status_name', 'auto_empty': 'auto_empty_status_name',
}
MODE_NAMES = {0: 'sweeping', 1: 'mopping', 2: 'sweeping and mopping', 3: 'mopping after sweeping'}
WETNESS_NAMES = {0: 'dry', 1: 'moist', 2: 'wet'}
COMMAND_KEYS = {'action', 'rooms', 'zones', 'point', 'repeats', 'suction'}


def _on(value):
    return 'on' if value else 'off'


def _clock(minutes):
    return f'{minutes // 60:02d}:{minutes % 60:02d}'


class Broker:
    def __init__(self, device, area_key=None, time_key=None, refusal_types=(), vendor=None):
        self.device = device
        self.area_key = area_key
        self.time_key = time_key
        self.refusal_types = tuple(refusal_types)
        # Library calls that need its enums: empty_dustbin, child lock, map rendering.
        self.vendor = vendor
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

    def _run(self, call):
        with self.lock:
            try:
                return call()
            except self.refusal_types as error:
                raise Refused(_text(error)) from error

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

    def _bounds(self, device):
        current = getattr(device.status, 'current_map', None)
        dims = getattr(current, 'dimensions', None) if current and not getattr(current, 'empty_map', False) else None
        if dims is None:
            return None
        return [int(dims.left), int(dims.top), int(dims.left + dims.width * dims.grid_size),
                int(dims.top + dims.height * dims.grid_size)]

    def rooms(self):
        device = self._device()
        rooms = []
        for seg_id, seg in self._segments(device).items():
            if not (_is_int(seg_id) and 0 < seg_id <= 2147483647):
                continue
            room = {'id': int(seg_id), 'name': _text(getattr(seg, 'name', '') or f'Room {seg_id}')}
            # x0..y1 are the room's millimetre box (seg.coords is in map-grid cells).
            box = [getattr(seg, key, None) for key in ('x0', 'y0', 'x1', 'y1')]
            if all(isinstance(c, (int, float)) for c in box) and box[0] < box[2] and box[1] < box[3]:
                room['box'] = [int(c) for c in box]
            rooms.append(room)
        out = {'rooms': sorted(rooms, key=lambda room: room['id'])[:256]}
        bounds = self._bounds(device)
        if bounds and bounds[0] < bounds[2] and bounds[1] < bounds[3]:
            out['bounds'] = bounds
        return out

    def command(self, body):
        if not isinstance(body, dict) or set(body) != COMMAND_KEYS:
            raise BadRequest('command needs action, rooms, zones, point, repeats and suction')
        action, rooms, zones, point = body['action'], body['rooms'], body['zones'], body['point']
        repeats, suction = body['repeats'], body['suction']
        if not isinstance(action, str) or not isinstance(rooms, list) or not all(_is_int(r) for r in rooms) \
                or not isinstance(zones, list) or not all(isinstance(z, list) and len(z) == 4 and all(_is_int(c) for c in z) for z in zones) \
                or not (point is None or (isinstance(point, list) and len(point) == 2 and all(_is_int(c) for c in point))) \
                or not _is_int(repeats) or not _is_int(suction):
            raise BadRequest('malformed command')
        device = self._device()
        level = None if suction < 0 else suction
        simple = {
            'start': (device.start, 'Started cleaning'),
            'pause': (device.pause, 'Paused'),
            'stop': (device.stop, 'Stopped'),
            'dock': (device.return_to_base, 'Returning to the dock'),
            'locate': (device.locate, 'Playing the locate sound'),
            'empty_dustbin': (lambda: self.vendor.empty_dustbin(device), 'Emptying the dustbin'),
            'wash_mops': (device.start_washing, 'Washing the mops'),
            'dry_mops': (device.start_drying, 'Drying the mops'),
            'stop_drying': (device.stop_drying, 'Stopped drying the mops'),
        }
        if action in simple:
            call, message = simple[action]
            self._run(call)
        elif action == 'clean_rooms':
            names = {seg_id: getattr(seg, 'name', '') for seg_id, seg in self._segments(device).items()}
            self._run(lambda: device.clean_segment(rooms, cleaning_times=repeats, suction_level=level))
            message = 'Cleaning ' + ', '.join(_text(names.get(r) or f'room {r}', 64) for r in rooms)
        elif action == 'clean_zones':
            self._run(lambda: device.clean_zone(zones, repeats, level, None))
            message = f'Cleaning {len(zones)} zone' + ('s' if len(zones) > 1 else '')
        elif action == 'clean_spot' and point is not None:
            self._run(lambda: device.clean_spot([point], repeats, level, None))
            message = 'Cleaning the spot'
        else:
            raise BadRequest('unsupported action')
        if action in ('clean_rooms', 'clean_zones', 'clean_spot'):
            if repeats > 1:
                message += f' ({repeats} passes)'
            if suction in SUCTION_NAMES:
                message += f' on {SUCTION_NAMES[suction]}'
        return {'accepted': True, 'message': _text(message, 512)}

    def _wetness(self, device, index, room=None):
        """Map dry/moist/wet onto whichever water control this model has."""
        capability = device.capability
        if getattr(capability, 'wetness_level', False):
            top = 15 if getattr(capability, 'mop_clean_frequency', False) else 32
            value = {0: round(top * 0.2), 1: round(top * 0.5), 2: round(top * 0.85)}[index]
            return (lambda: device.set_segment_wetness_level(room, value)) if room else (lambda: device.set_wetness_level(value))
        value = index + 1
        if getattr(capability, 'self_wash_base', False):
            return (lambda: device.set_segment_mop_pad_humidity(room, value)) if room else (lambda: device.set_mop_pad_humidity(value))
        return (lambda: device.set_segment_water_volume(room, value)) if room else (lambda: device.set_water_volume(value))

    def setting(self, body):
        if not isinstance(body, dict) or set(body) != {'name', 'value', 'room'} or not isinstance(body['name'], str) \
                or not _is_int(body['value']) or not _is_int(body['room']):
            raise BadRequest('setting needs name, value and room')
        name, value, room = body['name'], body['value'], body['room']
        device = self._device()
        calls = {
            'cleaning_mode': (lambda: device.set_cleaning_mode(value), f'Cleaning mode set to {MODE_NAMES.get(value)}'),
            'suction': (lambda: device.set_suction_level(value), f'Suction set to {SUCTION_NAMES.get(value)}'),
            'volume': (lambda: device.set_volume(value), f'Volume set to {value}'),
            'dnd': (lambda: device.set_dnd(bool(value)), f'Do not disturb {_on(value)}'),
            'dnd_start': (lambda: device.set_dnd_start(_clock(value)), f'Do not disturb starts at {_clock(value)}'),
            'dnd_end': (lambda: device.set_dnd_end(_clock(value)), f'Do not disturb ends at {_clock(value)}'),
            'child_lock': (lambda: self.vendor.set_child_lock(device, bool(value)), f'Child lock {_on(value)}'),
            'carpet_avoidance': (lambda: device.set_carpet_avoidance(bool(value)), f'Carpet avoidance {_on(value)}'),
            'carpet_recognition': (lambda: device.set_carpet_recognition(value), f'Carpet recognition {_on(value)}'),
            'room_mode': (lambda: device.set_segment_cleaning_mode(room, value), f'Room mode set to {MODE_NAMES.get(value)}'),
            'room_suction': (lambda: device.set_segment_suction_level(room, value), f'Room suction set to {SUCTION_NAMES.get(value)}'),
            'room_passes': (lambda: device.set_segment_cleaning_times(room, value), f'Room passes set to {value}'),
        }
        if name in ('mop_wetness', 'room_wetness') and value in WETNESS_NAMES:
            call = self._wetness(device, value, room if name == 'room_wetness' else None)
            message = f'Mop wetness set to {WETNESS_NAMES[value]}'
        elif name in calls:
            call, message = calls[name]
        else:
            raise BadRequest('unsupported setting')
        self._run(call)
        return {'accepted': True, 'message': _text(message, 512)}

    def details(self):
        device = self._device()
        status = device.status
        consumables = {}
        for key, attr in CONSUMABLES.items():
            value = getattr(status, attr, None)
            if _is_int(value) or isinstance(value, float):
                consumables[key] = max(0, min(100, int(value)))
        station = {}
        for key, attr in STATION.items():
            value = getattr(status, attr, None)
            if value:
                station[key] = _text(value, 128)
        for key in ('washing', 'drying'):
            value = getattr(status, key, None)
            if value is not None:
                station[key] = 'yes' if value else 'no'
        left = getattr(status, 'drying_left_time', None)
        if _is_int(left) and left > 0:
            station['drying_minutes_left'] = str(left)
        settings = {}
        readers = {
            'cleaning_mode': lambda: getattr(status, 'cleaning_mode_name', None),
            'suction': lambda: getattr(status, 'suction_level_name', None),
            'mop_wetness': lambda: getattr(status, 'mop_pad_humidity_name', None)
            if getattr(device.capability, 'self_wash_base', False) else getattr(status, 'water_volume_name', None),
            'wetness_level': lambda: getattr(status, 'wetness_level', None),
            'volume': lambda: getattr(status, 'volume', None),
            'dnd': lambda: None if getattr(status, 'dnd', None) is None else _on(status.dnd),
            'dnd_start': lambda: getattr(status, 'dnd_start', None),
            'dnd_end': lambda: getattr(status, 'dnd_end', None),
            'child_lock': lambda: None if self.vendor.child_lock(device) is None else _on(self.vendor.child_lock(device)),
            'carpet_avoidance': lambda: _on(getattr(status, 'carpet_avoidance', False)),
            'carpet_recognition': lambda: _on(getattr(status, 'carpet_recognition', False)),
        }
        for key, read in readers.items():
            try:
                value = read()
            except Exception:
                value = None
            if value is not None and value != '':
                settings[key] = _text(value, 128)
        return {'consumables': consumables, 'station': station, 'settings': settings}

    def history(self, body):
        limit = body.get('limit') if isinstance(body, dict) else None
        if not _is_int(limit) or not 1 <= limit <= 20:
            raise BadRequest('limit must be 1-20')
        runs = [run for run in (getattr(self._device().status, '_cleaning_history', None) or [])
                if getattr(run, 'date', None) is not None]
        runs.sort(key=lambda run: run.date.timestamp(), reverse=True)
        entries = []
        for run in runs[:limit]:
            entry = {'time': int(run.date.timestamp()), 'minutes': _count(run.cleaning_time),
                     'area_m2': _count(run.cleaned_area)}
            if run.completed is not None:
                entry['completed'] = bool(run.completed)
            if run.status is not None:
                entry['status'] = _text(getattr(run.status, 'name', run.status), 128).replace('_', ' ').lower()
            entries.append(entry)
        return {'entries': entries}

    def map(self):
        import base64
        import io
        from PIL import Image
        device = self._device()
        if self._bounds(device) is None:
            raise Refused('the vacuum has no saved map yet')
        with self.lock:
            data = self.vendor.render_map(device)
        picture = Image.open(io.BytesIO(data))
        picture.thumbnail((1600, 1600))
        out = io.BytesIO()
        picture.save(out, 'PNG')
        return {'mime_type': 'image/png', 'data': base64.b64encode(out.getvalue()).decode()}


class Holder:
    """The current broker; empty until the vacuum connects."""

    def __init__(self):
        self._broker = None
        self._problem = 'starting up and connecting to the vacuum'
        self._lock = threading.Lock()

    def set(self, value):
        with self._lock:
            self._broker = value
            if value is not None:
                self._problem = ''

    def set_problem(self, text):
        with self._lock:
            self._problem = _text(text)

    def problem(self):
        with self._lock:
            return self._problem

    def get(self):
        with self._lock:
            return self._broker


def make_server(path, holder):
    routes = {'/status': lambda b, _: b.status(), '/rooms': lambda b, _: b.rooms(),
              '/command': lambda b, body: b.command(body), '/setting': lambda b, body: b.setting(body),
              '/details': lambda b, _: b.details(), '/history': lambda b, body: b.history(body),
              '/map': lambda b, _: b.map()}

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
                # A plain reason the owner can act on; never library internals.
                return self._send(503, {'error': holder.problem() or 'vacuum unavailable'})
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


class DreameVendor:
    """The few broker calls that need the vendor library's own enums or renderer."""

    def __init__(self, action, prop, renderer_type):
        self.action = action
        self.prop = prop
        self.renderer_type = renderer_type
        self.renderer = None

    def empty_dustbin(self, device):
        device.call_action(self.action.START_AUTO_EMPTY)

    def child_lock(self, device):
        value = device.get_property(self.prop.CHILD_LOCK)
        return None if value is None else bool(value)

    def set_child_lock(self, device, on):
        device.set_property(self.prop.CHILD_LOCK, 1 if on else 0)

    def render_map(self, device):
        if self.renderer is None:
            # Low resolution keeps rendering memory small on the NAS.
            self.renderer = self.renderer_type(robot_type=device.capability.robot_type, low_resolution=True)
        status = device.status
        return self.renderer.render_map(device.get_map_for_render(status.current_map),
                                        status.robot_status, status.station_status)


def connect_from_env(env):  # pragma: no cover - needs the vendor library and a real account
    """Log in to the vendor cloud and connect to the configured vacuum."""
    import base64
    import zlib
    sys.path.insert(0, env.get('DREAME_LIBRARY', '/opt/dreame'))
    import dreame
    from dreame import DreameVacuumDevice, DreameVacuumProtocol, DreameVacuumProperty, DreameVacuumAction, DEVICE_INFO
    from dreame.exceptions import DeviceException
    from dreame.map import DreameVacuumMapRenderer

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
        if getattr(protocol.cloud, 'auth_failed', False):
            raise PasswordRejected('the account, password, or region did not match')
        raise CloudUnreachable(getattr(protocol.cloud, 'login_error', None) or 'sign-in did not complete')

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
    try:
        device.update()
    except DeviceException as error:
        raise VacuumOffline(str(error)) from error
    device.schedule_update()
    LOG.info('connected to vacuum (library %s)', dreame.VERSION)
    return Broker(device, area_key=DreameVacuumProperty.CLEANED_AREA,
                  time_key=DreameVacuumProperty.CLEANING_TIME, refusal_types=(DeviceException,),
                  vendor=DreameVendor(DreameVacuumAction, DreameVacuumProperty, DreameVacuumMapRenderer))


def keep_connected(holder, env, connect=connect_from_env, sleep=time.sleep):
    delay = 30
    while True:
        try:
            holder.set(connect(env))
            return
        except PasswordRejected as error:
            # Dreame locks the account after a few wrong passwords; never retry on our own.
            LOG.error('Dreame rejected the sign-in (%s); not retrying until the service restarts', error)
            holder.set_problem('Dreame rejected the password, so the vacuum service stopped trying to avoid locking the account. '
                               'Check DREAME_USERNAME, DREAME_COUNTRY and the dreame_password file, then restart the vacuum service')
            return
        except CloudUnreachable as error:
            LOG.warning('could not reach the Dreame cloud, retrying: %s', _text(error))
            holder.set_problem('could not reach the Dreame cloud; retrying')
        except ConfigError as error:
            LOG.error('vacuum setup problem: %s', error)
            holder.set_problem(str(error))
            delay = 600
        except VacuumOffline as error:
            LOG.warning('vacuum did not respond, retrying: %s', _text(error))
            holder.set_problem('signed in, but the vacuum did not respond; check it is powered on and connected to Wi-Fi')
        except Exception as error:
            LOG.warning('vacuum connection failed, retrying: %s', _text(error))
            holder.set_problem('could not reach the vacuum or the Dreame cloud; retrying')
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
