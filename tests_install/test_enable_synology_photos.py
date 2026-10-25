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
    photo = tmp_path / 'photos-user' / 'Photos'
    photo.mkdir(parents=True)
    original = ('NAS_TOKEN=private-token\nNAS_UID=1234\nNAS_GID=1234\n'
                'NAS_DOCUMENTS_PATH=/authorized/documents\nNAS_DOCUMENTS_LABEL=Documents\nNAS_CODE_PATH=/authorized/code\n'
                'NAS_PHOTOS_PATH=/authorized/pilot\nNAS_PHOTOS=false\nNAS_INDEX_PATH=/index\n'
                'NAS_BIND_IP=192.0.2.10\nNAS_ALLOWED_FOLDERS=Documents,Code,Photos\n'
                'NAS_UPLOAD_ENABLED=true\nNAS_UPLOAD_INDEXED_DESTINATIONS=true\n')
    (install / '.env').write_text(original)
    (install / '.env').chmod(0o600)
    (install / 'current-image').write_text('ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40)
    (install / 'current-image').chmod(0o600)
    (install / 'compose.yaml').write_bytes(b'services:\n  nas-search:\n    image: previous-image\n')
    (install / 'compose.yaml').chmod(0o600)
    (install / 'upgrade-host.py').write_bytes(b'#!/usr/bin/env python3\n# previous worker\n')
    (install / 'upgrade-host.py').chmod(0o700)
    monkeypatch.setattr(setup, 'ROOT', tmp_path / 'broker')
    recovery = tmp_path / 'recovery'
    recovery.mkdir(mode=0o700)
    (recovery / 'nas-recovery.py').write_bytes(b'old recovery')
    monkeypatch.setattr(setup, 'RECOVERY', recovery)
    monkeypatch.setattr(setup, 'secure_directory', lambda path: None)
    monkeypatch.setattr(setup, 'private_file', lambda path: None)
    monkeypatch.setattr(setup, 'protected_script', lambda path: None)
    account = SimpleNamespace(pw_uid=1234, pw_name='photos-user', pw_dir=str(photo.parent))
    monkeypatch.setattr(setup.pwd, 'getpwuid', lambda uid: account)
    monkeypatch.setattr(setup.pwd, 'getpwnam', lambda name: account)
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
    monkeypatch.setattr(setup, 'prepare_runner', lambda source, install: (None, None))
    monkeypatch.setattr(setup, 'update_root_runner', lambda *args: calls.append('runner-updated'))
    monkeypatch.setattr(setup, 'restore_root_runner', lambda *args: calls.append('runner-restored'))
    monkeypatch.setattr(setup, 'rollback_legacy', lambda *args: calls.append('legacy-restored'))
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
    import json
    private_account = setup.ROOT / 'account.json'
    assert private_account.stat().st_mode & 0o777 == 0o600
    assert json.loads(private_account.read_text()) == {
        'version': 1, 'account': 'photos-user', 'app_uid': 1234,
        'install_dir': str(install), 'photos_root': str(tmp_path / 'photos-user' / 'Photos')}
    assert calls == ['start', 'broker-ready', ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40), 'mcp-ready', 'runner-updated']


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
    account = SimpleNamespace(pw_uid=999, pw_name='photos-user', pw_dir=str(tmp_path / 'photos-user'))
    monkeypatch.setattr(setup.pwd, 'getpwuid', lambda uid: account)
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert not setup.ROOT.exists()
    account.pw_uid = 1234
    setup.ROOT.mkdir()
    with pytest.raises(FileExistsError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == original
    assert calls == []


@pytest.mark.parametrize('allowed,denied', [
    ('', ''), ('Documents,Code', ''), ('Documents,Photos/Family', ''),
    ('Photos', 'Photos'), ('Photos', 'Photos/Private'), ('Photos', ' Code, Photos/Private '),
])
def test_restricted_photos_scope_refused_before_changes(tmp_path, monkeypatch, allowed, denied):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    contents = original.replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', 'NAS_ALLOWED_FOLDERS=' + allowed)
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
    for body in ['NAS_UID=1234\nNAS_UID=0\n', 'export NAS_UID=1234\n', ' NAS_UID=1234\n']:
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
    env = {key: 'placeholder' for key in ('NAS_DOCUMENTS_PATH', 'NAS_DOCUMENTS_LABEL', 'NAS_PHOTOS_PATH', 'NAS_CODE_PATH',
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


def test_account_identity_and_scope_are_frozen_before_broker_launch(tmp_path, monkeypatch):
    import json
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    seen = []
    def start(log):
        record = json.loads((setup.ROOT / 'account.json').read_text())
        seen.append(record)
        return SimpleNamespace(poll=lambda: None, terminate=lambda: None, wait=lambda timeout: None)
    monkeypatch.setattr(setup, 'start_broker', start)
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert seen[0]['account'] == 'photos-user' and seen[0]['app_uid'] == 1234
    assert seen[0]['photos_root'] == str((tmp_path / 'photos-user' / 'Photos').resolve())
    assert (install / '.env').read_text().startswith(original)


def test_personal_photo_symlink_escape_or_invalid_uid_refused(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    photo = tmp_path / 'photos-user' / 'Photos'
    photo.rmdir()
    outside = tmp_path / 'outside'
    outside.mkdir()
    photo.symlink_to(outside, target_is_directory=True)
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert not setup.ROOT.exists()
    photo.unlink()
    photo.mkdir()
    for uid in ('0', '-1', 'invalid'):
        (install / '.env').write_text(original.replace('NAS_UID=1234', 'NAS_UID=' + uid))
        with pytest.raises(ValueError):
            setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert not setup.ROOT.exists()
    assert calls == []


def test_legacy_mount_aliases_preserve_private_keys_and_active_document_scope(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    legacy = ''.join(line for line in original.splitlines(True) if not line.startswith(('NAS_DOCUMENTS_PATH=', 'NAS_DOCUMENTS_LABEL=')))
    legacy += 'PRIVATE_SITE_DOCUMENTS_SETTING=/private/legacy-location\n'
    (install / '.env').write_text(legacy)
    live = tmp_path / 'active-documents'
    live.mkdir()
    monkeypatch.setattr(setup, 'inspect_document_mount', lambda: (str(live), 'ArchiveDocs'))
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    current = (install / '.env').read_text()
    assert current.startswith(legacy)
    assert f"NAS_DOCUMENTS_PATH='{live}'\nNAS_DOCUMENTS_LABEL=ArchiveDocs\n" in current
    assert 'NAS_PHOTOS_PATH=/authorized/pilot\nNAS_PHOTOS=false\n' in current
    assert (install / '.env.before-synology-photos').read_text() == legacy
    assert calls[-1] == 'runner-updated'


def test_legacy_rollback_uses_original_compose_and_never_new_installer_with_old_env(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    legacy = ''.join(line for line in original.splitlines(True) if not line.startswith(('NAS_DOCUMENTS_PATH=', 'NAS_DOCUMENTS_LABEL=')))
    (install / '.env').write_text(legacy)
    live = tmp_path / 'active-documents'
    live.mkdir()
    monkeypatch.setattr(setup, 'inspect_document_mount', lambda: (str(live), 'ArchiveDocs'))
    monkeypatch.setattr(setup, 'verify_mcp', lambda env: (_ for _ in ()).throw(ValueError('probe failed')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == legacy
    assert 'legacy-restored' in calls
    assert ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40) not in calls
    assert (install / 'upgrade-host.py').read_bytes() == b'#!/usr/bin/env python3\n# previous worker\n'


def test_active_mount_discovery_rejects_ambiguous_or_writable_document_roots(monkeypatch, tmp_path):
    import json
    setup = module()
    root = tmp_path / 'documents'
    root.mkdir()
    base = {'Type': 'bind', 'Destination': '/files/ArchiveDocs', 'Source': str(root), 'RW': False}
    for mounts in ([dict(base, RW=True)], [base, dict(base, Destination='/files/Other')],
                   [dict(base, Destination='/files/Photos')]):
        monkeypatch.setattr(setup, 'docker_capture', lambda args, mounts=mounts: json.dumps(mounts).encode())
        with pytest.raises(ValueError):
            setup.inspect_document_mount()


def test_runner_update_failure_restores_service_settings_and_keeps_old_worker(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    monkeypatch.setattr(setup, 'update_root_runner', lambda *args: (_ for _ in ()).throw(ValueError('runner update failed')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert (install / '.env').read_text() == original
    assert (install / 'upgrade-host.py').read_bytes() == b'#!/usr/bin/env python3\n# previous worker\n'
    assert ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40) in calls


def test_original_compose_rollback_preserves_profile_and_health_checks(tmp_path, monkeypatch):
    setup = module()
    calls = []
    monkeypatch.setattr(setup.os, 'chown', lambda *args: None)
    monkeypatch.setattr(setup.subprocess, 'run', lambda args, **kwargs: calls.append((args, kwargs)))
    compose = b'services:\n  nas-search:\n    image: old\n'
    image = b'ghcr.io/mattyv/nas-search-mcp:' + b'b' * 40 + b'\n'
    setup.rollback_legacy(tmp_path, compose, image, {'NAS_UPLOAD_INDEXED_DESTINATIONS': 'true'}, None, 17)
    assert (tmp_path / 'compose.yaml').read_bytes() == compose
    assert (tmp_path / 'current-image').read_bytes() == image
    assert '--profile' in calls[0][0] and 'indexed-upload' in calls[0][0]
    assert '--no-build' in calls[0][0]
    assert calls[0][1]['pass_fds'] == (17,)
    code = calls[1][0][-1]
    compile(code, '<legacy-health>', 'exec')
    assert '/healthz' in code and 'resource_metadata' in code


def test_active_mount_discovery_uses_selected_readonly_metadata(monkeypatch, tmp_path):
    import json
    setup = module()
    observed = []
    mounts = [{'Type': 'bind', 'Destination': '/files/ArchiveDocs', 'Source': str(tmp_path), 'RW': False},
              {'Type': 'bind', 'Destination': '/files/Code', 'Source': '/private/code', 'RW': False},
              {'Type': 'bind', 'Destination': '/files/Photos', 'Source': '/private/pilot', 'RW': False}]
    monkeypatch.setattr(setup, 'docker_capture', lambda args: observed.append(args) or json.dumps(mounts).encode())
    assert setup.inspect_document_mount() == (str(tmp_path), 'ArchiveDocs')
    assert observed == [['inspect', '--format', '{{json .Mounts}}', 'nas-search-nas-search-1']]


def test_exact_paused_worker_is_resumed_before_reviewed_updater(tmp_path, monkeypatch):
    import signal
    setup = module()
    proc = tmp_path / 'proc' / '42'
    proc.mkdir(parents=True)
    (proc / 'status').write_text('State:\tT (stopped)\n')
    source = tmp_path / 'source'
    (source / 'scripts').mkdir(parents=True)
    (source / 'scripts' / 'upgrade-host.py').write_bytes(b'new reviewed worker')
    install = tmp_path / 'install'
    install.mkdir()
    (install / 'upgrade-host.py').write_bytes(b'old worker')
    events = []
    updater = SimpleNamespace(process_identity=lambda path: 'same-start', worker_matches=lambda path, install: True,
                              update_runner=lambda *args: events.append('update'))
    worker = SimpleNamespace(pid=42, identity='same-start')
    monkeypatch.setattr(setup, 'Path', lambda value: tmp_path / 'proc' if value == '/proc' else Path(value))
    monkeypatch.setattr(setup.os, 'kill', lambda pid, sig: events.append((pid, sig)))
    setup.update_root_runner(source, install, updater, worker)
    assert events == [(42, signal.SIGCONT), 'update']
    events.clear()
    worker.identity = 'reused-pid'
    with pytest.raises(ValueError):
        setup.update_root_runner(source, install, updater, worker)
    assert events == []


def test_late_recovery_failure_restores_worker_before_service_rollback(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    write = setup.atomic_write
    def fail_recovery(path, body, mode=0o600):
        if path == setup.RECOVERY / 'nas-recovery.py':
            raise OSError('cannot replace recovery')
        return write(path, body, mode)
    monkeypatch.setattr(setup, 'atomic_write', fail_recovery)
    with pytest.raises(OSError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert calls.index('runner-restored') < calls.index(('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40))
    assert (install / '.env').read_text() == original


def test_original_state_is_private_and_durable_before_worker_pause(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    previous = {'.env': original.encode(),
                'compose.yaml': (install / 'compose.yaml').read_bytes(),
                'current-image': (install / 'current-image').read_bytes(),
                'upgrade-host.py': (install / 'upgrade-host.py').read_bytes()}
    events = []
    @contextlib.contextmanager
    def pause(path):
        directory = setup.ROOT / 'previous-state'
        assert directory.stat().st_mode & 0o777 == 0o700
        for name, contents in previous.items():
            saved = directory / name
            assert saved.read_bytes() == contents
            assert saved.stat().st_mode & 0o777 == 0o600
        events.append('durable-state-seen')
        yield
    monkeypatch.setattr(setup, 'load_restore', lambda source: SimpleNamespace(pause_workers=pause, check_idle=lambda path: None))
    monkeypatch.setattr(setup, 'wait_broker', lambda: (_ for _ in ()).throw(ValueError('not ready')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40)
    assert events == ['durable-state-seen']
    assert (install / '.env').read_bytes() == previous['.env']
    for name, contents in previous.items():
        assert (setup.ROOT / 'previous-state' / name).read_bytes() == contents


@pytest.mark.parametrize('line', [
    'NAS_ALLOWED_FOLDERS=Documents,Code',
    "NAS_ALLOWED_FOLDERS='Documents, Code'",
    'NAS_ALLOWED_FOLDERS=',
])
def test_explicit_photos_grant_changes_only_allowline_and_readiness_sees_candidate(tmp_path, monkeypatch, line):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    original = original.replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', line)
    original += 'NAS_DENIED_FOLDERS=Code/Private\nPRIVATE_SETTING=keep-exactly\n'
    (install / '.env').write_text(original)
    seen = []
    def ready():
        candidate = setup.read_env((install / '.env').read_text())
        seen.append(candidate)
        assert 'Photos' in [item.strip() for item in candidate['NAS_ALLOWED_FOLDERS'].split(',')]
        assert candidate['NAS_DENIED_FOLDERS'] == 'Code/Private'
    monkeypatch.setattr(setup, 'wait_broker', ready)
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40,
                 grant_photos_access=True)
    contents = (install / '.env').read_text()
    before_other = [item for item in original.splitlines(True) if not item.startswith('NAS_ALLOWED_FOLDERS=')]
    after_other = [item for item in contents.splitlines(True)
                   if not item.startswith(('NAS_ALLOWED_FOLDERS=', 'NAS_SYNOLOGY_PHOTOS_'))]
    assert before_other == after_other
    assert (install / '.env.before-synology-photos').read_text() == original
    assert 'NAS_PHOTOS=false\n' in contents
    assert len(seen) == 1


@pytest.mark.parametrize('denied', ['Photos', 'Photos/Private', 'Code, Photos/Private'])
def test_explicit_grant_never_removes_existing_photo_denial(tmp_path, monkeypatch, denied):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    original = original.replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', 'NAS_ALLOWED_FOLDERS=Documents,Code')
    original += 'NAS_DENIED_FOLDERS=' + denied + '\n'
    (install / '.env').write_text(original)
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40,
                     grant_photos_access=True)
    assert not setup.ROOT.exists()
    assert (install / '.env').read_text() == original
    assert calls == []


def test_opt_in_dry_run_does_not_write_and_existing_full_grant_is_unchanged(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    missing = original.replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', 'NAS_ALLOWED_FOLDERS=')
    (install / '.env').write_text(missing)
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40,
                 dry_run=True, grant_photos_access=True)
    assert not setup.ROOT.exists()
    assert (install / '.env').read_text() == missing
    (install / '.env').write_text(original)
    setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40,
                 grant_photos_access=True)
    assert (install / '.env').read_text().startswith(original)


def test_opt_in_broker_failure_restores_policy_without_restarting_healthy_service(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    original = original.replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', 'NAS_ALLOWED_FOLDERS=Documents,Code')
    (install / '.env').write_text(original)
    monkeypatch.setattr(setup, 'wait_broker', lambda: (_ for _ in ()).throw(ValueError('not ready')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40,
                     grant_photos_access=True)
    assert (install / '.env').read_text() == original
    assert calls == ['start', 'stop']


def test_opt_in_later_probe_failure_restores_policy_and_previous_image(tmp_path, monkeypatch):
    setup, install, original, calls = fixture(tmp_path, monkeypatch)
    original = original.replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', 'NAS_ALLOWED_FOLDERS=Documents,Code')
    (install / '.env').write_text(original)
    monkeypatch.setattr(setup, 'verify_mcp', lambda env: (_ for _ in ()).throw(ValueError('tool unavailable')))
    with pytest.raises(ValueError):
        setup.enable(Path(__file__).resolve().parents[1], install, 'ghcr.io/mattyv/nas-search-mcp:' + 'a' * 40,
                     grant_photos_access=True)
    assert (install / '.env').read_text() == original
    assert ('install', 'ghcr.io/mattyv/nas-search-mcp:' + 'b' * 40) in calls


@pytest.mark.parametrize('original,expected', [
    (b'PRIVATE=keep\r\nNAS_ALLOWED_FOLDERS="Documents, Code"\r\nTAIL=exact\r\n',
     b'PRIVATE=keep\r\nNAS_ALLOWED_FOLDERS="Documents, Code,Photos"\r\nTAIL=exact\r\n'),
    (b'PRIVATE=keep\nNAS_ALLOWED_FOLDERS=Documents',
     b'PRIVATE=keep\nNAS_ALLOWED_FOLDERS=Documents,Photos'),
    (b'PRIVATE=keep', b'PRIVATE=keep\nNAS_ALLOWED_FOLDERS=Photos\n'),
    (b'NAS_ALLOWED_FOLDERS=  "Documents"  \n', b'NAS_ALLOWED_FOLDERS=  "Documents,Photos"  \n'),
])
def test_opt_in_policy_edit_preserves_other_raw_bytes_and_quoting(original, expected):
    setup = module()
    values = setup.read_env(original.decode())
    assert setup.authorize_photos(original, values, True) == expected
    assert 'Photos' in [item.strip() for item in values['NAS_ALLOWED_FOLDERS'].split(',')]


def test_opt_in_flag_is_not_accepted_from_truthy_text():
    setup = module()
    with pytest.raises(ValueError):
        setup.authorize_photos(b'NAS_ALLOWED_FOLDERS=\n', {'NAS_ALLOWED_FOLDERS': ''}, 'false')
