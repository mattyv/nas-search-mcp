import importlib.util
import io
import json
import tarfile
from pathlib import Path

import pytest


def module():
    spec = importlib.util.spec_from_file_location('upgrade_host', Path(__file__).parents[1] / 'scripts/upgrade-host.py')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def test_env_is_data_and_preserves_spaces(tmp_path):
    env = tmp_path / '.env'
    env.write_text("NAS_INDEX_PATH='/volume1/My Index'\nNAS_TOKEN=$(touch /tmp/never)\nNAS_UID=1000\n")
    assert module().read_env(env)['NAS_INDEX_PATH'] == '/volume1/My Index'


@pytest.mark.parametrize('name,kind', [('../escape', 'file'), ('root/link', 'link'), ('/absolute', 'file')])
def test_archive_rejects_escape_and_links(tmp_path, name, kind):
    archive = tmp_path / 'a.tar.gz'
    with tarfile.open(archive, 'w:gz') as tar:
        info = tarfile.TarInfo(name)
        if kind == 'link':
            info.type = tarfile.SYMTYPE
            info.linkname = '/etc/passwd'
        tar.addfile(info)
    with pytest.raises(ValueError):
        module().extract_archive(archive, tmp_path / 'out')


def test_requires_exact_successful_main_push_and_publication():
    m = module()
    sha = 'a' * 40
    run = {'head_sha': sha, 'head_branch': 'main', 'event': 'push', 'status': 'completed', 'conclusion': 'success', 'name': 'Verify and test'}
    jobs = [{'name': 'verify', 'conclusion': 'success'}, {'name': 'publish', 'conclusion': 'success'}]
    assert m.verified_run(run, jobs, sha)
    for key, value in [('head_sha', 'b'*40), ('event', 'pull_request'), ('status', 'in_progress')]:
        assert not m.verified_run(dict(run, **{key: value}), jobs, sha)
    assert not m.verified_run(run, jobs[:1], sha)


def test_process_uses_fixed_commit_and_keeps_errors_private(tmp_path, monkeypatch):
    m = module()
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    install = tmp_path / 'install'
    install.mkdir()
    data = tmp_path / 'index'
    data.mkdir()
    (install / '.env').write_text(f"NAS_INDEX_PATH='{data}'\nNAS_DOCUMENTS_PATH=/m\nNAS_PHOTOS_PATH=/p\nNAS_CODE_PATH=/c\nNAS_UID=1\nNAS_GID=2\nNAS_BIND_IP=127.0.0.1\n")
    queue = install / 'upgrades'
    queue.mkdir()
    (queue / 'request.json').write_text('{"action":"upgrade_main"}')
    monkeypatch.setattr(m, 'resolve_verified_main', lambda: 'a'*40)
    def fail(*args):
        raise RuntimeError('secret token')
    monkeypatch.setattr(m, 'install_commit', fail)
    assert m.process_once(install)
    status = json.loads((queue / 'status.json').read_text())
    assert status['state'] == 'failed'
    assert status['commit'] == 'a'*40
    assert 'secret' not in json.dumps(status)
    assert not (queue / 'request.json').exists()


def test_install_pins_download_and_image_without_shell(tmp_path, monkeypatch):
    m = module()
    sha = 'b' * 40
    seen = {}
    def download(url, output, limit):
        seen['url'] = url
        with tarfile.open(fileobj=output, mode='w:gz') as tar:
            content = b'#!/bin/sh\n'
            entry = tarfile.TarInfo('project/scripts/install-nas.sh')
            entry.size = len(content)
            tar.addfile(entry, io.BytesIO(content))
    def run(args, **kwargs):
        seen['args'] = args
        assert not kwargs.get('shell')
        assert kwargs['check'] and 'timeout' not in kwargs
        assert Path(args[1]).read_text() == '#!/bin/sh\n'
    monkeypatch.setattr(m, 'download', download)
    monkeypatch.setattr(m.subprocess, 'run', run)
    env = dict.fromkeys(['NAS_DOCUMENTS_PATH', 'NAS_PHOTOS_PATH', 'NAS_CODE_PATH', 'NAS_INDEX_PATH'], '/source with spaces')
    env.update(NAS_UID='1', NAS_GID='2', NAS_BIND_IP='127.0.0.1')
    m.install_commit(tmp_path, env, sha)
    assert seen['url'].endswith('/' + sha)
    assert 'ghcr.io/mattyv/nas-search-mcp:' + sha in seen['args']
    assert '/source with spaces' in seen['args']


def test_archive_expansion_budget(tmp_path, monkeypatch):
    m = module()
    monkeypatch.setattr(m, 'MAX_EXPANDED', 1)
    archive = tmp_path / 'archive.tgz'
    with tarfile.open(archive, 'w:gz') as tar:
        entry = tarfile.TarInfo('project/large')
        entry.size = 2
        tar.addfile(entry, io.BytesIO(b'xx'))
    with pytest.raises(ValueError, match='too large'):
        m.extract_archive(archive, tmp_path / 'out')


@pytest.mark.parametrize('action,verified,expected,stage', [
    ('upgrade_main', True, 'succeeded', None),
    ('upgrade_main', False, 'failed', 'main_verification'),
    ('arbitrary_command', True, 'failed', 'request_validation'),
])
def test_request_outcomes(tmp_path, monkeypatch, action, verified, expected, stage):
    m = module()
    install = tmp_path / 'install'
    install.mkdir()
    (install / '.env').write_text('NAS_INDEX_PATH=/unused\n')
    queue = install / 'upgrades'
    queue.mkdir()
    request = queue / 'request.json'
    request.write_text(json.dumps({'action': action}))
    def resolve():
        if not verified:
            raise ValueError('not published')
        return 'c' * 40
    installed = []
    def deploy(*args):
        assert request.exists(), 'Request must block duplicate upgrades until terminal status'
        installed.append(args)
    monkeypatch.setattr(m, 'resolve_verified_main', resolve)
    monkeypatch.setattr(m, 'install_commit', deploy)
    assert m.process_once(install)
    result = json.loads((queue / 'status.json').read_text())
    assert result['state'] == expected
    assert result.get('failure_stage') == stage
    assert bool(installed) == (expected == 'succeeded')
    assert not request.exists()
    assert not m.process_once(install)


def test_download_budget(monkeypatch):
    m = module()
    class Response(io.BytesIO):
        pass
    monkeypatch.setattr(m.urllib.request, 'urlopen', lambda *a, **kw: Response(b'0123456789'))
    with pytest.raises(ValueError, match='too large'):
        m.download('https://example.com', io.BytesIO(), 3)
