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


def preserved_target(tmp_path, monkeypatch):
    m = module()
    monkeypatch.setattr(m, 'validate_trusted_ancestors', lambda path: None)
    real_lstat = Path.lstat
    from types import SimpleNamespace
    def root_lstat(path):
        info = real_lstat(path)
        return SimpleNamespace(st_mode=info.st_mode, st_uid=0)
    monkeypatch.setattr(Path, 'lstat', root_lstat)
    target = tmp_path / 'preserved'
    target.mkdir(mode=0o700)
    contents = {
        '.env': f'NAS_TOKEN=preserved-secret\nNAS_DOCUMENTS_PATH=/m\nNAS_PHOTOS_PATH=/p\nNAS_CODE_PATH=/custom\nNAS_INDEX_PATH=/index\nNAS_UID=1\nNAS_GID=2\nNAS_BIND_IP=127.0.0.1\nNAS_UPGRADE_DIR=/upgrades\nNAS_UPGRADE_PATH={target}/upgrades\n',
        'compose.yaml': 'preserved compose', 'current-image': 'preserved image',
        'upgrade-host.py': 'preserved worker', 'enable-upgrades.log': 'previous failure\n',
    }
    for name, value in contents.items():
        (target / name).write_text(value)
        (target / name).chmod(0o600)
    (target / 'upgrades').mkdir()
    (target / 'upgrades').chmod(0o1733)
    return m, target, contents


def test_resume_preserves_existing_state_without_source(tmp_path, monkeypatch):
    m, target, contents = preserved_target(tmp_path, monkeypatch)
    calls = []
    def install(args, **kwargs):
        calls.append('install')
        kwargs['stdout'].write(b'resumed install\n')
        assert args[args.index('--code-path') + 1] == '/custom'
    monkeypatch.setattr(m.subprocess, 'run', install)
    monkeypatch.setattr(m.subprocess, 'Popen', lambda *a, **kw: calls.append('launch'))
    m.enable(tmp_path / 'missing-source', target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40, resume=True)
    assert calls == ['install', 'launch']
    for name, value in contents.items():
        assert (target / name).read_text() == value + ('resumed install\n' if name == 'enable-upgrades.log' else '')


@pytest.mark.parametrize('unsafe', ['missing', 'symlink', 'writable', 'queue', 'target', 'env'])
def test_resume_rejects_unsafe_or_incomplete_state(tmp_path, monkeypatch, unsafe):
    m, target, _ = preserved_target(tmp_path, monkeypatch)
    if unsafe == 'missing':
        (target / 'current-image').unlink()
    elif unsafe == 'symlink':
        (target / 'upgrade-host.py').unlink()
        (target / 'upgrade-host.py').symlink_to(target / 'compose.yaml')
    elif unsafe == 'writable':
        (target / '.env').chmod(0o666)
    elif unsafe == 'queue':
        (target / 'upgrades').chmod(0o777)
    elif unsafe == 'target':
        target.chmod(0o777)
    else:
        (target / '.env').write_text((target / '.env').read_text().replace('NAS_UPGRADE_DIR=/upgrades', 'NAS_UPGRADE_DIR=/other'))
    monkeypatch.setattr(m.subprocess, 'run', lambda *a, **kw: pytest.fail('Unsafe state reached installer'))
    with pytest.raises((ValueError, FileNotFoundError)):
        m.enable(tmp_path / 'missing', target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40, resume=True)


def test_resume_failed_install_retains_log_and_does_not_launch(tmp_path, monkeypatch):
    m, target, _ = preserved_target(tmp_path, monkeypatch)
    def fail(*a, **kw):
        kw['stdout'].write(b'new failure\n')
        raise m.subprocess.CalledProcessError(1, 'installer')
    monkeypatch.setattr(m.subprocess, 'run', fail)
    monkeypatch.setattr(m.subprocess, 'Popen', lambda *a, **kw: pytest.fail('Started after failure'))
    with pytest.raises(m.subprocess.CalledProcessError):
        m.enable(tmp_path / 'missing', target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40, resume=True)
    assert (target / 'enable-upgrades.log').read_text() == 'previous failure\nnew failure\n'


def test_resume_rejects_nonroot_file_and_checks_ancestors(tmp_path, monkeypatch):
    m, target, _ = preserved_target(tmp_path, monkeypatch)
    ancestors = []
    monkeypatch.setattr(m, 'validate_trusted_ancestors', lambda path: ancestors.append(path))
    root_lstat = Path.lstat
    from types import SimpleNamespace
    def untrusted_file(path):
        info = root_lstat(path)
        return SimpleNamespace(st_mode=info.st_mode, st_uid=1000 if path == target / '.env' else 0)
    monkeypatch.setattr(Path, 'lstat', untrusted_file)
    with pytest.raises(ValueError):
        m.enable(tmp_path / 'missing', target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40, resume=True)
    assert ancestors == [target]


def test_bootstrap_does_not_implicitly_resume_existing_target(tmp_path, monkeypatch):
    m, target, contents = preserved_target(tmp_path, monkeypatch)
    monkeypatch.setattr(m.subprocess, 'run', lambda *a, **kw: pytest.fail('Implicitly resumed'))
    with pytest.raises(FileExistsError):
        m.enable(target, target, 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40)
    assert (target / '.env').read_text() == contents['.env']


def test_detect_active_source_uses_compose_label(monkeypatch):
    from types import SimpleNamespace
    m = module()
    calls = []
    def run(args, **kwargs):
        calls.append(args)
        return SimpleNamespace(returncode=0, stdout='/volume1/nas-search-connector\n')
    monkeypatch.setattr(m.subprocess, 'run', run)
    assert m.detect_source_install_dir() == Path('/volume1/nas-search-connector')
    assert 'com.docker.compose.project.working_dir' in ' '.join(calls[0])
    assert 'nas-search-nas-search-1' in calls[0]


@pytest.mark.parametrize('value', ['', '<no value>', 'relative', '/volume1/../secret', '/volume1/a\n/volume1/b'])
def test_detect_active_source_rejects_invalid_label(monkeypatch, value):
    from types import SimpleNamespace
    m = module()
    monkeypatch.setattr(m.subprocess, 'run', lambda *a, **kw: SimpleNamespace(returncode=0, stdout=value))
    with pytest.raises(ValueError):
        m.detect_source_install_dir()


def test_detect_source_falls_back_only_when_container_absent(monkeypatch):
    from types import SimpleNamespace
    m = module()
    replies = iter([SimpleNamespace(returncode=1, stdout=''), SimpleNamespace(returncode=0, stdout='')])
    monkeypatch.setattr(m.subprocess, 'run', lambda *a, **kw: next(replies))
    assert m.detect_source_install_dir() == Path('/volume1/docker/nas-search')
    replies = iter([SimpleNamespace(returncode=1, stdout=''), SimpleNamespace(returncode=0, stdout='nas-search-nas-search-1\n')])
    with pytest.raises(ValueError, match='Cannot inspect'):
        m.detect_source_install_dir()


def test_bootstrap_default_discovers_source_before_loading_configuration(tmp_path, monkeypatch):
    m = module()
    source = tmp_path / 'active'
    source.mkdir()
    (source / '.env').symlink_to(tmp_path / 'private')
    (tmp_path / 'private').write_text('secret')
    monkeypatch.setattr(m, 'detect_source_install_dir', lambda: source)
    with pytest.raises(ValueError, match='regular files'):
        m.enable(None, tmp_path / 'target', 'ghcr.io/mattyv/nas-search-mcp:'+'a'*40)
    assert not (tmp_path / 'target').exists()
