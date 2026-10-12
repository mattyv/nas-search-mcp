import importlib.util
import json
from pathlib import Path

import pytest


def load():
    spec = importlib.util.spec_from_file_location('fetch', Path(__file__).parents[1] / 'scripts/fetch-file.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_download_reassembles_original_and_tracks_version(tmp_path):
    import base64
    module = load()
    calls = []
    class Remote:
        def request(self, message):
            calls.append(message)
            if message.get('method') != 'tools/call':
                return {'result': {}}
            args = message['params']['arguments']
            offset = args['offset']
            if offset:
                assert args['expected_version'] == 'v1'
            data = b'%PDF\x00original'[offset:offset + 4]
            next_offset = offset + len(data)
            return {'result': {'structuredContent': {'data': base64.b64encode(data).decode(), 'offset': offset, 'size': 13, 'version': 'v1', 'next_offset': next_offset if next_offset < 13 else None, 'complete': next_offset >= 13}}}
    output = tmp_path / 'scan.pdf'
    module.download(Remote(), 'scan.pdf', output)
    assert output.read_bytes() == b'%PDF\x00original'
    assert output.stat().st_mode & 0o077 == 0
    assert calls[0]['method'] == 'initialize'


def test_failed_transfer_preserves_existing_file(tmp_path):
    module = load()
    output = tmp_path / 'scan.pdf'
    output.write_bytes(b'keep')
    class Remote:
        def request(self, message):
            if message.get('method') == 'tools/call':
                return {'result': {'isError': True, 'content': [{'text': 'remote failure'}]}}
            return {'result': {}}
    with pytest.raises(module.DownloadError):
        module.download(Remote(), 'scan.pdf', output, replace=True)
    assert output.read_bytes() == b'keep'
    assert list(tmp_path.iterdir()) == [output]


def test_local_download_tool_writes_original_and_preserves_existing(tmp_path, monkeypatch):
    import base64
    module = load().bridge
    calls = []
    def request(remote, message):
        calls.append(message)
        result = {}
        if message['method'] == 'tools/list':
            result = {'tools': [{'name': 'fetch_file'}]}
        elif message['method'] == 'tools/call':
            assert message['params']['name'] == 'fetch_file'
            result = {'structuredContent': {'data': base64.b64encode(b'original').decode(), 'offset': 0, 'size': 8, 'version': 'v1', 'complete': True}}
        return {'jsonrpc': '2.0', 'id': message.get('id'), 'result': result}
    monkeypatch.setattr(module.Bridge, '_request', request)
    remote = module.Bridge('http://localhost/mcp', 'token')
    listed = remote.request({'jsonrpc': '2.0', 'id': 1, 'method': 'tools/list'})
    assert [tool['name'] for tool in listed['result']['tools']] == ['fetch_file', 'download_file']
    output = tmp_path / 'original.bin'
    message = {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/call', 'params': {'name': 'download_file', 'arguments': {'path': 'Matthew/original.bin', 'output': str(output)}}}
    reply = remote.request(message)
    assert not reply['result'].get('isError')
    assert reply['result']['structuredContent'] == {'path': str(output), 'bytes': 8}
    assert output.read_bytes() == b'original'
    assert all(call.get('params', {}).get('name') != 'download_file' for call in calls)
    reply = remote.request(message)
    assert reply['result']['isError']
    assert output.read_bytes() == b'original'


@pytest.mark.parametrize('arguments', [{}, {'path': 'file', 'output': 'relative'}, {'path': '', 'output': '/tmp/file'}, {'path': 'file', 'output': 2}, {'path': 'file', 'output': '/tmp/file', 'replace': True}])
def test_local_download_rejects_invalid_arguments(arguments, monkeypatch):
    module = load().bridge
    monkeypatch.setattr(module.Bridge, '_request', lambda *args: pytest.fail('invalid download must not reach NAS'))
    remote = module.Bridge('http://localhost/mcp', 'token')
    reply = remote.request({'jsonrpc': '2.0', 'id': 3, 'method': 'tools/call', 'params': {'name': 'download_file', 'arguments': arguments}})
    assert reply['result']['isError']


def test_local_download_failure_removes_partial_file(tmp_path, monkeypatch):
    import base64
    module = load().bridge
    def request(remote, message):
        if message['method'] != 'tools/call':
            return {'result': {}}
        if message['params']['arguments']['offset']:
            return {'result': {'isError': True}}
        return {'result': {'structuredContent': {'data': base64.b64encode(b'part').decode(), 'offset': 0, 'size': 8, 'version': 'v1', 'next_offset': 4}}}
    monkeypatch.setattr(module.Bridge, '_request', request)
    remote = module.Bridge('http://localhost/mcp', 'token')
    reply = remote.request({'jsonrpc': '2.0', 'id': 3, 'method': 'tools/call', 'params': {'name': 'download_file', 'arguments': {'path': 'file', 'output': str(tmp_path / 'file')}}})
    assert reply['result']['isError']
    assert list(tmp_path.iterdir()) == []
