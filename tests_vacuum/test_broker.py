import http.client
import importlib.util
import json
import socket
import threading
from pathlib import Path
from types import SimpleNamespace

import pytest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('vacuum_broker', ROOT / 'vacuum/broker.py')
broker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(broker)


class FakeDeviceError(Exception):
    pass


class FakeDevice:
    def __init__(self):
        self.name = 'Robo'
        self.available = True
        self.calls = []
        kitchen = SimpleNamespace(id=3, name='Kitchen')
        hall = SimpleNamespace(id=5, name='Hall')
        self.status = SimpleNamespace(
            state_name='charging', battery_level=87, docked=True, has_error=False,
            error_name='no_error', suction_level_name='standard', current_room=kitchen,
            current_segments={5: hall, 3: kitchen},
        )
        self.properties = {'cleaned_area': 12, 'cleaning_time': 20}

    def get_property(self, key):
        return self.properties.get(key)

    def start(self): self.calls.append(('start',))
    def pause(self): self.calls.append(('pause',))
    def stop(self): self.calls.append(('stop',))
    def locate(self): self.calls.append(('locate',))
    def return_to_base(self): self.calls.append(('return_to_base',))

    def clean_segment(self, rooms, cleaning_times=None, suction_level=None):
        self.calls.append(('clean_segment', rooms, cleaning_times, suction_level))


def make(device=None):
    return broker.Broker(device or FakeDevice(), area_key='cleaned_area', time_key='cleaning_time')


def test_status_maps_device_fields():
    assert make().status() == {
        'name': 'Robo', 'connected': True, 'state': 'charging', 'battery': 87, 'docked': True,
        'error': '', 'suction': 'standard', 'current_room': 'Kitchen',
        'cleaned_area_m2': 12, 'cleaning_minutes': 20,
    }


def test_status_reports_error_and_tolerates_missing_values():
    device = FakeDevice()
    device.status.has_error = True
    device.status.error_name = 'wheel_stuck'
    device.status.current_room = None
    device.status.battery_level = None
    device.properties = {}
    out = make(device).status()
    assert out['error'] == 'wheel_stuck'
    assert out['current_room'] == ''
    assert out['battery'] == 0 and out['cleaned_area_m2'] == 0


def test_not_connected_is_unavailable():
    with pytest.raises(broker.Unavailable):
        broker.Broker(None).status()


def test_rooms_sorted_by_id():
    assert make().rooms() == {'rooms': [{'id': 3, 'name': 'Kitchen'}, {'id': 5, 'name': 'Hall'}]}


def test_rooms_empty_without_map():
    device = FakeDevice()
    device.status.current_segments = None
    assert make(device).rooms() == {'rooms': []}


@pytest.mark.parametrize('action,call', [
    ('start', ('start',)), ('pause', ('pause',)), ('stop', ('stop',)),
    ('locate', ('locate',)), ('dock', ('return_to_base',)),
])
def test_simple_commands_dispatch(action, call):
    device = FakeDevice()
    out = make(device).command({'action': action, 'rooms': [], 'repeats': 0, 'suction': -1})
    assert device.calls == [call] and out['accepted'] is True and out['message']


def test_clean_rooms_dispatch_keeps_current_suction():
    device = FakeDevice()
    make(device).command({'action': 'clean_rooms', 'rooms': [5, 3], 'repeats': 2, 'suction': -1})
    assert device.calls == [('clean_segment', [5, 3], 2, None)]


def test_clean_rooms_dispatch_with_suction():
    device = FakeDevice()
    out = make(device).command({'action': 'clean_rooms', 'rooms': [3], 'repeats': 1, 'suction': 3})
    assert device.calls == [('clean_segment', [3], 1, 3)]
    assert 'Kitchen' in out['message']


@pytest.mark.parametrize('body', [
    {'action': 'factory_reset', 'rooms': [], 'repeats': 0, 'suction': -1},
    {'action': 'start'},
    {'action': 'clean_rooms', 'rooms': ['3'], 'repeats': 1, 'suction': -1},
    {'action': 'clean_rooms', 'rooms': [3], 'repeats': True, 'suction': -1},
    'start',
])
def test_malformed_commands_rejected(body):
    device = FakeDevice()
    with pytest.raises(broker.BadRequest):
        make(device).command(body)
    assert device.calls == []


def test_vacuum_refusal_reported():
    device = FakeDevice()
    def refuse():
        raise FakeDeviceError('Unavailable while draining')
    device.start = refuse
    b = broker.Broker(device, refusal_types=(FakeDeviceError,))
    with pytest.raises(broker.Refused, match='draining'):
        b.command({'action': 'start', 'rooms': [], 'repeats': 0, 'suction': -1})


def test_select_device_by_configured_id_or_single():
    devices = [{'did': '1', 'name': 'Upstairs'}, {'did': '2', 'name': 'Downstairs'}]
    assert broker.select_device(devices, '2')['name'] == 'Downstairs'
    assert broker.select_device(devices[:1], '')['name'] == 'Upstairs'
    with pytest.raises(broker.ConfigError, match='Upstairs.*Downstairs'):
        broker.select_device(devices, '')
    with pytest.raises(broker.ConfigError):
        broker.select_device(devices, '9')
    with pytest.raises(broker.ConfigError):
        broker.select_device([], '')


@pytest.fixture
def served(tmp_path):
    path = str(tmp_path / 'v.sock')
    holder = broker.Holder()
    holder.set(make())
    server = broker.make_server(path, holder)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield path, holder
    server.shutdown()
    server.server_close()


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('dreame-vacuum', timeout=5)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect(self.path)


def request(path, method, route, body=b'{}'):
    conn = UnixConnection(path)
    conn.request(method, route, body=body, headers={'Content-Type': 'application/json'})
    response = conn.getresponse()
    data = response.read()
    conn.close()
    return response.status, json.loads(data) if data else None


def test_http_routes(served):
    path, _ = served
    status, body = request(path, 'POST', '/status')
    assert status == 200 and body['battery'] == 87
    status, body = request(path, 'POST', '/rooms')
    assert status == 200 and len(body['rooms']) == 2
    status, body = request(path, 'POST', '/command', json.dumps({'action': 'dock', 'rooms': [], 'repeats': 0, 'suction': -1}).encode())
    assert status == 200 and body['accepted'] is True
    assert request(path, 'POST', '/nope')[0] == 404
    assert request(path, 'GET', '/status', None)[0] == 405
    assert request(path, 'POST', '/command', b'not json')[0] == 400
    assert request(path, 'POST', '/command', b'{' + b' ' * 70000 + b'}')[0] == 413


def test_http_unavailable_until_connected(served):
    path, holder = served
    holder.set(None)
    assert request(path, 'POST', '/status')[0] == 503


def test_http_refusal_is_conflict(served):
    path, holder = served
    device = FakeDevice()
    def refuse():
        raise FakeDeviceError('Map not ready')
    device.locate = refuse
    holder.set(broker.Broker(device, refusal_types=(FakeDeviceError,)))
    status, body = request(path, 'POST', '/command', json.dumps({'action': 'locate', 'rooms': [], 'repeats': 0, 'suction': -1}).encode())
    assert status == 409 and body == {'error': 'Map not ready'}


def test_socket_limited_to_service_user_and_group(served):
    import os, stat
    path, _ = served
    assert stat.S_IMODE(os.stat(path).st_mode) == 0o660


def test_password_read_from_secret_file_without_trailing_newline(tmp_path):
    secret = tmp_path / 'dreame_password'
    secret.write_text('pa ss$word\n')
    assert broker.read_password({'DREAME_PASSWORD_FILE': str(secret)}) == 'pa ss$word'


def test_password_never_taken_from_environment(tmp_path):
    with pytest.raises(broker.ConfigError, match='dreame_password'):
        broker.read_password({'DREAME_PASSWORD_FILE': str(tmp_path / 'missing'), 'DREAME_PASSWORD': 'leaked'})


def test_empty_password_file_rejected(tmp_path):
    secret = tmp_path / 'dreame_password'
    secret.write_text('\n')
    with pytest.raises(broker.ConfigError):
        broker.read_password({'DREAME_PASSWORD_FILE': str(secret)})
