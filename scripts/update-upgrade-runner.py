#!/usr/bin/env python3
"""Safely replace and restart the protected root upgrade worker."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import tempfile
import time


class Worker:
    def __init__(self, pid, identity):
        self.pid = pid
        self.identity = identity


def trusted_directory(path, uid=0):
    if not path.is_absolute() or '..' in path.parts:
        raise ValueError('Unsafe directory')
    for directory in (path, *path.parents):
        info = directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != uid or info.st_mode & 0o022:
            raise ValueError('Unsafe directory')


def trusted_file(path, uid=0):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_mode & 0o022:
        raise ValueError('Unsafe file')


def process_identity(directory):
    return (directory / 'stat').read_text().rsplit(')', 1)[1].split()[19]


def worker_matches(directory, install_dir):
    args = (directory / 'cmdline').read_bytes().rstrip(b'\0').split(b'\0')
    expected = [str(install_dir / 'upgrade-host.py').encode(), b'--install-dir', str(install_dir).encode()]
    if len(args) != 4 or not Path(os.fsdecode(args[0])).name.startswith('python') or args[1:] != expected:
        return False
    status = (directory / 'status').read_text().splitlines()
    uid = next(line for line in status if line.startswith('Uid:')).split()[1:]
    return len(uid) == 4 and all(value == '0' for value in uid)


def find_workers(install_dir, proc=Path('/proc')):
    workers = []
    for directory in proc.iterdir():
        if not directory.name.isdigit():
            continue
        try:
            identity = process_identity(directory)
            if worker_matches(directory, install_dir) and process_identity(directory) == identity:
                workers.append(Worker(int(directory.name), identity))
        except (FileNotFoundError, ProcessLookupError, StopIteration, IndexError):
            continue
    return workers


def check_idle(install_dir, root_uid=0):
    queue = install_dir / 'upgrades'
    info = queue.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != root_uid or stat.S_IMODE(info.st_mode) != 0o1733:
        raise ValueError('Unsafe upgrade queue')
    request = queue / 'request.json'
    if request.exists() or request.is_symlink():
        raise ValueError('Upgrade is pending')
    status = queue / 'status.json'
    if status.exists() or status.is_symlink():
        trusted_file(status, root_uid)
        if json.loads(status.read_text()).get('state') == 'running':
            raise ValueError('Upgrade is running')


def wait_stopped(worker, install_dir, timeout=10):
    proc = Path('/proc') / str(worker.pid)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if process_identity(proc) != worker.identity or not worker_matches(proc, install_dir):
                return
        except (FileNotFoundError, ProcessLookupError):
            return
        time.sleep(0.1)
    raise ValueError('Upgrade worker did not stop')


def stop_worker(worker, install_dir):
    proc = Path('/proc') / str(worker.pid)
    if process_identity(proc) != worker.identity or not worker_matches(proc, install_dir):
        raise ValueError('Upgrade worker identity changed')
    os.kill(worker.pid, signal.SIGTERM)
    wait_stopped(worker, install_dir)


def atomic_install(path, contents, uid=0):
    fd, name = tempfile.mkstemp(prefix='.upgrade-host-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(contents)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(name, 0o700)
        os.chown(name, uid, uid)
        os.replace(name, path)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def backup_file(source, backup, uid=0):
    fd = os.open(backup, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o700)
    try:
        with os.fdopen(fd, 'wb') as output, source.open('rb') as input_file:
            while chunk := input_file.read(65536):
                output.write(chunk)
            output.flush()
            os.fsync(output.fileno())
        os.chown(backup, uid, uid)
        os.chmod(backup, 0o700)
    except Exception:
        backup.unlink(missing_ok=True)
        raise


def start_worker(install_dir):
    log_path = install_dir / 'upgrade-host.log'
    trusted_file(log_path)
    flags = os.O_WRONLY | os.O_APPEND | os.O_NOFOLLOW
    log_fd = os.open(log_path, flags)
    info = os.fstat(log_fd)
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        os.close(log_fd)
        raise ValueError('Unsafe upgrade worker log')
    with os.fdopen(log_fd, 'ab') as log, open(os.devnull, 'rb') as input_stream:
        return subprocess.Popen(['/usr/bin/python3', str(install_dir / 'upgrade-host.py'), '--install-dir', str(install_dir)], stdin=input_stream, stdout=log, stderr=log, start_new_session=True, close_fds=True, cwd=install_dir)


def worker_alive(install_dir, proc=Path('/proc')):
    return bool(find_workers(install_dir, proc))


def wait_alive(install_dir, pid, timeout=5, stable_seconds=2):
    deadline = time.monotonic() + timeout
    identity = None
    stable_since = None
    while time.monotonic() < deadline:
        workers = [worker for worker in find_workers(install_dir) if worker.pid == pid]
        if not workers:
            raise ValueError('Upgrade worker restart failed')
        if identity is None:
            identity = workers[0].identity
            stable_since = time.monotonic()
        elif workers[0].identity != identity:
            raise ValueError('Upgrade worker restart failed')
        elif time.monotonic() - stable_since >= stable_seconds:
            return
        time.sleep(0.1)
    raise ValueError('Upgrade worker restart failed')


def update_runner(install_dir, candidate, expected_sha256, root_uid=0):
    trusted_directory(install_dir, root_uid)
    trusted_directory(candidate.parent, root_uid)
    worker = install_dir / 'upgrade-host.py'
    trusted_file(worker, root_uid)
    trusted_file(candidate, root_uid)
    if candidate == worker:
        raise ValueError('Candidate must be staged separately')
    if not re.fullmatch(r'[0-9a-f]{64}', expected_sha256):
        raise ValueError('Invalid candidate digest')
    contents = candidate.read_bytes()
    if hashlib.sha256(contents).hexdigest() != expected_sha256:
        raise ValueError('Candidate digest mismatch')
    if not contents.startswith(b'#!/usr/bin/env python3\n'):
        raise ValueError('Candidate is not a reviewed Python worker')
    backup = install_dir / ('.upgrade-host.py.backup-' + hashlib.sha256(worker.read_bytes()).hexdigest())
    if backup.exists() or backup.is_symlink():
        raise ValueError('Runner backup already exists')
    check_idle(install_dir, root_uid)
    workers = find_workers(install_dir)
    if len(workers) != 1:
        raise ValueError('Expected exactly one root upgrade worker')
    backup_file(worker, backup, root_uid)
    replaced = False
    stopped = False
    try:
        check_idle(install_dir, root_uid)
        stop_worker(workers[0], install_dir)
        stopped = True
        check_idle(install_dir, root_uid)
        atomic_install(worker, contents, root_uid)
        replaced = True
        launched = start_worker(install_dir)
        wait_alive(install_dir, launched.pid)
    except Exception:
        try:
            if replaced:
                for current in find_workers(install_dir):
                    try:
                        stop_worker(current, install_dir)
                    except Exception:
                        pass
                atomic_install(worker, backup.read_bytes(), root_uid)
            if stopped:
                launched = start_worker(install_dir)
                wait_alive(install_dir, launched.pid)
        except Exception:
            raise ValueError('Runner update failed; automatic recovery needs inspection')
        raise ValueError('Runner update failed; the previous runner was restored and restarted')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', type=Path, required=True, help='Root-owned reviewed upgrade-host.py staged on the NAS')
    parser.add_argument('--sha256', required=True, help='Expected SHA-256 of the reviewed candidate')
    parser.add_argument('--install-dir', type=Path, required=True, help='Root-owned protected upgrade directory')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run as root on the NAS')
    os.umask(0o077)
    try:
        update_runner(args.install_dir, args.candidate, args.sha256)
    except Exception:
        parser.exit(1, 'Upgrade worker update failed. The existing worker was restored where possible; inspect the protected host log.\n')
    print('Upgrade worker updated and restarted.')


if __name__ == '__main__':
    main()
