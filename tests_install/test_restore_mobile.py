import importlib.util
from pathlib import Path
import pytest


def module():
    spec = importlib.util.spec_from_file_location('restore_mobile', Path(__file__).parents[1] / 'scripts/restore-mobile.py')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def test_merge_preserves_source_and_only_target_upgrade_settings():
    m = module()
    source = 'NAS_TOKEN=secret\nNAS_OAUTH_PASSWORD_HASH=private\nNAS_DOWNLOADS=true\nNAS_UPGRADE_DIR=old\n'
    result = m.merge_env(source, {'NAS_UPGRADE_DIR': '/upgrades', 'NAS_UPGRADE_PATH': '/target/upgrades'})
    assert 'NAS_OAUTH_PASSWORD_HASH=private' in result
    assert 'NAS_DOWNLOADS=true' in result
    assert 'old' not in result
    assert 'NAS_UPGRADE_PATH=/target/upgrades' in result


@pytest.mark.parametrize('change', [{'NAS_TOKEN': 'other'}, {'NAS_INDEX_PATH': '/wrong'}, {'NAS_OAUTH_ISSUER': 'http://bad'}, {'NAS_OAUTH_ISSUER': 'https://ok/path'}, {'NAS_OAUTH_PASSWORD_HASH': ''}])
def test_rejects_unrelated_or_incomplete_source(change):
    m = module()
    target = dict(NAS_TOKEN='same', NAS_INDEX_PATH='/data', NAS_MATTHEW_PATH='/m', NAS_CODE_PATH='/c', NAS_PHOTOS_PATH='/p', NAS_UPGRADE_DIR='/upgrades', NAS_UPGRADE_PATH='/target/upgrades')
    source = dict(target, NAS_OAUTH_ISSUER='https://login.example', NAS_OAUTH_PASSWORD_HASH='hash')
    source.update(change)
    with pytest.raises(ValueError):
        m.validate_config(source, target, Path('/target'))


def test_private_file_rejects_symlink_and_open_permissions(tmp_path):
    m = module()
    path = tmp_path / 'private'
    path.write_text('secret')
    path.chmod(0o644)
    with pytest.raises(ValueError):
        m.private_file(path)
    link = tmp_path / 'link'
    link.symlink_to(path)
    with pytest.raises(ValueError):
        m.private_file(link)


def test_oauth_check_requires_metadata_and_challenge(monkeypatch):
    m = module()
    calls = []
    class Response:
        status = 200
        def __enter__(self):
            return self
        def __exit__(self, *args):
            pass
        def read(self):
            return b'{"issuer":"https://login.example"}'
    def open_url(request, **kwargs):
        calls.append(request)
        if isinstance(request, str):
            return Response()
        raise m.urllib.error.HTTPError(request.full_url, 401, 'Unauthorized', {'WWW-Authenticate': 'Bearer resource_metadata="https://example/metadata"'}, None)
    monkeypatch.setattr(m.urllib.request, 'urlopen', open_url)
    m.check_oauth({'NAS_OAUTH_ISSUER': 'https://login.example'})
    assert calls[0] == 'http://127.0.0.1:8082/.well-known/oauth-authorization-server'
    assert calls[1].full_url == 'http://127.0.0.1:8082/mcp'


def test_oauth_metadata_must_match_source_issuer(monkeypatch):
    m = module()
    class Response:
        status = 200
        def __enter__(self): return self
        def __exit__(self, *args): pass
        def read(self): return b'{"issuer":"https://wrong.example"}'
    def open_url(request, **kwargs):
        if isinstance(request, str): return Response()
        raise m.urllib.error.HTTPError(request.full_url, 401, 'Unauthorized', {'WWW-Authenticate': 'Bearer resource_metadata="https://example/metadata"'}, None)
    monkeypatch.setattr(m.urllib.request, 'urlopen', open_url)
    with pytest.raises(ValueError):
        m.check_oauth({'NAS_OAUTH_ISSUER': 'https://right.example'})


def test_pending_upgrade_blocks_restore(tmp_path):
    m = module()
    queue = tmp_path / 'upgrades'
    queue.mkdir()
    (queue / 'request.json').write_text('{}')
    with pytest.raises(ValueError):
        m.check_idle(tmp_path)
    (queue / 'request.json').unlink()
    (queue / 'status.json').write_text('{"state":"running"}')
    with pytest.raises(ValueError):
        m.check_idle(tmp_path)


@pytest.mark.parametrize('mode', ['success', 'dry-run', 'failed-install', 'wrong-token'])
def test_main_preserves_configuration_and_recovers(tmp_path, monkeypatch, mode):
    import os
    import sys
    m = module()
    source, target = tmp_path / 'source', tmp_path / 'target'
    source.mkdir()
    target.mkdir()
    common = 'NAS_TOKEN=same\nNAS_INDEX_PATH=/data\nNAS_MATTHEW_PATH=/m\nNAS_CODE_PATH=/c\nNAS_PHOTOS_PATH=/p\nNAS_UID=1\nNAS_GID=2\nNAS_BIND_IP=127.0.0.1\n'
    original = common + 'NAS_UPGRADE_DIR=/upgrades\nNAS_UPGRADE_PATH=' + str(target / 'upgrades') + '\n'
    (target / '.env').write_text(original)
    source_text = common + 'NAS_OAUTH_ISSUER=https://login.example\nNAS_OAUTH_PASSWORD_HASH=privatehash\nNAS_DOWNLOADS=true\n'
    if mode == 'wrong-token':
        source_text = source_text.replace('NAS_TOKEN=same', 'NAS_TOKEN=wrong')
    (source / '.env').write_text(source_text)
    (target / 'current-image').write_text('ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    # Filesystem privilege validation is tested independently. Exercise orchestration here.
    monkeypatch.setattr(m, 'secure_directory', lambda p: None)
    monkeypatch.setattr(m, 'private_file', lambda p: None)
    monkeypatch.setattr(m.os, 'geteuid', lambda: 0)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    real_lstat = m.Path.lstat
    def root_owned(path, *args, **kwargs):
        result = real_lstat(path, *args, **kwargs)
        values = list(result)
        values[4] = 0
        values[0] &= ~0o022
        return os.stat_result(values)
    monkeypatch.setattr(m.Path, 'lstat', root_owned)
    calls = []
    def run_installer(installer, install_dir, image, env, log):
        calls.append(dict(env))
        if mode == 'failed-install' and len(calls) == 1:
            raise RuntimeError('private adapter error')
    monkeypatch.setattr(m, 'install', run_installer)
    monkeypatch.setattr(m, 'check_oauth', lambda env: None)
    argv = ['restore-mobile.py', '--source-install-dir', str(source), '--install-dir', str(target)]
    if mode == 'dry-run': argv.append('--dry-run')
    monkeypatch.setattr(sys, 'argv', argv)
    result = m.main()
    if mode in ('dry-run', 'wrong-token'):
        assert not calls
        assert (target / '.env').read_text() == original
        assert not (target / '.env.before-mobile-restore').exists()
        assert result == (0 if mode == 'dry-run' else 1)
    elif mode == 'failed-install':
        assert result == 1
        assert len(calls) == 2
        assert 'NAS_OAUTH_ISSUER' not in calls[1]
        assert (target / '.env').read_text() == original
    else:
        assert result == 0
        assert len(calls) == 1
        assert calls[0]['NAS_OAUTH_PASSWORD_HASH'] == 'privatehash'
        assert calls[0]['NAS_UPGRADE_PATH'] == str(target / 'upgrades')
        assert 'NAS_DOWNLOADS=true' in (target / '.env').read_text()
        backup = target / '.env.before-mobile-restore'
        assert backup.read_text() == original
        assert backup.stat().st_mode & 0o777 == 0o600
