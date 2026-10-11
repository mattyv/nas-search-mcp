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
