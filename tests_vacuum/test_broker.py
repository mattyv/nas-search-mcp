import http.client
import importlib.util
import json
import socket
import threading
from pathlib import Path
import base64
import io
from datetime import datetime, timezone
from types import SimpleNamespace

from PIL import Image

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
        # coords are map-grid cells; x0..y1 are the millimetre box that zones use.
        kitchen = SimpleNamespace(id=3, name='Kitchen', coords=[80, 60, 140, 110], x0=0, y0=0, x1=3000, y1=2500)
        hall = SimpleNamespace(id=5, name='Hall', coords=None, x0=None, y0=None, x1=None, y1=None)
        dimensions = SimpleNamespace(left=-4000, top=-3000, width=160, height=120, grid_size=50)
        self.status = SimpleNamespace(
            state_name='charging', battery_level=87, docked=True, has_error=False,
            error_name='no_error', suction_level_name='standard', current_room=kitchen,
            current_segments={5: hall, 3: kitchen},
            current_map=SimpleNamespace(dimensions=dimensions, empty_map=False, obstacles={
                '2': SimpleNamespace(type=SimpleNamespace(name='SOCK'), x=120.4, y=-40, possibility=87, file_name='abc.jpg', picture_status=SimpleNamespace(value=2)),
                '4': SimpleNamespace(type=SimpleNamespace(name='WIRE'), x=0, y=0, possibility=60, file_name=None, picture_status=None),
            }),
            main_brush_life=80, side_brush_life=None, filter_life=12, mop_life=55,
            clean_water_tank_status_name='installed', dirty_water_tank_status_name='installed',
            dust_bag_status_name=None, washing=False, drying=True, drying_left_time=95,
            cleaning_mode_name='sweeping_and_mopping', mop_pad_humidity_name='moist',
            volume=40, dnd=True, dnd_start='22:00', dnd_end='08:00',
            carpet_avoidance=False, carpet_recognition=True, wetness_level=None,
            shortcuts={
                32: SimpleNamespace(name='After dinner', running=False, tasks=[[SimpleNamespace(segment_id=3, order=1), SimpleNamespace(segment_id=5, order=2)]]),
                40: SimpleNamespace(name='Bathroom mop', running=True, tasks=None),
            },
            _cleaning_history=[
                SimpleNamespace(date=datetime(2026, 11, 1, 9, 0, tzinfo=timezone.utc), cleaning_time=30, cleaned_area=40, completed=True, status=None),
                SimpleNamespace(date=datetime(2026, 11, 3, 9, 0, tzinfo=timezone.utc), cleaning_time=45, cleaned_area=52, completed=False, status=None),
            ],
        )
        self.capability = SimpleNamespace(wetness_level=False, self_wash_base=True, mop_clean_frequency=False)
        self.properties = {'cleaned_area': 12, 'cleaning_time': 20}

    def __getattr__(self, name):
        # Every library setter/action the broker may call is recorded.
        if name.startswith(('set_', 'start_', 'stop_')):
            return lambda *args, **kwargs: self.calls.append((name, *args))
        raise AttributeError(name)

    def clean_zone(self, zones, cleaning_times, suction_level, water_volume):
        self.calls.append(('clean_zone', zones, cleaning_times, suction_level, water_volume))

    def clean_spot(self, points, cleaning_times, suction_level, water_volume):
        self.calls.append(('clean_spot', points, cleaning_times, suction_level, water_volume))

    def go_to(self, x, y):
        self.calls.append(('go_to', x, y))

    def remote_control_move_step(self, rotation, velocity):
        self.calls.append(('move', rotation, velocity))

    def get_property(self, key):
        return self.properties.get(key)

    def start(self): self.calls.append(('start',))
    def pause(self): self.calls.append(('pause',))
    def stop(self): self.calls.append(('stop',))
    def locate(self): self.calls.append(('locate',))
    def return_to_base(self): self.calls.append(('return_to_base',))

    def clean_segment(self, rooms, cleaning_times=None, suction_level=None):
        self.calls.append(('clean_segment', rooms, cleaning_times, suction_level))


class FakeVendor:
    def __init__(self):
        self.calls = []
        self.lock = 0

    def empty_dustbin(self, device):
        self.calls.append('empty_dustbin')

    def child_lock(self, device):
        return self.lock

    def set_child_lock(self, device, on):
        self.calls.append(('child_lock', on))

    def obstacle_photo(self, device, index):
        self.calls.append(('obstacle_photo', index))
        picture = io.BytesIO()
        Image.new('RGB', (2400, 1800), 'gray').save(picture, 'JPEG')
        return picture.getvalue()

    def render_map(self, device):
        picture = io.BytesIO()
        Image.new('RGB', (3000, 1500), 'white').save(picture, 'PNG')
        return picture.getvalue()


def make(device=None, vendor=None):
    return broker.Broker(device or FakeDevice(), area_key='cleaned_area', time_key='cleaning_time',
                         vendor=vendor or FakeVendor())


def cmd(action, rooms=(), zones=(), point=None, repeats=0, suction=-1):
    return {'action': action, 'rooms': list(rooms), 'zones': list(zones), 'point': point,
            'repeats': repeats, 'suction': suction}


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
    assert 'position' not in out


def test_status_reports_vacuum_position_on_the_map():
    device = FakeDevice()
    device.status.current_map.robot_position = SimpleNamespace(x=1234.6, y=-56, a=90)
    assert make(device).status()['position'] == [1235, -56]
    device.status.current_map.robot_position = None
    assert 'position' not in make(device).status()


def test_not_connected_is_unavailable():
    with pytest.raises(broker.Unavailable):
        broker.Broker(None).status()


def test_rooms_sorted_by_id_with_boxes_and_bounds():
    assert make().rooms() == {
        'rooms': [{'id': 3, 'name': 'Kitchen', 'box': [0, 0, 3000, 2500]}, {'id': 5, 'name': 'Hall'}],
        'bounds': [-4000, -3000, 4000, 3000],
    }


def test_rooms_empty_without_map():
    device = FakeDevice()
    device.status.current_segments = None
    device.status.current_map = None
    assert make(device).rooms() == {'rooms': []}


@pytest.mark.parametrize('action,call', [
    ('start', ('start',)), ('pause', ('pause',)), ('stop', ('stop',)),
    ('locate', ('locate',)), ('dock', ('return_to_base',)),
    ('wash_mops', ('start_washing',)), ('dry_mops', ('start_drying',)), ('stop_drying', ('stop_drying',)),
])
def test_simple_commands_dispatch(action, call):
    device = FakeDevice()
    out = make(device).command(cmd(action))
    assert device.calls == [call] and out['accepted'] is True and out['message']


def test_clean_rooms_dispatch_keeps_current_suction():
    device = FakeDevice()
    make(device).command(cmd('clean_rooms', rooms=[5, 3], repeats=2))
    assert device.calls == [('clean_segment', [5, 3], 2, None)]


def test_clean_rooms_dispatch_with_suction():
    device = FakeDevice()
    out = make(device).command(cmd('clean_rooms', rooms=[3], repeats=1, suction=3))
    assert device.calls == [('clean_segment', [3], 1, 3)]
    assert 'Kitchen' in out['message']


@pytest.mark.parametrize('body', [
    cmd('factory_reset'),
    {'action': 'start'},
    cmd('clean_rooms', rooms=['3'], repeats=1),
    cmd('clean_rooms', rooms=[3], repeats=True),
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
    b = broker.Broker(device, refusal_types=(FakeDeviceError,), vendor=FakeVendor())
    with pytest.raises(broker.Refused, match='draining'):
        b.command(cmd('start'))


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
    status, body = request(path, 'POST', '/command', json.dumps(cmd('dock')).encode())
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
    holder.set(broker.Broker(device, refusal_types=(FakeDeviceError,), vendor=FakeVendor()))
    status, body = request(path, 'POST', '/command', json.dumps(cmd('locate')).encode())
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


def test_empty_dustbin_uses_vendor_action():
    vendor = FakeVendor()
    out = make(vendor=vendor).command(cmd('empty_dustbin'))
    assert vendor.calls == ['empty_dustbin'] and out['accepted']


def test_zone_and_spot_dispatch():
    device = FakeDevice()
    b = make(device)
    b.command(cmd('clean_zones', zones=[[0, 0, 1000, 1000]], repeats=2, suction=1))
    b.command(cmd('clean_spot', point=[100, 200], repeats=1))
    assert device.calls == [('clean_zone', [[0, 0, 1000, 1000]], 2, 1, None),
                            ('clean_spot', [[100, 200]], 1, None, None)]


@pytest.mark.parametrize('body', [
    cmd('clean_zones', zones=[[0, 0, 1000]]),
    cmd('clean_zones', zones=[['0', 0, 1, 1]]),
    cmd('clean_spot', point=[1]),
    {'action': 'start', 'rooms': [], 'repeats': 0, 'suction': -1},
])
def test_malformed_targets_rejected(body):
    device = FakeDevice()
    with pytest.raises(broker.BadRequest):
        make(device).command(body)
    assert device.calls == []


@pytest.mark.parametrize('name,value,room,call', [
    ('cleaning_mode', 3, 0, ('set_cleaning_mode', 3)),
    ('suction', 2, 0, ('set_suction_level', 2)),
    ('mop_wetness', 2, 0, ('set_mop_pad_humidity', 3)),
    ('volume', 40, 0, ('set_volume', 40)),
    ('dnd', 1, 0, ('set_dnd', True)),
    ('dnd_start', 1350, 0, ('set_dnd_start', '22:30')),
    ('dnd_end', 485, 0, ('set_dnd_end', '08:05')),
    ('carpet_avoidance', 0, 0, ('set_carpet_avoidance', False)),
    ('carpet_recognition', 1, 0, ('set_carpet_recognition', 1)),
    ('room_mode', 1, 3, ('set_segment_cleaning_mode', 3, 1)),
    ('room_suction', 3, 3, ('set_segment_suction_level', 3, 3)),
    ('room_wetness', 0, 3, ('set_segment_mop_pad_humidity', 3, 1)),
    ('room_passes', 2, 3, ('set_segment_cleaning_times', 3, 2)),
])
def test_settings_dispatch(name, value, room, call):
    device = FakeDevice()
    out = make(device).setting({'name': name, 'value': value, 'room': room})
    assert device.calls == [call] and out['accepted'] and out['message']


def test_wetness_follows_model_capability():
    device = FakeDevice()
    device.capability.wetness_level = True
    make(device).setting({'name': 'mop_wetness', 'value': 2, 'room': 0})
    make(device).setting({'name': 'room_wetness', 'value': 0, 'room': 3})
    device.capability.wetness_level = False
    device.capability.self_wash_base = False
    make(device).setting({'name': 'mop_wetness', 'value': 1, 'room': 0})
    assert device.calls == [('set_wetness_level', 27), ('set_segment_wetness_level', 3, 6), ('set_water_volume', 2)]


def test_child_lock_uses_vendor_property():
    vendor = FakeVendor()
    make(vendor=vendor).setting({'name': 'child_lock', 'value': 1, 'room': 0})
    assert vendor.calls == [('child_lock', True)]


@pytest.mark.parametrize('body', [
    {'name': 'firmware', 'value': 1, 'room': 0},
    {'name': 'volume', 'value': '40', 'room': 0},
    {'name': 'volume', 'value': 40},
    ['volume', 40],
])
def test_malformed_settings_rejected(body):
    device = FakeDevice()
    with pytest.raises(broker.BadRequest):
        make(device).setting(body)
    assert device.calls == []


def test_details_report_present_fields_only():
    out = make().details()
    assert out['consumables'] == {'main_brush': 80, 'filter': 12, 'mop_pad': 55}
    assert out['station']['clean_water_tank'] == 'installed' and out['station']['drying'] == 'yes'
    assert out['station']['drying_minutes_left'] == '95' and 'dust_bag' not in out['station']
    assert out['settings']['cleaning_mode'] == 'sweeping_and_mopping'
    assert out['settings']['mop_wetness'] == 'moist' and out['settings']['volume'] == '40'
    assert out['settings']['dnd'] == 'on' and out['settings']['dnd_start'] == '22:00'
    assert out['settings']['child_lock'] == 'off' and out['settings']['carpet_recognition'] == 'on'


def test_history_newest_first_and_limited():
    out = make().history({'limit': 1})
    assert out == {'entries': [{'time': int(datetime(2026, 11, 3, 9, 0, tzinfo=timezone.utc).timestamp()),
                                'minutes': 45, 'area_m2': 52, 'completed': False}]}
    with pytest.raises(broker.BadRequest):
        make().history({'limit': 'ten'})


def test_map_is_png_scaled_down():
    out = make().map()
    assert out['mime_type'] == 'image/png'
    picture = Image.open(io.BytesIO(base64.b64decode(out['data'])))
    assert picture.format == 'PNG' and max(picture.size) <= 1600


def test_map_unavailable_without_saved_map():
    device = FakeDevice()
    device.status.current_map = None
    with pytest.raises(broker.Refused):
        make(device).map()


def test_http_new_routes(served):
    path, _ = served
    assert request(path, 'POST', '/details')[0] == 200
    status, body = request(path, 'POST', '/history', json.dumps({'limit': 5}).encode())
    assert status == 200 and len(body['entries']) == 2
    assert request(path, 'POST', '/map')[0] == 200
    status, body = request(path, 'POST', '/setting', json.dumps({'name': 'volume', 'value': 30, 'room': 0}).encode())
    assert status == 200 and body['accepted'] is True


class StopLoop(Exception):
    pass


def stop_after_first(_delay):
    raise StopLoop


@pytest.mark.parametrize('error,expected', [
    (broker.ConfigError('login failed: check the account, password, and country'), 'login failed: check the account, password, and country'),
    (broker.VacuumOffline('no reply'), 'signed in, but the vacuum did not respond; check it is powered on and connected to Wi-Fi'),
    (RuntimeError('socket secret-ish detail'), 'could not reach the vacuum or the Dreame cloud; retrying'),
])
def test_connection_problem_is_reported_plainly(error, expected):
    holder = broker.Holder()
    def connect(_env):
        raise error
    with pytest.raises(StopLoop):
        broker.keep_connected(holder, {}, connect=connect, sleep=stop_after_first)
    assert holder.problem() == expected
    assert 'secret-ish' not in holder.problem()


def test_problem_cleared_once_connected():
    holder = broker.Holder()
    holder.set_problem('login failed')
    broker.keep_connected(holder, {}, connect=lambda _env: make(), sleep=stop_after_first)
    assert holder.get() is not None and holder.problem() == ''


def test_http_unavailable_explains_why(served):
    path, holder = served
    holder.set(None)
    holder.set_problem('login failed: check the account, password, and country')
    status, body = request(path, 'POST', '/status')
    assert status == 503 and body == {'error': 'login failed: check the account, password, and country'}


def test_rejected_password_stops_retrying_to_protect_the_account():
    holder = broker.Holder()
    attempts = []
    def connect(_env):
        attempts.append(1)
        raise broker.PasswordRejected('user password not match')
    def sleep(_delay):
        raise AssertionError('must not retry a rejected password')
    broker.keep_connected(holder, {}, connect=connect, sleep=sleep)
    assert len(attempts) == 1 and holder.get() is None
    assert 'rejected the password' in holder.problem() and 'stopped trying' in holder.problem()


def test_network_trouble_keeps_retrying():
    holder = broker.Holder()
    def connect(_env):
        raise broker.CloudUnreachable('timed out')
    with pytest.raises(StopLoop):
        broker.keep_connected(holder, {}, connect=connect, sleep=stop_after_first)
    assert holder.problem() == 'could not reach the Dreame cloud; retrying'


def test_go_to_dispatch():
    device = FakeDevice()
    out = make(device).command(cmd('go_to', point=[1500, -200]))
    assert device.calls == [('go_to', 1500, -200)] and 'Driving' in out['message']


def test_move_step_dispatch_and_shape():
    device = FakeDevice()
    out = make(device).move({'rotation': -90, 'velocity': 0})
    assert device.calls == [('move', -90, 0)] and out['accepted']
    for body in [{'rotation': '1', 'velocity': 0}, {'rotation': 1}, []]:
        with pytest.raises(broker.BadRequest):
            make(device).move(body)


def test_obstacles_listed_with_photo_flag():
    assert make().obstacles() == {'obstacles': [
        {'index': 2, 'type': 'sock', 'x': 120, 'y': -40, 'confidence': 87, 'has_photo': True},
        {'index': 4, 'type': 'wire', 'x': 0, 'y': 0, 'confidence': 60, 'has_photo': False},
    ]}


def test_obstacles_empty_without_map():
    device = FakeDevice()
    device.status.current_map = None
    assert make(device).obstacles() == {'obstacles': []}


def test_obstacle_photo_is_scaled_jpeg():
    vendor = FakeVendor()
    out = make(vendor=vendor).obstacle_photo({'index': 2})
    picture = Image.open(io.BytesIO(base64.b64decode(out['data'])))
    assert out['mime_type'] == 'image/jpeg' and picture.format == 'JPEG' and max(picture.size) <= 1280
    assert vendor.calls == [('obstacle_photo', 2)]


def test_obstacle_photo_missing_is_refused():
    vendor = FakeVendor()
    vendor.obstacle_photo = lambda device, index: None
    with pytest.raises(broker.Refused):
        make(vendor=vendor).obstacle_photo({'index': 2})
    with pytest.raises(broker.BadRequest):
        make().obstacle_photo({'index': 'two'})


def test_http_drive_and_obstacle_routes(served):
    path, _ = served
    assert request(path, 'POST', '/move', json.dumps({'rotation': 10, 'velocity': 100}).encode())[0] == 200
    status, body = request(path, 'POST', '/obstacles')
    assert status == 200 and len(body['obstacles']) == 2
    assert request(path, 'POST', '/obstacle_photo', json.dumps({'index': 2}).encode())[0] == 200


def test_shortcuts_listed_with_room_names():
    assert make().shortcuts() == {'shortcuts': [
        {'id': 32, 'name': 'After dinner', 'running': False, 'rooms': ['Kitchen', 'Hall']},
        {'id': 40, 'name': 'Bathroom mop', 'running': True, 'rooms': []},
    ]}


@pytest.mark.parametrize('body,call', [
    ({'action': 'start', 'id': 32, 'name': ''}, ('start_shortcut', 32)),
    ({'action': 'rename', 'id': 32, 'name': 'Evening tidy'}, ('set_rename_shortcut',)),
    ({'action': 'delete', 'id': 40, 'name': ''}, ('delete_shortcut', 40)),
])
def test_shortcut_dispatch(body, call):
    device = FakeDevice()
    device.rename_shortcut = lambda i, n: device.calls.append(('rename_shortcut', i, n))
    device.delete_shortcut = lambda i: device.calls.append(('delete_shortcut', i))
    out = make(device).shortcut(body)
    expected = ('rename_shortcut', 32, 'Evening tidy') if call == ('set_rename_shortcut',) else call
    assert device.calls == [expected] and out['accepted'] and out['message']


@pytest.mark.parametrize('body', [
    {'action': 'start', 'id': '32', 'name': ''},
    {'action': 'start', 'id': 32},
    {'action': 'launch', 'id': 32, 'name': ''},
])
def test_malformed_shortcut_rejected(body):
    device = FakeDevice()
    with pytest.raises(broker.BadRequest):
        make(device).shortcut(body)
    assert device.calls == []


spec_cat = importlib.util.spec_from_file_location('vacuum_catalogue_b', ROOT / 'vacuum/catalogue.py')
catalogue = importlib.util.module_from_spec(spec_cat)
spec_cat.loader.exec_module(catalogue)
FIXTURES = Path(__file__).resolve().parent / 'fixtures'


class OptionVendor(FakeVendor):
    """Stands in for the library's property tables."""

    def __init__(self, values):
        super().__init__()
        self.values = values

    def prop_present(self, device, family, prop):
        return (family, prop) in self.values

    def prop_value(self, device, family, prop):
        return self.values.get((family, prop))

    def set_prop(self, device, family, prop, value):
        device.calls.append(('set_property', family, prop, value))


class OptionDevice(FakeDevice):
    def set_carpet_avoidance(self, value):
        self.calls.append(('set_carpet_avoidance', value))

    def set_off_peak_charging_start(self, value):
        self.calls.append(('set_off_peak_charging_start', value))


def option_broker():
    device = OptionDevice()
    device.status.mop_pressure_list = {'light': 1, 'medium': 2, 'strong': 3}
    device.status.mop_pressure_name = 'medium'
    device.status.mop_cleaning_remainder = 30
    device.status.self_clean_time = 20
    device.status.self_clean_time_min = 10
    device.status.self_clean_time_max = 50
    device.status.off_peak_charging_start = '22:00'
    device.status.battery_charge_level = 90
    vendor = OptionVendor({('property', 'CHILD_LOCK'): 0, ('auto_switch', 'STAIN_AVOIDANCE'): 2,
                           ('property', 'MOP_PRESSURE'): 2, ('property', 'BATTERY_CHARGE_LEVEL'): 90,
                           ('property', 'MOP_CLEANING_REMAINDER'): 30})
    b = broker.Broker(device, vendor=vendor, catalogue=catalogue.load(FIXTURES))
    return b, device


def test_options_report_only_settings_this_vacuum_has():
    b, _ = option_broker()
    found = {o['name']: o for o in b.options()['options']}
    assert 'ai_pet_detection' not in found
    assert found['child_lock'] == {'name': 'child_lock', 'kind': 'switch', 'current': 'off',
                                   'choices': [{'label': 'off', 'value': 0}, {'label': 'on', 'value': 1}]}
    assert found['stain_avoidance']['current'] == 'on'
    assert found['stain_avoidance']['choices'] == [{'label': 'off', 'value': 1}, {'label': 'on', 'value': 2}]
    assert found['carpet_avoidance']['current'] == 'off'
    assert found['mop_pressure']['current'] == 'medium'
    assert [c['label'] for c in found['mop_pressure']['choices']] == ['light', 'medium', 'strong']
    assert found['battery_charge_level']['choices'] == [{'label': '80%', 'value': 80}, {'label': '90%', 'value': 90},
                                                       {'label': '100%', 'value': 100}]
    assert found['mop_cleaning_remainder'] == {'name': 'mop_cleaning_remainder', 'kind': 'number', 'current': '30',
                                               'min': 0, 'max': 180, 'step': 15}
    assert (found['self_clean_time']['min'], found['self_clean_time']['max'], found['self_clean_time']['step']) == (10, 50, 5)
    assert found['off_peak_charging_start'] == {'name': 'off_peak_charging_start', 'kind': 'time', 'current': '22:00'}


@pytest.mark.parametrize('name,value,call', [
    ('child_lock', 1, ('set_property', 'property', 'CHILD_LOCK', 1)),
    ('stain_avoidance', 1, ('set_property', 'auto_switch', 'STAIN_AVOIDANCE', 1)),
    ('carpet_avoidance', 1, ('set_carpet_avoidance', 1)),
    ('mop_pressure', 3, ('set_property', 'property', 'MOP_PRESSURE', 3)),
    ('mop_cleaning_remainder', 45, ('set_property', 'property', 'MOP_CLEANING_REMAINDER', 45)),
    ('off_peak_charging_start', 1410, ('set_off_peak_charging_start', '23:30')),
])
def test_set_option_follows_the_original(name, value, call):
    b, device = option_broker()
    out = b.set_option({'name': name, 'value': value})
    assert device.calls == [call] and out['accepted'] and out['message']


@pytest.mark.parametrize('body', [{'name': 'ai_pet_detection', 'value': 1}, {'name': 'nope', 'value': 1},
                                  {'name': 'child_lock', 'value': '1'}, {'name': 'child_lock'}])
def test_set_option_rejects_unknown_or_malformed(body):
    b, device = option_broker()
    with pytest.raises(broker.BadRequest):
        b.set_option(body)
    assert device.calls == []


def test_camera_light_auto_reads_and_writes_brightness():
    b, device = option_broker()
    b.catalogue['camera_light_brightness_auto'] = {'name': 'camera_light_brightness_auto', 'kind': 'switch',
                                                    'family': None, 'prop': None}
    device.status.camera_light_brightness = 101
    device.set_camera_light_brightness = lambda v: device.calls.append(('set_camera_light_brightness', v))
    found = {o['name']: o for o in b.options()['options']}
    assert found['camera_light_brightness_auto']['current'] == 'on'
    b.set_option({'name': 'camera_light_brightness_auto', 'value': 40})
    assert device.calls == [('set_camera_light_brightness', 40)]


def button_broker():
    device = OptionDevice()
    device.clear_warning = lambda: device.calls.append(('clear_warning',))
    device.start_draining = lambda clean=False: device.calls.append(('start_draining', clean))
    device.status.sensor_dirty_life = 70
    vendor = OptionVendor({})
    vendor.call_action = lambda d, action: d.calls.append(('call_action', action))
    return broker.Broker(device, vendor=vendor, buttons=catalogue.load_buttons(FIXTURES)), device


def test_buttons_hide_resets_for_parts_this_model_lacks():
    b, device = button_broker()
    names = [x['name'] for x in b.buttons()['buttons']]
    assert 'reset_main_brush' in names            # main_brush_life is 80
    assert 'clear_warning' in names and 'start_mapping' in names and 'empty_water_tank' in names
    device.status.main_brush_life = None
    assert 'reset_main_brush' not in [x['name'] for x in b.buttons()['buttons']]


@pytest.mark.parametrize('name,call', [
    ('reset_main_brush', ('call_action', 'RESET_MAIN_BRUSH')),
    ('clear_warning', ('clear_warning',)),
    ('empty_water_tank', ('start_draining', True)),
])
def test_press_calls_the_library_like_the_original(name, call):
    b, device = button_broker()
    out = b.press({'name': name})
    assert device.calls == [call] and out['accepted'] and out['message']


@pytest.mark.parametrize('body', [{'name': 'factory_reset'}, {'name': 3}, []])
def test_press_rejects_unknown(body):
    b, device = button_broker()
    with pytest.raises(broker.BadRequest):
        b.press(body)
    assert device.calls == []


class RoomDevice(OptionDevice):
    def set_segment_mop_pressure(self, room, value):
        self.calls.append(('set_segment_mop_pressure', room, value))

    def set_segment_cleaning_times(self, room, value):
        self.calls.append(('set_segment_cleaning_times', room, value))

    def set_segment_mop_type(self, room, value):
        self.calls.append(('set_segment_mop_type', room, value))

    def set_segment_wetness_level(self, room, value):
        self.calls.append(('set_segment_wetness_level', room, value))


def room_broker():
    device = RoomDevice()
    kitchen = device.status.current_segments[3]
    kitchen.mop_pressure, kitchen.cleaning_times, kitchen.mop_type, kitchen.wetness_level = 2, 1, 'B', 10
    device.status.mop_pressure_list = {'light': 1, 'medium': 2}
    ground = SimpleNamespace(map_id=1, custom_name='Ground', map_name=None, recovery_map_list=[
        SimpleNamespace(date='2026-11-01 09:00'), SimpleNamespace(date='2026-11-10 09:00')])
    upstairs = SimpleNamespace(map_id=2, custom_name=None, map_name='Map 2', recovery_map_list=None)
    device.status.map_data_list = {1: ground, 2: upstairs}
    device.status.selected_map = ground
    device.status.has_temporary_map = False
    vendor = OptionVendor({})
    vendor.room_types = lambda: {0: 'Room', 4: 'Kitchen'}
    b = broker.Broker(device, vendor=vendor, room_catalogue=catalogue.load(FIXTURES, rooms=True))
    return b, device


def test_maps_listed_with_backups():
    b, _ = room_broker()
    assert b.maps() == {'maps': [
        {'id': 1, 'name': 'Ground', 'selected': True, 'backups': [{'index': 1, 'date': '2026-11-01 09:00'},
                                                                   {'index': 2, 'date': '2026-11-10 09:00'}]},
        {'id': 2, 'name': 'Map 2', 'selected': False, 'backups': []}], 'temporary_map': False}


@pytest.mark.parametrize('body,call', [
    ({'action': 'select', 'map_id': 2, 'name': '', 'backup': 0}, ('set_selected_map', 2)),
    ({'action': 'rename', 'map_id': 1, 'name': 'Down', 'backup': 0}, ('rename_map', 1, 'Down')),
    ({'action': 'delete', 'map_id': 2, 'name': '', 'backup': 0}, ('delete_map', 2)),
    ({'action': 'backup', 'map_id': 1, 'name': '', 'backup': 0}, ('backup_map', 1)),
    ({'action': 'restore', 'map_id': 1, 'name': '', 'backup': 2}, ('restore_map', 2, 1)),
    ({'action': 'request', 'map_id': 0, 'name': '', 'backup': 0}, ('request_map',)),
    ({'action': 'save_temporary', 'map_id': 0, 'name': '', 'backup': 0}, ('save_temporary_map',)),
    ({'action': 'discard_temporary', 'map_id': 0, 'name': '', 'backup': 0}, ('discard_temporary_map',)),
    ({'action': 'replace_temporary', 'map_id': 1, 'name': '', 'backup': 0}, ('replace_temporary_map', 1)),
])
def test_map_actions_dispatch(body, call):
    b, device = room_broker()
    for method in ('set_selected_map', 'rename_map', 'delete_map', 'backup_map', 'restore_map', 'request_map',
                   'save_temporary_map', 'discard_temporary_map', 'replace_temporary_map'):
        setattr(device, method, (lambda m: lambda *a: device.calls.append((m, *a)))(method))
    assert b.manage_map(body)['accepted'] and device.calls == [call]


@pytest.mark.parametrize('body,call', [
    ({'action': 'rename', 'rooms': [3], 'name': 'Galley', 'type': 0, 'line': None}, ('set_segment_name', 3, 0, 'Galley')),
    ({'action': 'set_type', 'rooms': [3], 'name': '', 'type': 4, 'line': None}, ('set_segment_name', 3, 4)),
    ({'action': 'merge', 'rooms': [3, 5], 'name': '', 'type': 0, 'line': None}, ('merge_segments', None, [3, 5])),
    ({'action': 'split', 'rooms': [3], 'name': '', 'type': 0, 'line': [0, 0, 10, 0]}, ('split_segments', None, 3, [0, 0, 10, 0])),
    ({'action': 'order', 'rooms': [5, 3], 'name': '', 'type': 0, 'line': None}, ('set_cleaning_sequence', [5, 3])),
    ({'action': 'hide', 'rooms': [5], 'name': '', 'type': 0, 'line': None}, ('set_hidden_segments', [5])),
])
def test_room_actions_dispatch(body, call):
    b, device = room_broker()
    for method in ('set_segment_name', 'merge_segments', 'split_segments', 'set_cleaning_sequence', 'set_hidden_segments'):
        setattr(device, method, (lambda m: lambda *a: device.calls.append((m, *a)))(method))
    assert b.manage_rooms(body)['accepted'] and device.calls == [call]


def test_room_options_and_types():
    b, _ = room_broker()
    out = b.room_options({'room': 3})
    found = {o['name']: o for o in out['options']}
    assert set(found) == {'mop_pressure', 'cleaning_times', 'mop_type', 'wetness_level'}
    assert found['mop_pressure']['current'] == 'medium'
    assert found['cleaning_times']['choices'] == [{'label': '1x', 'value': 1}, {'label': '2x', 'value': 2}, {'label': '3x', 'value': 3}]
    assert found['mop_type']['current'] == 'b' and found['mop_type']['choices'][1] == {'label': 'b', 'value': 2}
    assert (found['wetness_level']['min'], found['wetness_level']['max']) == (1, 32)
    assert out['types'] == [{'label': 'Room', 'value': 0}, {'label': 'Kitchen', 'value': 4}]


@pytest.mark.parametrize('name,value,call', [
    ('mop_pressure', 1, ('set_segment_mop_pressure', 3, 1)),
    ('cleaning_times', 2, ('set_segment_cleaning_times', 3, 2)),
    ('mop_type', 3, ('set_segment_mop_type', 3, 'C')),
    ('wetness_level', 20, ('set_segment_wetness_level', 3, 20)),
])
def test_set_room_option_dispatch(name, value, call):
    b, device = room_broker()
    assert b.set_room_option({'room': 3, 'name': name, 'value': value})['accepted'] and device.calls == [call]


def test_room_option_for_unknown_room_rejected():
    b, device = room_broker()
    with pytest.raises(broker.BadRequest):
        b.set_room_option({'room': 9, 'name': 'mop_pressure', 'value': 1})


def area(x0, y0, x1, y1):
    """A library-style four-cornered area."""
    return SimpleNamespace(x0=x0, y0=y0, x1=x1, y1=y0, x2=x1, y2=y1, x3=x0, y3=y1)


def line(x0, y0, x1, y1):
    return SimpleNamespace(x0=x0, y0=y0, x1=x1, y1=y1)


class EditDevice(FakeDevice):
    def __init__(self):
        super().__init__()
        current = self.status.current_map
        current.no_go_areas = [area(0, 0, 500, 400)]
        current.no_mopping_areas = [area(1000, 1000, 1500, 1500)]
        current.virtual_walls = [line(0, 0, 0, 900)]
        current.virtual_thresholds = []
        current.passable_thresholds = [line(10, 10, 60, 10)]
        current.impassable_thresholds = []
        current.ramps = [area(-100, -100, 0, 0)]
        current.curtains = None
        current.low_lying_areas = []
        current.carpets = [area(200, 200, 300, 300)]
        current.deleted_carpets = [area(400, 400, 450, 450)]
        current.predefined_points = {1: SimpleNamespace(x=10, y=20)}
        current.router_position = SimpleNamespace(x=5, y=6)
        for method in ('set_restricted_zone', 'set_virtual_threshold', 'set_threshold', 'set_curtain',
                       'set_low_lying_area', 'set_carpet_area', 'set_predefined_points', 'set_router_position',
                       'follow_path', 'set_obstacle_ignore'):
            setattr(self, method, (lambda m: lambda *a: self.calls.append((m, *a)))(method))


def test_map_elements_read_every_kind():
    out = make(EditDevice()).map_elements()
    assert out['no_go_zones'] == [[0, 0, 500, 400]]
    assert out['virtual_walls'] == [[0, 0, 0, 900]]
    assert out['ramps'] == [[-100, -100, 0, 0]]
    assert out['curtains'] == [] and out['carpets'] == [[200, 200, 300, 300]]
    assert out['predefined_points'] == [[10, 20]] and out['router_position'] == [[5, 6]]


@pytest.mark.parametrize('element,items,call', [
    # Editing one kind always resends the current others, because the library clears what is left out.
    ('no_go_zones', [[0, 0, 100, 100]], ('set_restricted_zone', [[0, 0, 0, 900]], [[0, 0, 100, 100]], [[1000, 1000, 1500, 1500]])),
    ('no_mop_zones', [], ('set_restricted_zone', [[0, 0, 0, 900]], [[0, 0, 500, 400]], [])),
    ('virtual_walls', [[1, 1, 2, 2]], ('set_restricted_zone', [[1, 1, 2, 2]], [[0, 0, 500, 400]], [[1000, 1000, 1500, 1500]])),
    ('passable_thresholds', [], ('set_threshold', [], [], [[-100, -100, 0, 0]])),
    ('ramps', [[0, 0, 50, 50]], ('set_threshold', [[10, 10, 60, 10]], [], [[0, 0, 50, 50]])),
    ('virtual_thresholds', [[0, 0, 10, 0]], ('set_virtual_threshold', [[0, 0, 10, 0]])),
    ('curtains', [[0, 0, 10, 0]], ('set_curtain', [[0, 0, 10, 0]])),
    ('low_lying_areas', [[0, 0, 10, 10]], ('set_low_lying_area', [[0, 0, 10, 10]])),
    ('carpets', [[0, 0, 10, 10]], ('set_carpet_area', [[0, 0, 10, 10]], [[400, 400, 450, 450]])),
    ('predefined_points', [[1, 2], [3, 4]], ('set_predefined_points', [[1, 2], [3, 4]])),
    ('router_position', [[7, 8]], ('set_router_position', 7, 8)),
])
def test_edit_map_dispatch_keeps_other_kinds(element, items, call):
    device = EditDevice()
    out = make(device).edit_map({'element': element, 'items': items})
    assert device.calls == [call] and out['accepted']


@pytest.mark.parametrize('body', [
    {'element': 'furniture', 'items': []},
    {'element': 'deleted_carpets', 'items': []},
    {'element': 'no_go_zones', 'items': [[0, 0, 1]]},
    {'element': 'no_go_zones', 'items': [['0', 0, 1, 1]]},
    {'element': 'router_position', 'items': []},
])
def test_edit_map_rejects_malformed(body):
    device = EditDevice()
    with pytest.raises(broker.BadRequest):
        make(device).edit_map(body)
    assert device.calls == []


def test_patrol_and_ignore_obstacle():
    device = EditDevice()
    b = make(device)
    b.patrol({'points': [[0, 0], [100, 100]]})
    b.ignore_obstacle({'x': 10, 'y': 20, 'ignore': True})
    assert device.calls == [('follow_path', [[0, 0], [100, 100]]), ('set_obstacle_ignore', 10, 20, True)]
    with pytest.raises(broker.BadRequest):
        b.patrol({'points': [[0]]})
    with pytest.raises(broker.BadRequest):
        b.ignore_obstacle({'x': 1, 'y': 2, 'ignore': 'yes'})
