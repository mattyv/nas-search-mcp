#!/usr/bin/env python3
"""Host-only upgrade worker. Never mount Docker access into the application."""
import argparse
import fcntl
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile
import time
import urllib.request

API = 'https://api.github.com/repos/mattyv/nas-search-mcp'
MAX_ARCHIVE = 64 * 1024 * 1024
MAX_EXPANDED = 256 * 1024 * 1024


def read_env(path):
    if path.is_symlink():
        raise ValueError('Environment must be a regular file')
    result = {}
    for line in path.read_text().splitlines():
        key, sep, value = line.partition('=')
        if sep and key.startswith('NAS_'):
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            result[key] = value
    return result


def download(url, target, limit):
    request = urllib.request.Request(url, headers={'Accept': 'application/vnd.github+json', 'User-Agent': 'nas-search-upgrader'})
    with urllib.request.urlopen(request, timeout=60) as response:
        total = 0
        while True:
            chunk = response.read(65536)
            if not chunk:
                break
            total += len(chunk)
            if total > limit:
                raise ValueError('Download too large')
            target.write(chunk)


def api(path):
    import io
    output = io.BytesIO()
    download(API + path, output, 4 * 1024 * 1024)
    return json.loads(output.getvalue())


def verified_run(run, jobs, sha):
    return (run.get('head_sha') == sha and run.get('head_branch') == 'main'
            and run.get('event') == 'push' and run.get('status') == 'completed'
            and run.get('conclusion') == 'success' and run.get('name') == 'Verify and test'
            and all(any(job.get('name') == name and job.get('conclusion') == 'success'
                        for job in jobs) for name in ('verify', 'publish')))


def resolve_verified_main():
    sha = api('/commits/main')['sha']
    if not re.fullmatch('[0-9a-f]{40}', sha):
        raise ValueError('Invalid commit')
    runs = api('/actions/workflows/verify.yml/runs?branch=main&event=push&head_sha=' + sha + '&per_page=30')['workflow_runs']
    for run in runs:
        if run.get('head_sha') == sha:
            jobs = api('/actions/runs/' + str(int(run['id'])) + '/jobs?per_page=100')['jobs']
            if verified_run(run, jobs, sha):
                return sha
    raise ValueError('Main has not passed verification and publication')


def extract_archive(archive, destination):
    destination.mkdir()
    with tarfile.open(archive, 'r:gz') as tar:
        total = 0
        members = []
        for member in tar:
            path = PurePosixPath(member.name)
            if path.is_absolute() or '..' in path.parts or not (member.isfile() or member.isdir()):
                raise ValueError('Unsafe archive entry')
            total += member.size
            if total > MAX_EXPANDED or len(members) >= 20000:
                raise ValueError('Archive too large')
            members.append(member)
        for member in members:
            target = destination.joinpath(*PurePosixPath(member.name).parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as source, target.open('xb') as output:
                    shutil.copyfileobj(source, output)
                target.chmod(0o700 if member.mode & 0o111 else 0o600)
    roots = list(destination.iterdir())
    if len(roots) != 1 or not roots[0].is_dir():
        raise ValueError('Expected complete project archive')
    return roots[0]


def install_commit(install_dir, env, sha):
    with tempfile.TemporaryDirectory(prefix='nas-search-upgrade-') as temp:
        temp = Path(temp)
        archive = temp / 'source.tar.gz'
        with archive.open('wb') as output:
            download('https://codeload.github.com/mattyv/nas-search-mcp/tar.gz/' + sha, output, MAX_ARCHIVE)
        root = extract_archive(archive, temp / 'source')
        args = ['/bin/sh', str(root / 'scripts/install-nas.sh'), '--image', 'ghcr.io/mattyv/nas-search-mcp:' + sha, '--install-dir', str(install_dir)]
        for option, key in [('matthew-path', 'NAS_MATTHEW_PATH'), ('photos-path', 'NAS_PHOTOS_PATH'), ('code-path', 'NAS_CODE_PATH'), ('data-path', 'NAS_INDEX_PATH'), ('uid', 'NAS_UID'), ('gid', 'NAS_GID'), ('bind-ip', 'NAS_BIND_IP')]:
            args.extend(['--' + option, env[key]])
        # Installer output can contain deployment details; expose only a fixed status.
        with tempfile.TemporaryFile() as log:
            subprocess.run(args, stdout=log, stderr=log, check=True)


def write_status(queue, state, commit='', failure_stage=''):
    status = {'state': state, 'commit': commit, 'updated_at': int(time.time())}
    status['message'] = {'running': 'Upgrading to verified main.', 'succeeded': 'Upgrade passed health and authenticated MCP checks.', 'failed': 'Upgrade failed. The installer preserves credentials and index and attempts rollback.'}[state]
    if failure_stage:
        status['failure_stage'] = failure_stage
    fd, name = tempfile.mkstemp(prefix='.status-', dir=queue)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(status, output)
        os.chmod(name, 0o644)
        os.replace(name, queue / 'status.json')
    finally:
        if os.path.exists(name):
            os.unlink(name)


def process_once(install_dir):
    env = read_env(install_dir / '.env')
    queue = install_dir / 'upgrades'
    if queue.is_symlink():
        raise ValueError('Queue must not be a symlink')
    queue.mkdir(mode=0o1733, exist_ok=True)
    queue_info = queue.stat()
    if os.geteuid() == 0:
        if queue_info.st_uid != 0:
            raise ValueError('Queue must belong to root')
        queue.chmod(0o1733)
    request = queue / 'request.json'
    try:
        fd = os.open(request, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return False
    sha = ''
    stage = 'request_validation'
    try:
        with os.fdopen(fd) as source:
            if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
                raise ValueError('Invalid request file')
            body = source.read(4097)
        write_status(queue, 'running')
        if len(body) > 4096 or json.loads(body) != {'action': 'upgrade_main'}:
            raise ValueError('Invalid request')
        stage = 'main_verification'
        sha = resolve_verified_main()
        write_status(queue, 'running', sha)
        stage = 'download_and_install'
        install_commit(install_dir, env, sha)
        write_status(queue, 'succeeded', sha)
    except Exception:
        write_status(queue, 'failed', sha, stage)
    finally:
        request.unlink(missing_ok=True)
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--install-dir', type=Path, default=Path('/volume1/@nas-search-upgrader'))
    parser.add_argument('--once', action='store_true')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run as root on the NAS')
    os.umask(0o077)
    if not args.install_dir.is_absolute() or '..' in args.install_dir.parts:
        parser.error('Installation directory must be absolute without traversal')
    for directory in (args.install_dir, *args.install_dir.parents):
        install_info = directory.lstat()
        if not stat.S_ISDIR(install_info.st_mode) or install_info.st_uid != 0 or install_info.st_mode & 0o022:
            parser.error('Installation directory and ancestors must belong to root and permit writes only by root')
    # Root-owned installation directory keeps the lock outside the app's writable data.
    with (args.install_dir / '.upgrade-host.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        while True:
            process_once(args.install_dir)
            if args.once:
                return
            time.sleep(5)


if __name__ == '__main__':
    main()
