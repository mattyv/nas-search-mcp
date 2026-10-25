#!/usr/bin/env python3
"""Install or run bounded recovery for this NAS's search and Nord services."""
import fcntl
import importlib.util
import http.client
import json
import os
from pathlib import Path
import signal
import socket
import stat
import subprocess
import sys
import time
import urllib.request

HOME = Path('/volume1/@nas-recovery')
UPGRADER = Path('/volume1/@nas-search-upgrader')
PHOTOS = Path('/volume1/@nas-photos-broker')
DOCKER = '/usr/local/bin/docker'
ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin', 'HOME': '/root'}


class RecoveryError(Exception):
    pass


def trusted(path, directory=False):
    for item in (path, *path.parents):
        info = item.lstat()
        expected = stat.S_ISDIR if item != path or directory else stat.S_ISREG
        if not expected(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise RecoveryError('untrusted-installation')


def command(args, timeout=30):
    try:
        result = subprocess.run(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, env=ENV)
    except (OSError, subprocess.TimeoutExpired):
        raise RecoveryError('command-unavailable') from None
    if result.returncode:
        raise RecoveryError('command-failed')
    return result.stdout.decode('utf-8')


def recover_search(info, perform):
    if info.get('project') != 'nas-search' or info.get('service') != 'nas-search':
        return 'identity-mismatch'
    if info['status'] in ('created', 'exited'):
        perform('start-search')
        return 'start-requested'
    return 'running' if info['status'] == 'running' else 'needs-attention'


def worker_running():
    path = UPGRADER / '.upgrade-host.lock'
    fd = os.open(str(path), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return False
        except BlockingIOError:
            return True
    finally:
        os.close(fd)


def start_worker():
    trusted(UPGRADER, True)
    trusted(UPGRADER / 'upgrade-host.py')
    fd = os.open(str(UPGRADER / 'upgrade-host.log'), os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'ab') as log:
        subprocess.Popen(['/usr/bin/python3', str(UPGRADER / 'upgrade-host.py'),
                          '--install-dir', str(UPGRADER)], stdin=subprocess.DEVNULL,
                         stdout=log, stderr=log, cwd=str(UPGRADER), env=ENV,
                         start_new_session=True, close_fds=True)


def check_worker():
    trusted(UPGRADER, True)
    if worker_running():
        return 'running'
    start_worker()
    return 'start-requested'


def worker_protocol():
    path = UPGRADER / '.recovery-protocol.json'
    trusted(path)
    record = json.loads(path.read_text())
    if record.get('version') != 1 or type(record.get('pid')) is not int or record['pid'] <= 1:
        return False
    proc = Path('/proc') / str(record['pid'])
    identity = (proc / 'stat').read_text().rsplit(')', 1)[1].split()[19]
    args = (proc / 'cmdline').read_bytes().rstrip(b'\0').split(b'\0')
    expected = [str(UPGRADER / 'upgrade-host.py').encode(), b'--install-dir', str(UPGRADER).encode()]
    uid = next(line for line in (proc / 'status').read_text().splitlines() if line.startswith('Uid:')).split()[1:]
    return (identity == record.get('start') and len(args) == 4
            and Path(os.fsdecode(args[0])).name.startswith('python') and args[1:] == expected
            and uid == ['0', '0', '0', '0'])


def check_search():
    trusted(UPGRADER, True)
    fd = os.open(str(UPGRADER / '.deployment.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return 'upgrade-running'
        return search_under_lock()
    finally:
        os.close(fd)


def search_under_lock():
    template = ('{"project":{{json (index .Config.Labels "com.docker.compose.project")}},'
                '"service":{{json (index .Config.Labels "com.docker.compose.service")}},'
                '"status":{{json .State.Status}}}')
    info = json.loads(command([DOCKER, 'inspect', '--format', template, 'nas-search-nas-search-1']))

    def perform(action):
        # Until a patched worker is running, legacy upgrades do not share our lock.
        # Reporting healthy services remains safe; automatic starts must wait.
        if not worker_protocol():
            raise RecoveryError('worker-update-required')
        path = HOME / 'search-attempts.json'
        attempts = json.loads(path.read_text()) if path.exists() else []
        if not isinstance(attempts, list) or any(type(item) not in (int, float) for item in attempts):
            raise RecoveryError('invalid-retry-state')
        attempts = [item for item in attempts if item > time.time() - 3600]
        if len(attempts) >= 3:
            raise RecoveryError('retry-limit')
        attempts.append(time.time())
        temporary = path.with_name(path.name + '.new')
        fd = os.open(str(temporary), os.O_WRONLY | os.O_TRUNC | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(attempts, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(str(temporary), str(path))
        command([DOCKER, 'start', 'nas-search-nas-search-1'])

    result = recover_search(info, perform)
    if result == 'running':
        for name, port, route in [('search', 8080, '/healthz'),
                                  ('mobile', 8082, '/.well-known/oauth-authorization-server')]:
            with urllib.request.urlopen('http://127.0.0.1:' + str(port) + route, timeout=5) as response:
                if response.status != 200:
                    return name + '-unhealthy'
    return result


def check_nord():
    path = HOME / 'nord-exit-recovery.py'
    trusted(path)
    spec = importlib.util.spec_from_file_location('nord_recovery', str(path))
    nord = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(nord)
    return nord.run_once()


def check_photos():
    if not PHOTOS.exists() and not PHOTOS.is_symlink():
        return 'disabled'
    trusted(PHOTOS, True)
    script = PHOTOS / 'synology-photos-host.py'
    trusted(script)
    trusted(PHOTOS / 'run', True)
    fd = os.open(str(PHOTOS / '.host.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            connection = http.client.HTTPConnection('synology-photos', timeout=3)
            connection.sock = socket.socket(socket.AF_UNIX)
            connection.sock.settimeout(3)
            try:
                connection.sock.connect(str(PHOTOS / 'run' / 'photos.sock'))
                connection.request('POST', '/health', body='{}', headers={'Content-Type': 'application/json'})
                response = connection.getresponse()
                if response.status == 200 and json.loads(response.read(1025)) == {'status': 'ok'}:
                    return 'running'
                return 'needs-attention'
            finally:
                connection.close()
        # Release the probe lock before the child tries to take its lifetime lock.
        fcntl.flock(fd, fcntl.LOCK_UN)
        subprocess.Popen(['/usr/bin/python3', str(script), 'serve'], stdin=subprocess.DEVNULL,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, cwd=str(PHOTOS),
                         env=ENV, close_fds=True, start_new_session=True)
        return 'start-requested'
    finally:
        os.close(fd)


def check_all(checks):
    results = {}
    for name, check in checks.items():
        try:
            results[name] = check()
        except Exception:
            results[name] = 'check-failed'
    return results


def process_identity(proc):
    return (proc / 'stat').read_text().rsplit(')', 1)[1].split()[19]


def matching_workers(proc_root=Path('/proc')):
    matches = []
    expected = [str(UPGRADER / 'upgrade-host.py').encode(), b'--install-dir', str(UPGRADER).encode()]
    for proc in proc_root.iterdir():
        if not proc.name.isdigit():
            continue
        try:
            identity = process_identity(proc)
            args = (proc / 'cmdline').read_bytes().rstrip(b'\0').split(b'\0')
            if len(args) != 4 or not Path(os.fsdecode(args[0])).name.startswith('python') or args[1:] != expected:
                continue
            uid = next(line for line in (proc / 'status').read_text().splitlines() if line.startswith('Uid:')).split()[1:]
            if uid == ['0', '0', '0', '0'] and process_identity(proc) == identity:
                matches.append((proc, int(proc.name), identity))
        except (FileNotFoundError, ProcessLookupError):
            continue
    return matches


def atomic_script(path, body):
    temporary = path.with_name(path.name + '.new')
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o700)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(body)
        stream.flush()
        os.fsync(stream.fileno())
    os.chown(str(temporary), 0, 0)
    temporary.chmod(0o700)
    os.replace(str(temporary), str(path))


def update_worker(body):
    """Replace only an idle, identified worker; never interrupt an installer."""
    trusted(UPGRADER, True)
    worker = UPGRADER / 'upgrade-host.py'
    trusted(worker)
    paused = []
    try:
        for proc, pid, identity in matching_workers():
            if process_identity(proc) != identity:
                raise RecoveryError('worker-changed')
            os.kill(pid, signal.SIGSTOP)
            paused.append((proc, pid, identity))
            deadline = time.monotonic() + 5
            while True:
                if process_identity(proc) != identity:
                    raise RecoveryError('worker-changed')
                state = next(line for line in (proc / 'status').read_text().splitlines()
                             if line.startswith('State:')).split()[1]
                if state in ('T', 't'):
                    break
                if time.monotonic() > deadline:
                    raise RecoveryError('worker-pause-failed')
                time.sleep(0.05)
        request = UPGRADER / 'upgrades' / 'request.json'
        status = UPGRADER / 'upgrades' / 'status.json'
        if request.exists() or request.is_symlink():
            raise RecoveryError('upgrade-pending')
        if status.exists() or status.is_symlink():
            info = status.lstat()
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022
                    or json.loads(status.read_text()).get('state') == 'running'):
                raise RecoveryError('upgrade-running')
        backup = UPGRADER / 'upgrade-host.before-recovery.py'
        if not backup.exists() and not backup.is_symlink():
            atomic_script(backup, worker.read_bytes())
        else:
            trusted(backup)
        atomic_script(worker, body)
        for proc, pid, identity in paused:
            if process_identity(proc) != identity:
                raise RecoveryError('worker-changed')
            os.kill(pid, signal.SIGTERM)
            os.kill(pid, signal.SIGCONT)
        deadline = time.monotonic() + 5
        while worker_running():
            if time.monotonic() > deadline:
                raise RecoveryError('worker-stop-failed')
            time.sleep(0.05)
        start_worker()
        deadline = time.monotonic() + 5
        while True:
            try:
                if worker_running() and worker_protocol():
                    break
            except (OSError, ValueError):
                pass
            if time.monotonic() > deadline:
                raise RecoveryError('worker-start-failed')
            time.sleep(0.05)
    finally:
        for proc, pid, identity in paused:
            try:
                if process_identity(proc) == identity:
                    os.kill(pid, signal.SIGCONT)
            except (FileNotFoundError, ProcessLookupError):
                pass


def install():
    if HOME.exists() or HOME.is_symlink():
        trusted(HOME, True)
    else:
        trusted(HOME.parent, True)
        HOME.mkdir(mode=0o700)
    source = Path(__file__).resolve().parent
    # Read all reviewed inputs before any replacement. The caller authorizes these
    # staged sources explicitly; normal recovery executes only protected copies.
    bodies = {name: (source / name).read_bytes()
              for name in ('nas-recovery.py', 'nord-exit-recovery.py', 'upgrade-host.py')}
    update_worker(bodies['upgrade-host.py'])
    for name in ('nas-recovery.py', 'nord-exit-recovery.py'):
        atomic_script(HOME / name, bodies[name])
    print('Recovery installed. Add root boot and five-minute DSM tasks; see the setup instructions.')


def main():
    if os.geteuid() != 0 or sys.argv[1:] not in ([], ['--install']):
        print('Run as root, with --install for setup or without arguments for a recovery check.')
        return 1
    os.umask(0o077)
    try:
        if sys.argv[1:] == ['--install']:
            install()
            return 0
        trusted(HOME, True)
        trusted(HOME / 'nas-recovery.py')
        fd = os.open(str(HOME / 'supervisor.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                print('NAS recovery: already-running')
                return 0
            results = check_all({'worker': check_worker, 'search': check_search, 'nord': check_nord, 'photos': check_photos})
            for name, result in results.items():
                print('NAS recovery ' + name + ': ' + result)
            return 0 if all(item in ('ready', 'running', 'disabled') for item in results.values()) else 1
        finally:
            os.close(fd)
    except Exception:
        print('NAS recovery: check-failed; inspect protected installation.')
        return 1


if __name__ == '__main__':
    sys.exit(main())
