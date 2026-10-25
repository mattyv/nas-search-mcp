#!/usr/bin/env python3
"""Scheduled recovery for the existing Nord exit stack. No credentials are read."""
import calendar
import fcntl
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import time

DOCKER = '/usr/local/bin/docker'
STACK = Path('/volume1/docker/nord-exit')
HOME = Path('/volume1/@nas-recovery')
ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin', 'HOME': '/root'}


class RecoveryError(Exception):
    pass


def command(args, timeout=45):
    try:
        result = subprocess.run(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, env=ENV)
    except (OSError, subprocess.TimeoutExpired):
        raise RecoveryError('command-unavailable-or-timed-out') from None
    if result.returncode:
        raise RecoveryError('command-failed')
    return result.stdout.decode('utf-8', errors='strict')


def valid_tun(path):
    try:
        info = path.lstat()
        return (stat.S_ISCHR(info.st_mode) and os.major(info.st_rdev) == 10
                and os.minor(info.st_rdev) == 200)
    except OSError:
        return False


def ensure_tun():
    """Restore the standard kernel device, without replacing existing objects."""
    directory = Path('/dev/net')
    device = directory / 'tun'
    try:
        if 'tun' not in [line.split()[-1] for line in Path('/proc/misc').read_text().splitlines()]:
            command(['/usr/sbin/modprobe', 'tun'])
        directory.mkdir(mode=0o755, exist_ok=True)
        if directory.is_symlink() or not directory.is_dir():
            return False
        if not device.exists() and not device.is_symlink():
            os.mknod(str(device), stat.S_IFCHR | 0o600, os.makedev(10, 200))
        return valid_tun(device)
    except (OSError, RecoveryError):
        return False


def reconcile(vpn, tail, state, now, prepare_tun, perform):
    """One bounded repair step. Wait for VPN health before changing Tailscale."""
    if any(item.get('project') != 'nord-exit' or item.get('service') != service
           for item, service in ((vpn, 'gluetun'), (tail, 'tailscale'))):
        return 'identity-mismatch'
    if tail.get('network') != 'container:' + vpn['id']:
        return 'network-identity-mismatch'
    action = None
    if vpn['status'] in ('created', 'exited'):
        if not prepare_tun():
            return 'waiting-for-tun'
        action = 'start-vpn'
    elif vpn['status'] != 'running':
        return 'needs-attention'
    elif vpn['health'] != 'healthy':
        # Gluetun already retries VPN servers. Do not interrupt its recovery.
        return 'waiting-for-vpn'
    elif tail['status'] in ('created', 'exited'):
        action = 'start-tailscale'
    elif tail['status'] != 'running':
        return 'needs-attention'
    elif tail['started'] < vpn['started']:
        action = 'recreate-tailscale'
    else:
        return 'ready'
    attempts = state.get('attempts', [])
    if (not isinstance(attempts, list) or any(type(item) not in (int, float) for item in attempts)):
        raise RecoveryError('invalid-retry-state')
    state['attempts'] = [item for item in attempts if item > now - 3600]
    if len(state['attempts']) >= 3:
        return 'retry-limit'
    state['attempts'].append(now)
    perform(action)  # Caller persists the attempt before invoking Docker.
    return {'start-vpn': 'vpn-start-requested', 'start-tailscale': 'tailscale-start-requested',
            'recreate-tailscale': 'tailscale-recreate-requested'}[action]


def trusted(path, directory=False):
    info = path.lstat()
    expected = stat.S_ISDIR if directory else stat.S_ISREG
    if not expected(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
        raise RecoveryError('untrusted-installation')


def inspect(service):
    # Select fields explicitly. Never request Docker's credential-bearing Config.Env.
    fields = {'project': '(index .Config.Labels "com.docker.compose.project")',
              'service': '(index .Config.Labels "com.docker.compose.service")',
              'id': '.Id', 'status': '.State.Status', 'started': '.State.StartedAt',
              'network': '.HostConfig.NetworkMode'}
    template = '{' + ','.join('"' + key + '":{{json ' + value + '}}'
                              for key, value in fields.items())
    template += ',"health":{{if .State.Health}}{{json .State.Health.Status}}{{else}}""{{end}}}'
    value = json.loads(command([DOCKER, 'inspect', '--format', template, 'nord-exit-' + service + '-1']))
    started = value['started']
    # Docker timestamps are UTC with fractional seconds; retain subsecond ordering.
    whole = calendar.timegm(time.strptime(started[:19], '%Y-%m-%dT%H:%M:%S'))
    fraction = started[19:].rstrip('Z')
    value['started'] = whole + (float('0' + fraction) if fraction else 0)
    return value


def save(state):
    temporary = HOME / 'state.new'
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(state, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(str(temporary), str(HOME / 'state.json'))


def run_once():
    trusted(HOME, True)
    lock = os.open(str(HOME / 'lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return 'already-running'
        state_path = HOME / 'state.json'
        if state_path.exists():
            trusted(state_path)
            state = json.loads(state_path.read_text())
            if not isinstance(state, dict):
                raise RecoveryError('invalid-retry-state')
        else:
            state = {}
        vpn, tail = inspect('gluetun'), inspect('tailscale')

        def perform(action):
            save(state)
            if action == 'start-vpn':
                command([DOCKER, 'start', 'nord-exit-gluetun-1'])
            else:
                # Restart reattaches the existing container to the VPN's current
                # namespace. Never load Compose from the shared Docker directory.
                verb = 'start' if action == 'start-tailscale' else 'restart'
                command([DOCKER, verb, 'nord-exit-tailscale-1'])

        result = reconcile(vpn, tail, state, time.time(), ensure_tun, perform)
        save(state)
        if result == 'ready':
            connection = json.loads(command([DOCKER, 'exec', 'nord-exit-tailscale-1',
                                             'tailscale', 'status', '--json']))
            if connection.get('BackendState') != 'Running' or not connection.get('Self', {}).get('Online'):
                return 'waiting-for-tailscale-network'
        return result
    finally:
        os.close(lock)


def main():
    if os.geteuid() != 0 or len(sys.argv) != 1:
        print('Run the installed recovery script as root, without arguments.')
        return 1
    os.umask(0o077)
    try:
        result = run_once()
    except (RecoveryError, OSError, ValueError, KeyError, TypeError, OverflowError):
        # Neither Docker errors nor exception text may reveal private settings.
        print('Nord recovery: check-failed; inspect protected stack status.')
        return 1
    print('Nord recovery: ' + result)
    return 0 if result == 'ready' else 1


if __name__ == '__main__':
    sys.exit(main())
