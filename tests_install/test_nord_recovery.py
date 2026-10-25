import importlib.util
from pathlib import Path

import pytest


def module():
    path = Path(__file__).resolve().parents[1] / 'scripts/nord-exit-recovery.py'
    spec = importlib.util.spec_from_file_location('nord_recovery', path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def container(service, status='running', health='healthy', started=100):
    return {'service': service, 'project': 'nord-exit', 'status': status,
            'health': health, 'started': started, 'id': service + '-id',
            'network': 'container:gluetun-id'}


def check(vpn=None, tail=None, state=None, now=1000, tun=True):
    recovery = module()
    actions = []
    result = recovery.reconcile(
        vpn or container('gluetun'), tail or container('tailscale', started=110),
        {} if state is None else state, now, lambda: tun,
        lambda action: actions.append(action))
    return result, actions


def test_healthy_stack_is_untouched():
    assert check() == ('ready', [])


def test_stopped_vpn_restores_tun_before_start_and_defers_tailscale():
    assert check(vpn=container('gluetun', 'exited'), tail=container('tailscale', 'exited')) == (
        'vpn-start-requested', ['start-vpn'])


def test_missing_tun_prevents_container_start():
    assert check(vpn=container('gluetun', 'exited'), tun=False) == ('waiting-for-tun', [])


@pytest.mark.parametrize('health', ['starting', 'unhealthy', ''])
def test_unhealthy_vpn_waits_for_existing_vpn_retries(health):
    assert check(vpn=container('gluetun', health=health), tail=container('tailscale', 'created')) == (
        'waiting-for-vpn', [])


@pytest.mark.parametrize('status', ['created', 'exited'])
def test_late_vpn_success_starts_waiting_tailscale(status):
    assert check(tail=container('tailscale', status)) == (
        'tailscale-start-requested', ['start-tailscale'])


def test_vpn_restart_refreshes_old_shared_network_namespace():
    assert check(vpn=container('gluetun', started=200), tail=container('tailscale', started=100)) == (
        'tailscale-recreate-requested', ['recreate-tailscale'])


def test_recovery_is_limited_and_clock_rollback_does_not_reset_budget():
    state = {'attempts': [900, 950, 1100]}
    assert check(tail=container('tailscale', 'exited'), state=state) == ('retry-limit', [])
    assert state['attempts'] == [900, 950, 1100]


def test_old_attempts_expire_and_action_is_recorded_before_execution():
    recovery = module()
    state = {'attempts': [1, 2, 3]}
    seen = []
    recovery.reconcile(container('gluetun'), container('tailscale', 'exited'), state,
                       5000, lambda: True, lambda action: seen.append(list(state['attempts'])))
    assert seen == [[5000]]


def test_other_project_or_wrong_service_is_never_changed():
    vpn = container('gluetun', 'exited')
    vpn['project'] = 'unrelated'
    assert check(vpn=vpn) == ('identity-mismatch', [])
    assert check(tail=container('other', 'exited')) == ('identity-mismatch', [])


def test_restarting_or_paused_container_is_not_interrupted():
    for status in ('restarting', 'paused', 'removing', 'dead'):
        assert check(vpn=container('gluetun', status)) == ('needs-attention', [])


def test_device_creation_rejects_symlinks_and_wrong_device(tmp_path):
    recovery = module()
    device = tmp_path / 'tun'
    target = tmp_path / 'target'
    target.write_text('original')
    device.symlink_to(target)
    assert not recovery.valid_tun(device)
    device.unlink()
    device.write_text('ordinary file')
    assert not recovery.valid_tun(device)
    assert target.read_text() == 'original'


def test_command_failure_does_not_disclose_output(capsys):
    recovery = module()
    with pytest.raises(recovery.RecoveryError) as error:
        recovery.command(['/bin/sh', '-c', 'echo private-password >&2; exit 1'])
    assert 'private-password' not in str(error.value)
    assert 'private-password' not in str(capsys.readouterr())


def test_inspection_template_groups_label_lookups():
    from unittest.mock import patch
    import json
    recovery = module()
    value = container('gluetun')
    value['started'] = '2026-10-24T10:43:00.123456789Z'
    with patch.object(recovery, 'command', return_value=json.dumps(value)) as run:
        recovery.inspect('gluetun')
    template = run.call_args.args[0][3]
    assert '{{json (index .Config.Labels "com.docker.compose.project")}}' in template


def test_stale_container_reference_does_not_restart_against_wrong_vpn():
    tail = container('tailscale', 'exited')
    tail['network'] = 'container:old-vpn-id'
    assert check(tail=tail) == ('network-identity-mismatch', [])
