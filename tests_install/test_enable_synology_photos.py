import contextlib
import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest


def module():
    path = Path(__file__).resolve().parents[1] / 'scripts/enable-synology-photos.py'
    spec = importlib.util.spec_from_file_location('enable_synology_photos', path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def fixture(tmp_path, monkeypatch):
    setup = module()
    install = tmp_path / 'install'
    install.mkdir(mode=0o700)
    photo = tmp_path / 'matt' / 'Photos'
    photo.mkdir(parents=True)
    original = ('NAS_TOKEN=private-token\nNAS_UID=1026\nNAS_GID=100\n'
                'NAS_MATTHEW_PATH=/authorized/documents\nNAS_CODE_PATH=/authorized/code\n'
                'NAS_PHOTOS_PATH=/authorized/pilot\nNAS_PHOTOS=false\nNAS_INDEX_PATH=/index\n'
                'NAS_BIND_IP=192.168.1.4\nNAS_ALLOWED_FOLDERS=Matthew,Code,Photos\n')
    (install / '.env').write_text(original)
    (install / '.env').chmod(0o600)
    (install / 'current-image').write_text('ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40)
    (install / 'current-image').chmod(0o600)
    monkeypatch.setattr(setup, 'ROOT', tmp_path / 'broker')
    recovery = tmp_path / 'recovery'
    recovery.mkdir(mode=0o700)
    (recovery / 'nas-recovery.py').write_bytes(b'old recovery')
    monkeypatch.setattr(setup, 'RECOVERY', recovery)
    monkeypatch.setattr(setup, 'PERSONAL_PHOTOS', photo)
    monkeypatch.setattr(setup, 'secure_directory', lambda path: None)
    monkeypatch.setattr(setup, 'private_file', lambda path: None)
    monkeypatch.setattr(setup, 'protected_script', lambda path: None)
    monkeypatch.setattr(setup.pwd, 'getpwnam', lambda name: SimpleNamespace(pw_uid=1026))
    monkeypatch.setattr(setup.os, 'chown', lambda *args: None)
    maintenance = SimpleNamespace(pause_workers=lambda path: contextlib.nullcontext(),
                                  check_idle=lambda path: None)
    monkeypatch.setattr(setup, 'load_restore', lambda source: maintenance)
    calls = []
    child = SimpleNamespace(poll=lambda: None, terminate=lambda: calls.append('stop'),
                            wait=lambda timeout: None)
    monkeypatch.setattr(setup, 'start_broker', lambda log: calls.append('start') or child)
    monkeypatch.setattr(setup, 'wait_broker', lambda: calls.append('broker-ready'))
    monkeypatch.setattr(setup, 'verify_mcp', lambda env: calls.append('mcp-ready'))
    monkeypatch.setattr(setup, 'run_install', lambda *args: calls.append(('install', args[2])))
    return setup, install, original, calls


def test_enabling_preserves_credentials_original_mounts_and_photos_pipeline(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    current = (install / '.env').read_text()
    assert current.startswith(original)
    assert current.count('NAS_SYNOLOGY_PHOTOS_SOCKET=') == 1
    assert 'NAS_SYNOLOGY_PHOTOS_SOCKET=/synology-photos/photos.sock\n' in current
    assert f'NAS_SYNOLOGY_PHOTOS_HOST_PATH={setup.ROOT}/run\n' in current
    assert (install / '.env.before-synology-photos').read_text() == original
    assert (setup.ROOT / 'run').stat().st_mode & 0o777 == 0o755
    assert (setup.ROOT / 'synology-photos-host.py').stat().st_mode & 0o777 == 0o700
    assert calls == ['start', 'broker-ready', ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40), 'mcp-ready']


def test_dry_run_does_not_create_paths_or_pause_processes(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40, dry_run=True)
    assert not setup.ROOT.exists()
    assert not (install / '.deployment.lock').exists()
    assert (install / '.env').read_text() == original
    assert calls == []


def test_failure_restores_env_and_previous_deployment(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    monkeypatch.setattr(setup, 'verify_mcp', lambda env: (_ for _ in ()).throw(ValueError('failed')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == original
    assert ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40) in calls
    assert calls[-1] == 'stop'
    assert setup.ROOT.exists()  # Preserve diagnostics; never recursively delete.


def test_wrong_app_uid_or_existing_target_refused_before_changes(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    monkeypatch.setattr(setup.pwd, 'getpwnam', lambda name: SimpleNamespace(pw_uid=999))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert not setup.ROOT.exists()
    monkeypatch.setattr(setup.pwd, 'getpwnam', lambda name: SimpleNamespace(pw_uid=1026))
    setup.ROOT.mkdir()
    with pytest.raises(FileExistsError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == original
    assert calls == []


@pytest.mark.parametrize('allowed,denied', [
    ('', ''), ('Matthew,Code', ''), ('Matthew,Photos/Family', ''),
    ('Photos', 'Photos'), ('Photos', 'Photos/Private'), ('Photos', ' Code, Photos/Private '),
])
def test_restricted_photos_scope_refused_before_changes(tmp_path, monkeypatch, allowed, denied):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    contents = original.replace('NAS_ALLOWED_FOLDERS=Matthew,Code,Photos', 'NAS_ALLOWED_FOLDERS=' + allowed)
    contents += 'NAS_DENIED_FOLDERS=' + denied + '\n'
    (install / '.env').write_text(contents)
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert not setup.ROOT.exists()
    assert (install / '.env').read_text() == contents
    assert calls == []


@pytest.mark.parametrize('image', ['latest', 'ghcr.io/mattyv/nas-search-mcp:latest', 'other:' + 'a' * 40])
def test_unpinned_images_rejected(image):
    with pytest.raises(ValueError):
        module().validate_image(image)


def test_env_parser_rejects_duplicates_and_aliases():
    setup = module()
    for body in ['NAS_UID=1026\nNAS_UID=0\n', 'export NAS_UID=1026\n', ' NAS_UID=1026\n']:
        with pytest.raises(ValueError):
            setup.read_env(body)


def test_deployment_lock_prevents_setup_overlapping_orphan_installer(tmp_path, monkeypatch):
    import fcntl
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    with (install / '.deployment.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        with pytest.raises(BlockingIOError):
            setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == original
    assert calls == []


def test_pending_upgrade_resumes_maintenance_without_touching_settings(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    events = []
    @contextlib.contextmanager
    def pause(path):
        events.append('paused')
        try:
            yield
        finally:
            events.append('resumed')
    def busy(path):
        raise ValueError('upgrade pending')
    monkeypatch.setattr(setup, 'load_restore', lambda source: SimpleNamespace(pause_workers=pause, check_idle=busy))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert events == ['paused', 'resumed']
    assert (install / '.env').read_text() == original
    assert not (install / '.env.before-synology-photos').exists()
    assert calls == []


def test_broker_failure_does_not_deploy_or_replace_recovery(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    monkeypatch.setattr(setup, 'wait_broker', lambda: (_ for _ in ()).throw(ValueError('not ready')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert calls == ['start', 'stop']
    assert (install / '.env').read_text() == original
    assert (setup.RECOVERY / 'nas-recovery.py').read_bytes() == b'old recovery'


def test_installer_carries_deployment_lock_without_secret_arguments(tmp_path, monkeypatch):
    setup = module()
    env = {key: 'placeholder' for key in ('NAS_MATTHEW_PATH', 'NAS_PHOTOS_PATH', 'NAS_CODE_PATH',
                                         'NAS_INDEX_PATH', 'NAS_UID', 'NAS_GID', 'NAS_BIND_IP')}
    env['NAS_TOKEN'] = 'do-not-expose'
    observed = []
    monkeypatch.setattr(setup.subprocess, 'run', lambda args, **kwargs: observed.append((args, kwargs)))
    setup.run_install(tmp_path, tmp_path, 'image', env, None, 17)
    args, kwargs = observed[0]
    assert kwargs['pass_fds'] == (17,)
    assert 'do-not-expose' not in str(args)
    assert 'NAS_TOKEN' not in kwargs['env']


def test_container_mcp_probe_checks_real_people_call_without_secret_arguments(monkeypatch):
    setup = module()
    observed = []
    monkeypatch.setattr(setup.subprocess, 'run', lambda args, **kwargs: observed.append(args))
    setup.verify_mcp({'NAS_TOKEN': 'do-not-expose'})
    code = observed[0][-1]
    compile(code, '<photos-readiness>', 'exec')
    assert '"photos_people"' in code and '"tools/call"' in code
    assert '"photos_preview"' in code
    assert 'do-not-expose' not in str(observed)


def test_missing_checkout_inputs_are_rejected_before_creating_target(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    source = tmp_path / 'incomplete'
    source.mkdir()
    with pytest.raises(FileNotFoundError):
        setup.enable(source, install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert not setup.ROOT.exists()
    assert (install / '.env').read_text() == original
    assert calls == []


def test_failed_post_install_probe_restores_env_and_resumes_worker(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    events = []
    @contextlib.contextmanager
    def pause(path):
        events.append('paused')
        try:
            yield
        finally:
            events.append('resumed')
    monkeypatch.setattr(setup, 'load_restore', lambda source: SimpleNamespace(pause_workers=pause, check_idle=lambda path: None))
    monkeypatch.setattr(setup, 'verify_mcp', lambda env: (_ for _ in ()).throw(ValueError('unavailable')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert events == ['paused', 'resumed']
    assert (install / '.env').read_text() == original
    assert ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40) in calls
    assert (setup.RECOVERY / 'nas-recovery.py').read_bytes() == b'old recovery'


def test_changed_deployment_during_preflight_is_not_overwritten(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    changed = original + 'NAS_OAUTH_CLIENT_ID=new-value\n'
    @contextlib.contextmanager
    def pause(path):
        (install / '.env').write_text(changed)
        yield
    monkeypatch.setattr(setup, 'load_restore', lambda source: SimpleNamespace(pause_workers=pause, check_idle=lambda path: None))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == changed
    assert calls == []
