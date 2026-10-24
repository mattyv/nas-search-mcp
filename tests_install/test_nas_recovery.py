import importlib.util
from pathlib import Path
from unittest.mock import patch


def module():
    path = Path(__file__).resolve().parents[1] / 'scripts/nas-recovery.py'
    spec = importlib.util.spec_from_file_location('nas_recovery', path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def test_search_start_requires_exact_project_and_service():
    recovery = module()
    actions = []
    for project, service in [('other', 'nas-search'), ('nas-search', 'other')]:
        assert recovery.recover_search({'project': project, 'service': service, 'status': 'exited'},
                                       actions.append) == 'identity-mismatch'
    assert actions == []


def test_only_stopped_search_is_started_and_running_search_is_not_restarted():
    recovery = module()
    actions = []
    info = {'project': 'nas-search', 'service': 'nas-search', 'status': 'running'}
    assert recovery.recover_search(info, actions.append) == 'running'
    assert actions == []
    info['status'] = 'exited'
    assert recovery.recover_search(info, actions.append) == 'start-requested'
    assert actions == ['start-search']


def test_upgrade_lock_held_means_do_not_touch_search(tmp_path):
    import fcntl
    recovery = module()
    with patch.object(recovery, 'UPGRADER', tmp_path):
        with (tmp_path / '.upgrade-host.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            assert recovery.worker_running()
        assert not recovery.worker_running()


def test_worker_start_is_detached_and_preserves_existing_script(tmp_path):
    recovery = module()
    worker = tmp_path / 'upgrade-host.py'
    worker.write_text('existing-version')
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery.subprocess, 'Popen') as launch:
        recovery.start_worker()
    assert worker.read_text() == 'existing-version'
    args, options = launch.call_args
    assert args[0] == ['/usr/bin/python3', str(worker), '--install-dir', str(tmp_path)]
    assert options['start_new_session'] and options['close_fds']


def test_subsystem_error_does_not_skip_other_checks():
    recovery = module()
    calls = []
    def failure():
        raise OSError('secret-error')
    result = recovery.check_all({'nord': failure, 'search': lambda: calls.append('search') or 'running',
                                 'worker': lambda: calls.append('worker') or 'running'})
    assert calls == ['search', 'worker']
    assert result == {'nord': 'check-failed', 'search': 'running', 'worker': 'running'}


def test_installer_will_not_replace_an_existing_untrusted_directory(tmp_path):
    recovery = module()
    target = tmp_path / 'recovery'
    target.mkdir(mode=0o777)
    target.chmod(0o777)
    with patch.object(recovery, 'HOME', target):
        try:
            recovery.install()
        except recovery.RecoveryError:
            pass
        else:
            raise AssertionError('untrusted target was accepted')
    assert list(target.iterdir()) == []


def test_search_recovery_and_upgrade_share_exclusion(tmp_path):
    import fcntl
    recovery = module()
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery, 'search_under_lock') as start:
        with (tmp_path / '.deployment.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            assert recovery.check_search() == 'upgrade-running'
        start.assert_not_called()
        recovery.check_search()
        start.assert_called_once()


def test_stale_running_status_does_not_block_recovery(tmp_path):
    recovery = module()
    (tmp_path / 'upgrades').mkdir()
    (tmp_path / 'upgrades' / 'status.json').write_text('{"state":"running"}')
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery, 'search_under_lock', return_value='running'):
        assert recovery.check_search() == 'running'


def test_worker_replacement_refuses_pending_upgrade(tmp_path):
    recovery = module()
    (tmp_path / 'upgrades').mkdir()
    (tmp_path / 'upgrades' / 'request.json').write_text('{"action":"upgrade_main"}')
    worker = tmp_path / 'upgrade-host.py'
    worker.write_text('old-worker')
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery, 'matching_workers', return_value=[]):
        try:
            recovery.update_worker(b'new-worker')
        except recovery.RecoveryError:
            pass
        else:
            raise AssertionError('pending upgrade ignored')
    assert worker.read_text() == 'old-worker'


def test_idle_worker_update_preserves_backup_and_requires_new_protocol(tmp_path):
    recovery = module()
    (tmp_path / 'upgrades').mkdir()
    (tmp_path / 'upgrade-host.py').write_bytes(b'old-worker')
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery, 'matching_workers', return_value=[]), \
            patch.object(recovery, 'worker_running', side_effect=[False, True]), \
            patch.object(recovery, 'worker_protocol', return_value=True), \
            patch.object(recovery, 'start_worker') as start, patch.object(recovery.os, 'chown'):
        recovery.update_worker(b'new-worker')
    assert (tmp_path / 'upgrade-host.py').read_bytes() == b'new-worker'
    assert (tmp_path / 'upgrade-host.before-recovery.py').read_bytes() == b'old-worker'
    start.assert_called_once()


def test_process_matching_requires_exact_command_root_and_stable_identity(tmp_path):
    recovery = module()
    def process(pid, uid, args):
        directory = tmp_path / str(pid)
        directory.mkdir()
        (directory / 'stat').write_text(str(pid) + ' (python3) ' + ' '.join(['S'] + ['0'] * 18 + ['12345']))
        (directory / 'status').write_text('Uid:\t' + '\t'.join([str(uid)] * 4) + '\n')
        (directory / 'cmdline').write_bytes(b'\0'.join(item.encode() for item in args) + b'\0')
    expected = ['/usr/bin/python3', str(recovery.UPGRADER / 'upgrade-host.py'), '--install-dir', str(recovery.UPGRADER)]
    process(111, 0, expected)
    process(222, 1026, expected)
    process(333, 0, expected + ['--once'])
    process(444, 0, ['/usr/bin/python3', '/unrelated.py'])
    assert [(pid, identity) for _, pid, identity in recovery.matching_workers(tmp_path)] == [(111, '12345')]


def test_paused_worker_is_resumed_when_update_refuses_pending_request(tmp_path):
    import signal
    recovery = module()
    (tmp_path / 'upgrades').mkdir()
    (tmp_path / 'upgrades' / 'request.json').write_text('{}')
    (tmp_path / 'upgrade-host.py').write_bytes(b'old-worker')
    proc = tmp_path / 'proc'
    proc.mkdir()
    (proc / 'status').write_text('State:\tT (stopped)\n')
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery, 'matching_workers', return_value=[(proc, 123, 'identity')]), \
            patch.object(recovery, 'process_identity', return_value='identity'), \
            patch.object(recovery.os, 'kill') as kill:
        try:
            recovery.update_worker(b'new-worker')
        except recovery.RecoveryError:
            pass
        else:
            raise AssertionError('pending upgrade ignored')
    assert [call.args for call in kill.call_args_list] == [(123, signal.SIGSTOP), (123, signal.SIGCONT)]
    assert (tmp_path / 'upgrade-host.py').read_bytes() == b'old-worker'


def test_worker_update_signal_order_and_verified_restart(tmp_path):
    import signal
    recovery = module()
    (tmp_path / 'upgrades').mkdir()
    (tmp_path / 'upgrade-host.py').write_bytes(b'old-worker')
    proc = tmp_path / 'proc'
    proc.mkdir()
    (proc / 'status').write_text('State:\tT (stopped)\n')
    with patch.object(recovery, 'UPGRADER', tmp_path), patch.object(recovery, 'trusted'), \
            patch.object(recovery, 'matching_workers', return_value=[(proc, 123, 'identity')]), \
            patch.object(recovery, 'process_identity', return_value='identity'), \
            patch.object(recovery, 'worker_running', side_effect=[False, True]), \
            patch.object(recovery, 'worker_protocol', return_value=True) as protocol, \
            patch.object(recovery, 'start_worker') as start, patch.object(recovery.os, 'chown'), \
            patch.object(recovery.os, 'kill') as kill:
        recovery.update_worker(b'new-worker')
    assert [call.args for call in kill.call_args_list][:3] == [
        (123, signal.SIGSTOP), (123, signal.SIGTERM), (123, signal.SIGCONT)]
    start.assert_called_once()
    protocol.assert_called_once()
    assert (tmp_path / 'upgrade-host.before-recovery.py').read_bytes() == b'old-worker'
