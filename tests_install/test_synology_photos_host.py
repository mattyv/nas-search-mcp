import importlib.util
import json
from pathlib import Path

import pytest


def module():
    path = Path(__file__).parents[1] / 'scripts/synology-photos-host.py'
    spec = importlib.util.spec_from_file_location('photos_host', path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def test_native_and_cgi_wrapper_responses():
    host = module()
    good = json.dumps({'success': True, 'data': {'list': [{'id': 3753}]}})
    assert host.parse_response(good.encode()) == [{'id': 3753}]
    wrapper = json.dumps({'success': False, 'error': {'code': 117}, 'httpd_restart': False})
    assert host.parse_response(('Content-type: application/json; charset="UTF-8"\n\n' + good + wrapper).encode()) == [{'id': 3753}]


@pytest.mark.parametrize('body', [b'{}', b'{"success":false,"error":{"code":105}}', b'not json',
                                  b'{"success":true,"data":{"list":[]}} trailing',
                                  b'{"success":true,"data":{"list":[]}}{"success":false,"error":{"code":117}}'])
def test_unknown_or_ambiguous_responses_fail_closed(body):
    with pytest.raises(ValueError):
        module().parse_response(body)


def test_people_filter_is_bounded_and_hides_other_fields():
    host = module()
    calls = []
    def api(name, method, version, params):
        calls.append((name, method, version, params))
        return [{'id': 1, 'name': 'Matt', 'item_count': 3, 'show': True},
                {'id': 3753, 'name': 'Lucas', 'item_count': 2037, 'show': True, 'private': 'unused'},
                {'id': 4, 'name': 'Lucas hidden', 'item_count': 1, 'show': False}]
    result = host.read_only_request('/people', {'query': 'lucas', 'offset': 0, 'limit': 3}, api)
    assert result == {'people': [{'id': 3753, 'name': 'Lucas', 'item_count': 2037}],
                      'offset': 0, 'next_offset': 3, 'truncated': True}
    assert calls == [('SYNO.Foto.Browse.Person', 'list', 1, {'offset': 0, 'limit': 3})]


def photo(person=3753):
    return {'id': 89027, 'filename': 'IMG_3577.HEIC', 'time': 1766247560, 'type': 'photo',
            'folder_id': 2194, 'owner_user_id': 1,
            'additional': {'person': [{'id': person, 'name': 'Lucas'}]}}


def test_photo_membership_and_metadata_contract():
    host = module()
    calls = []
    def api(name, method, version, params):
        calls.append((name, method, version, params))
        return [photo()]
    result = host.read_only_request('/photos', {'person_id': 3753, 'offset': 0, 'limit': 20}, api)
    assert result == {'photos': [{'id': 89027, 'filename': 'IMG_3577.HEIC', 'taken_time': 1766247560, 'type': 'photo'}],
                      'offset': 0, 'next_offset': None, 'truncated': False}
    assert calls == [('SYNO.Foto.Browse.Item', 'list', 2,
                      {'person_id': 3753, 'offset': 0, 'limit': 20, 'additional': ['person']})]


def test_ignored_person_filter_is_not_reported_as_a_match():
    with pytest.raises(ValueError):
        module().read_only_request('/photos', {'person_id': 3753, 'offset': 0, 'limit': 20},
                                   lambda *args: [photo(2)])


@pytest.mark.parametrize('path,body', [('/delete', {}), ('/people', {'runner': 'admin'}),
    ('/people', {'offset': -1}), ('/people', {'limit': 101}), ('/people', {'limit': True}),
    ('/people', {'query': 'x' * 101}), ('/photos', {'person_id': 0}),
    ('/photos', {'person_id': True}), ('/photos', {'person_id': 2147483648}),
    ('/photos', {'person_id': 3753, 'api': 'SYNO.Core.User'})])
def test_privileged_boundary_rejects_unbounded_or_extra_inputs(path, body):
    called = []
    with pytest.raises(ValueError):
        module().read_only_request(path, body, lambda *args: called.append(args) or [])
    assert called == []


def test_filesystem_paths_never_appear_in_photo_metadata():
    item = photo()
    item['filename'] = '../../secret'
    with pytest.raises(ValueError):
        module().read_only_request('/photos', {'person_id': 3753}, lambda *args: [item])


def test_cli_invocation_has_fixed_runner_and_no_shell(monkeypatch):
    host = module()
    calls = []
    def invoke(args):
        calls.append(args)
        return b'{"success":true,"data":{"list":[]}}'
    monkeypatch.setattr(host, 'invoke', invoke)
    host.call_api('SYNO.Foto.Browse.Person', 'list', 1, {'offset': 0, 'limit': 20})
    assert calls == [['/usr/syno/bin/synowebapi', '--exec', 'api=SYNO.Foto.Browse.Person',
                     'method=list', 'version=1', 'runner=matt', 'offset=0', 'limit=20']]


def test_real_unix_http_boundary(monkeypatch):
    import collections
    import http.client
    import os
    import socket
    import socketserver
    import tempfile
    import threading
    host = module()
    monkeypatch.setattr(host, 'invoke', lambda args: b'{"success":true,"data":{"list":[{"id":3753,"name":"Lucas","item_count":2037,"show":true}]}}')
    with tempfile.TemporaryDirectory(prefix='photos-http-') as directory:
        path = str(Path(directory) / 'photos.sock')
        with socketserver.UnixStreamServer(path, host.Handler) as server:
            server.app_uid = os.getuid()
            server.requests = collections.deque()
            server.access_allowed = lambda: True
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            def request(route, body, headers):
                connection = http.client.HTTPConnection('synology-photos', timeout=3)
                connection.sock = socket.socket(socket.AF_UNIX)
                connection.sock.connect(path)
                connection.request('POST', route, body=body, headers=headers)
                response = connection.getresponse()
                result = response.status, json.loads(response.read())
                connection.close()
                return result
            try:
                status, body = request('/people', '{"query":"lucas"}', {'Content-Type': 'application/json'})
                assert status == 200 and body['people'][0]['name'] == 'Lucas'
                assert request('/delete', '{}', {'Content-Type': 'application/json'})[0] == 503
                assert request('/people', '{}', {'Content-Type': 'text/plain'})[0] == 503
                assert request('/people', '{"runner":"admin"}', {'Content-Type': 'application/json'})[0] == 503
                server.access_allowed = lambda: False
                assert request('/people', '{}', {'Content-Type': 'application/json'})[0] == 403
            finally:
                server.shutdown()
                thread.join(timeout=3)


def test_invoke_timeout_does_not_expose_process_output(monkeypatch):
    import subprocess
    host = module()
    def timeout(*args, **kwargs):
        raise subprocess.TimeoutExpired(args[0], 25, output=b'private-response')
    monkeypatch.setattr(host.subprocess, 'run', timeout)
    with pytest.raises(ValueError) as error:
        host.invoke(['/fixed/command'])
    assert 'private-response' not in str(error.value)


def test_preview_revalidates_photo_and_person_before_reading_bytes():
    import base64
    host = module()
    item = photo()
    item['additional']['thumbnail'] = {'m': 'ready', 'unit_id': 89027, 'cache_key': '89027_1766247560'}
    calls = []
    def get(api, method, version, params):
        calls.append((api, method, version, params))
        return [item]
    def image(unit, cache):
        assert (unit, cache) == (89027, '89027_1766247560')
        return b'\xff\xd8small-preview\xff\xd9'
    result = host.read_only_request('/preview', {'person_id': 3753, 'photo_id': 89027}, get, image)
    assert result == {'mime_type': 'image/jpeg', 'data': base64.b64encode(image(89027, '89027_1766247560')).decode()}
    assert calls == [('SYNO.Foto.Browse.Item', 'get', 2, {'id': [89027], 'additional': ['person', 'thumbnail']})]


@pytest.mark.parametrize('mutation', ['person', 'photo', 'unit', 'cache', 'readiness'])
def test_preview_never_trusts_arbitrary_ids_or_cache_paths(mutation):
    host = module()
    item = photo()
    item['additional']['thumbnail'] = {'m': 'ready', 'unit_id': 89027, 'cache_key': '89027_123'}
    if mutation == 'person': item['additional']['person'][0]['id'] = 2
    if mutation == 'photo': item['id'] = 3
    if mutation == 'unit': item['additional']['thumbnail']['unit_id'] = 4
    if mutation == 'cache': item['additional']['thumbnail']['cache_key'] = '../../secret'
    if mutation == 'readiness': item['additional']['thumbnail']['m'] = 'converting'
    called = []
    with pytest.raises(ValueError):
        host.read_only_request('/preview', {'person_id': 3753, 'photo_id': 89027}, lambda *args: [item],
                               lambda *args: called.append(args) or b'')
    assert not called


@pytest.mark.parametrize('image', [b'<html>error</html>', b'\xff\xd8truncated', b'\xff\xd8' + b'x' * (512*1024) + b'\xff\xd9'], ids=['html', 'truncated', 'oversize'])
def test_preview_rejects_non_images_and_oversize_images(image):
    host = module()
    item = photo()
    item['additional']['thumbnail'] = {'m': 'ready', 'unit_id': 89027, 'cache_key': '89027_123'}
    with pytest.raises(ValueError):
        host.read_only_request('/preview', {'person_id': 3753, 'photo_id': 89027}, lambda *args: [item], lambda *args: image)


def test_thumbnail_accepts_image_stdout_when_outfile_has_only_wrapper_json(monkeypatch):
    host = module()
    image = b'\xff\xd8preview\xff\xd9'
    wrapper = b'{"success":false,"error":{"code":117},"httpd_restart":false}'
    def invoke(args):
        output = Path(next(arg.split('=', 1)[1] for arg in args if arg.startswith('outfile=')))
        assert output.parent.stat().st_mode & 0o077 == 0
        assert 'runner=matt' in args and 'api=SYNO.Foto.Thumbnail' in args
        output.write_bytes(wrapper)
        return b'Content-type: image/jpeg\r\n\r\n' + image + wrapper
    monkeypatch.setattr(host, 'invoke', invoke)
    assert host.thumbnail(89027, '89027_123') == image


def test_thumbnail_uses_valid_private_outfile(monkeypatch):
    host = module()
    image = b'\xff\xd8preview\xff\xd9'
    def invoke(args):
        output = Path(next(arg.split('=', 1)[1] for arg in args if arg.startswith('outfile=')))
        output.write_bytes(image)
        return b'{"success":true}'
    monkeypatch.setattr(host, 'invoke', invoke)
    assert host.thumbnail(89027, '89027_123') == image


@pytest.mark.parametrize('contents,allowed', [
    ('NAS_ALLOWED_FOLDERS=Matthew,Code,Photos\n', True),
    ("NAS_ALLOWED_FOLDERS='Photos'\nNAS_DENIED_FOLDERS=Code/private\n", True),
    ('NAS_ALLOWED_FOLDERS=\n', False), ('', False),
    ('NAS_ALLOWED_FOLDERS=Photos/subfolder\n', False),
    ('NAS_ALLOWED_FOLDERS=Photos\nNAS_DENIED_FOLDERS=Photos/private\n', False),
    ('NAS_ALLOWED_FOLDERS=Photos\nNAS_DENIED_FOLDERS=Photos\n', False),
    ('NAS_ALLOWED_FOLDERS=Photos\nNAS_ALLOWED_FOLDERS=\n', False),
    ('export NAS_ALLOWED_FOLDERS=Photos\n', False)])
def test_host_honors_current_photos_access_restrictions(contents, allowed):
    assert module().policy_allows(contents) is allowed


@pytest.mark.parametrize('field,value', [('id', True), ('id', 1.0), ('unit', True), ('unit', 1.0)])
def test_preview_ids_reject_boolean_and_float_aliases(field, value):
    item = photo()
    item['id'] = value if field == 'id' else 1
    item['additional']['thumbnail'] = {'m': 'ready', 'unit_id': value if field == 'unit' else 1, 'cache_key': '1_123'}
    with pytest.raises(ValueError):
        module().read_only_request('/preview', {'person_id': 3753, 'photo_id': 1}, lambda *args: [item],
                                   lambda *args: b'\xff\xd8preview\xff\xd9')
