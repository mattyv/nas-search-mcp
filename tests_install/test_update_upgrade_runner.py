import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
from types import SimpleNamespace

import pytest


def module():
    spec = importlib.util.spec_from_file_location('update_upgrade_runner', Path(__file__).parents[1] / 'scripts/update-upgrade-runner.py')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def setup_paths(tmp_path):
    install = tmp_path / 'install'
    install.mkdir(mode=0o700)
    queue = install / 'upgrades'
    queue.mkdir(mode=0o1733)
    queue.chmod(0o1733)
    (queue / 'status.json').write_text(json.dumps({'state': 'succeeded'}))
    (install / 'upgrade-host.py').write_text('#!/usr/bin/env python3\nold runner')
    candidate = tmp_path / 'candidate.py'
    candidate.write_text('#!/usr/bin/env python3\nnew reviewed runner')
    return install, candidate


def test_update_backups_replaces_and_restarts_exact_worker(tmp_path, monkeypatch):
    m = module()
    install, candidate = setup_paths(tmp_path)
    old_sha = hashlib.sha256((install / 'upgrade-host.py').read_bytes()).hexdigest()
    digest = hashlib.sha256(candidate.read_bytes()).hexdigest()
    calls = []
    monkeypatch.setattr(m, 'trusted_directory', lambda *args: None)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    monkeypatch.setattr(m, 'trusted_file', lambda *args: None)
    monkeypatch.setattr(m, 'find_workers', lambda *args: [m.Worker(42, 999)])
    monkeypatch.setattr(m, 'stop_worker', lambda worker, *args: calls.append(('stop', worker.pid)))
    monkeypatch.setattr(m, 'start_worker', lambda *args: (calls.append(('start', Path(args[0] / 'upgrade-host.py').read_text())), SimpleNamespace(pid=42))[1])
    monkeypatch.setattr(m, 'worker_alive', lambda *args: True)
    monkeypatch.setattr(m, 'wait_alive', lambda *args: None)
    m.update_runner(install, candidate, digest, root_uid=os.geteuid())
    assert (install / 'upgrade-host.py').read_text() == '#!/usr/bin/env python3\nnew reviewed runner'
    backup = install / f'.upgrade-host.py.backup-{old_sha}'
    assert backup.read_text() == '#!/usr/bin/env python3\nold runner'
    assert stat.S_IMODE(backup.stat().st_mode) == 0o700
    assert stat.S_IMODE((install / 'upgrade-host.py').stat().st_mode) == 0o700
    assert calls == [('stop', 42), ('start', '#!/usr/bin/env python3\nnew reviewed runner')]


def test_pending_or_running_upgrade_refuses_without_stopping(tmp_path, monkeypatch):
    m = module()
    install, candidate = setup_paths(tmp_path)
    digest = hashlib.sha256(candidate.read_bytes()).hexdigest()
    calls = []
    monkeypatch.setattr(m, 'trusted_directory', lambda *args: None)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    monkeypatch.setattr(m, 'trusted_file', lambda *args: None)
    monkeypatch.setattr(m, 'find_workers', lambda *args: [m.Worker(42, 999)])
    monkeypatch.setattr(m, 'stop_worker', lambda *args: calls.append('stop'))
    (install / 'upgrades' / 'request.json').write_text('{}')
    with pytest.raises(ValueError, match='pending'):
        m.update_runner(install, candidate, digest, root_uid=os.geteuid())
    assert not calls
    (install / 'upgrades' / 'request.json').unlink()
    (install / 'upgrades' / 'status.json').write_text('{"state":"running"}')
    with pytest.raises(ValueError, match='running'):
        m.update_runner(install, candidate, digest, root_uid=os.geteuid())
    assert not calls


def test_wrong_digest_refuses_before_worker_stop(tmp_path, monkeypatch):
    m = module()
    install, candidate = setup_paths(tmp_path)
    monkeypatch.setattr(m, 'trusted_directory', lambda *args: None)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    monkeypatch.setattr(m, 'trusted_file', lambda *args: None)
    monkeypatch.setattr(m, 'find_workers', lambda *args: pytest.fail('worker must not be inspected'))
    with pytest.raises(ValueError, match='digest'):
        m.update_runner(install, candidate, '0' * 64, root_uid=os.geteuid())


def test_restart_failure_restores_backup_and_restarts_previous_runner(tmp_path, monkeypatch):
    m = module()
    install, candidate = setup_paths(tmp_path)
    digest = hashlib.sha256(candidate.read_bytes()).hexdigest()
    launches = []
    monkeypatch.setattr(m, 'trusted_directory', lambda *args: None)
    monkeypatch.setattr(m.os, 'chown', lambda *args: None)
    monkeypatch.setattr(m, 'trusted_file', lambda *args: None)
    monkeypatch.setattr(m, 'find_workers', lambda *args: [m.Worker(42, 999)] if not launches else [])
    monkeypatch.setattr(m, 'stop_worker', lambda *args: None)
    monkeypatch.setattr(m, 'start_worker', lambda install_dir, *args: (launches.append((install_dir / 'upgrade-host.py').read_text()), SimpleNamespace(pid=42))[1])
    checks = []
    def wait_alive(*args):
        checks.append(1)
        if len(checks) == 1:
            raise ValueError('not alive')
    monkeypatch.setattr(m, 'wait_alive', wait_alive)
    with pytest.raises(ValueError, match='restart'):
        m.update_runner(install, candidate, digest, root_uid=os.geteuid())
    assert (install / 'upgrade-host.py').read_text() == '#!/usr/bin/env python3\nold runner'
    assert launches == ['#!/usr/bin/env python3\nnew reviewed runner', '#!/usr/bin/env python3\nold runner']


def test_requires_one_exact_root_worker_and_embeds_no_deployment_path():
    m = module()
    source = Path(__file__).parents[1] / 'scripts/update-upgrade-runner.py'
    assert '/volume' not in source.read_text()
    assert '/volume' not in (source.parent / 'upgrade-host.py').read_text()


def test_wait_alive_requires_same_worker_to_remain_alive(monkeypatch):
    m = module()
    observed = iter([
        [m.Worker(42, 'start-token')],
        [],
    ])
    monkeypatch.setattr(m, 'find_workers', lambda *args: next(observed))
    monkeypatch.setattr(m.time, 'sleep', lambda _: None)
    monkeypatch.setattr(m.time, 'monotonic', iter([0, 0, 0, 0]).__next__)
    with pytest.raises(ValueError, match='restart'):
        m.wait_alive(Path('/srv/worker'), 42, timeout=1, stable_seconds=0.2)


def test_start_worker_refuses_insecure_existing_log(tmp_path, monkeypatch):
    m = module()
    install, _ = setup_paths(tmp_path)
    log = install / 'upgrade-host.log'
    log.write_text('log')
    log.chmod(0o644)
    monkeypatch.setattr(m.os, 'geteuid', lambda: os.geteuid())
    monkeypatch.setattr(m.subprocess, 'Popen', lambda *args, **kwargs: pytest.fail('must not start'))
    with pytest.raises(ValueError, match='Unsafe'):
        m.start_worker(install)
