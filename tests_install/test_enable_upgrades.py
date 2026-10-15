import importlib.util
from pathlib import Path

import pytest


def module():
    spec = importlib.util.spec_from_file_location('enable_upgrades', Path(__file__).parents[1] / 'scripts/enable-upgrades.py')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


@pytest.mark.parametrize('image', ['ghcr.io/mattyv/nas-search-mcp:latest', 'other:'+'a'*40, 'ghcr.io/mattyv/nas-search-mcp:'+'A'*40])
def test_requires_exact_repository_and_commit(image):
    with pytest.raises(ValueError):
        module().validate_image(image)


def test_target_refuses_existing_or_symlink_ancestor(tmp_path, monkeypatch):
    m = module()
    monkeypatch.setattr(m, 'validate_trusted_ancestors', lambda path: None)
    existing = tmp_path / 'existing'
    existing.mkdir()
    with pytest.raises(FileExistsError):
        m.create_target(existing)


def test_bootstrap_preserves_credentials_and_launches_after_install(tmp_path, monkeypatch):
    m = module()
    source = tmp_path / 'old'
    source.mkdir()
    original = "NAS_TOKEN=private\nNAS_DOCUMENTS_PATH=/m\nNAS_PHOTOS_PATH=/p\nNAS_CODE_PATH=/c\nNAS_INDEX_PATH=/index\nNAS_UID=1\nNAS_GID=2\nNAS_BIND_IP=127.0.0.1\nNAS_UPGRADE_PATH=/unsafe\n"
    (source / '.env').write_text(original)
    (source / 'compose.yaml').write_text('services: {}\n')
    (source / 'current-image').write_text('old-image\n')
    target = tmp_path / 'trusted'
    monkeypatch.setattr(m, 'validate_trusted_ancestors', lambda path: None)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    calls = []
    def run(args, **kwargs):
        assert 'timeout' not in kwargs
        calls.append(('install', args))
    monkeypatch.setattr(m.subprocess, 'run', run)
    monkeypatch.setattr(m.subprocess, 'Popen', lambda args, **kw: calls.append(('launch', args)))
    m.enable(source, target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40)
    assert (source / '.env').read_text() == original
    env = (target / '.env').read_text()
    assert 'NAS_TOKEN=private\n' in env
    assert f'NAS_UPGRADE_PATH={target}/upgrades\n' in env
    assert 'NAS_UPGRADE_DIR=/upgrades\n' in env
    assert '/unsafe' not in env
    assert (target / '.env').stat().st_mode & 0o777 == 0o600
    assert (target / 'upgrades').stat().st_mode & 0o7777 == 0o1733
    assert [call[0] for call in calls] == ['install', 'launch']
    assert str(target) in calls[0][1]


def test_source_symlink_rejected_before_target_creation(tmp_path, monkeypatch):
    m = module()
    source = tmp_path / 'source'
    source.mkdir()
    other = tmp_path / 'credentials'
    other.write_text('NAS_TOKEN=private')
    (source / '.env').symlink_to(other)
    with pytest.raises(ValueError):
        m.enable(source, tmp_path / 'target', 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40)
    assert not (tmp_path / 'target').exists()


def test_unsafe_ancestors_rejected(tmp_path):
    m = module()
    parent = tmp_path / 'writable'
    parent.mkdir(mode=0o777)
    parent.chmod(0o777)
    with pytest.raises(ValueError):
        m.validate_trusted_ancestors(parent / 'target')
    link = tmp_path / 'linked'
    link.symlink_to(parent, target_is_directory=True)
    with pytest.raises(ValueError):
        m.validate_trusted_ancestors(link / 'target')


def test_installer_failure_does_not_launch_worker(tmp_path, monkeypatch):
    m = module()
    source = tmp_path / 'old'
    source.mkdir()
    (source / '.env').write_text('NAS_TOKEN=private\nNAS_DOCUMENTS_PATH=/m\nNAS_PHOTOS_PATH=/p\nNAS_INDEX_PATH=/index\nNAS_UID=1\nNAS_GID=2\n')
    (source / 'compose.yaml').write_text('services: {}')
    (source / 'current-image').write_text('old')
    monkeypatch.setattr(m, 'validate_trusted_ancestors', lambda path: None)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    def fail(*args, **kwargs):
        raise m.subprocess.CalledProcessError(1, 'installer')
    monkeypatch.setattr(m.subprocess, 'run', fail)
    monkeypatch.setattr(m.subprocess, 'Popen', lambda *a, **kw: pytest.fail('Worker started after failed deployment'))
    target = tmp_path / 'target'
    with pytest.raises(m.subprocess.CalledProcessError):
        m.enable(source, target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40)
    assert (target / '.env').exists()
    assert (source / 'current-image').read_text() == 'old'


def test_failure_message_acknowledges_deployment_may_have_changed(monkeypatch, capsys):
    m = module()
    monkeypatch.setattr(m.os, 'geteuid', lambda: 0)
    monkeypatch.setattr(m.os, 'umask', lambda *args: None)
    monkeypatch.setattr(m.sys, 'argv', ['enable-upgrades.py', '--image', 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40])
    def fail(*args):
        raise RuntimeError('private diagnostic')
    monkeypatch.setattr(m, 'enable', fail)
    with pytest.raises(SystemExit) as error:
        m.main()
    assert error.value.code == 1
    message = capsys.readouterr().err
    assert 'Deployment may have changed' in message
    assert 'private diagnostic' not in message
