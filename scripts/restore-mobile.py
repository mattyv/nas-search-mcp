#!/usr/bin/env python3
"""Restore the original mobile configuration without changing network settings."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request


def private_file(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        raise ValueError('Unsafe private file')


def secure_directory(path):
    if not path.is_absolute() or '..' in path.parts:
        raise ValueError('Absolute directory required')
    for item in reversed((path,) + tuple(path.parents)):
        info = item.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('Unsafe directory')
    if stat.S_IMODE(path.stat().st_mode) != 0o700:
        raise ValueError('Private directory required')


def validate_config(source, target, install):
    for key in ('NAS_TOKEN', 'NAS_INDEX_PATH', 'NAS_MATTHEW_PATH', 'NAS_CODE_PATH', 'NAS_PHOTOS_PATH'):
        if not source.get(key) or source[key] != target.get(key):
            raise ValueError('Deployment identity mismatch')
    issuer = urllib.parse.urlsplit(source.get('NAS_OAUTH_ISSUER', ''))
    if issuer.scheme != 'https' or not issuer.hostname or issuer.username or issuer.password or issuer.path not in ('', '/') or issuer.query or issuer.fragment or not source.get('NAS_OAUTH_PASSWORD_HASH'):
        raise ValueError('OAuth configuration incomplete')
    if target.get('NAS_UPGRADE_DIR') != '/upgrades' or target.get('NAS_UPGRADE_PATH') != str(install / 'upgrades'):
        raise ValueError('Upgrade configuration mismatch')


def merge_env(contents, target):
    lines = [line for line in contents.splitlines() if line.partition('=')[0] not in ('NAS_UPGRADE_DIR', 'NAS_UPGRADE_PATH')]
    for key in ('NAS_UPGRADE_DIR', 'NAS_UPGRADE_PATH'):
        value = target[key]
        if any(c in value for c in '\r\n\x00'):
            raise ValueError('Invalid setting')
        lines.append(key + '=' + value)
    return '\n'.join(lines) + '\n'


def atomic_write(path, contents):
    fd, name = tempfile.mkstemp(prefix='.mobile-', dir=str(path.parent))
    try:
        with os.fdopen(fd, 'w') as output:
            output.write(contents)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(name, 0o600)
        os.chown(name, 0, 0)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def install(installer, install_dir, image, env, log):
    args = ['/bin/sh', str(installer), '--image', image, '--install-dir', str(install_dir)]
    for option, key in [('matthew-path', 'NAS_MATTHEW_PATH'), ('photos-path', 'NAS_PHOTOS_PATH'), ('code-path', 'NAS_CODE_PATH'), ('data-path', 'NAS_INDEX_PATH'), ('uid', 'NAS_UID'), ('gid', 'NAS_GID'), ('bind-ip', 'NAS_BIND_IP')]:
        args.extend(['--' + option, env[key]])
    subprocess.run(args, stdout=log, stderr=log, check=True)


def check_idle(install_dir):
    queue = install_dir / 'upgrades'
    request = queue / 'request.json'
    if request.exists() or request.is_symlink():
        raise ValueError('Upgrade pending')
    status = queue / 'status.json'
    if status.exists() or status.is_symlink():
        info = status.lstat()
        if not stat.S_ISREG(info.st_mode) or json.loads(status.read_text()).get('state') == 'running':
            raise ValueError('Upgrade active')


def check_oauth(env):
    base = 'http://127.0.0.1:8082'
    with urllib.request.urlopen(base + '/.well-known/oauth-authorization-server', timeout=20) as response:
        if response.status != 200 or json.loads(response.read()).get('issuer') != env['NAS_OAUTH_ISSUER']:
            raise ValueError('Metadata unavailable')
    try:
        urllib.request.urlopen(urllib.request.Request(base + '/mcp', data=b'{}', headers={'Content-Type': 'application/json'}), timeout=20)
    except urllib.error.HTTPError as error:
        if error.code == 401 and 'resource_metadata=' in error.headers.get('WWW-Authenticate', ''):
            return
    raise ValueError('OAuth challenge unavailable')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-install-dir', type=Path, default=Path('/volume1/nas-search-connector'))
    parser.add_argument('--install-dir', type=Path, default=Path('/volume1/@nas-search-upgrader'))
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    stage = 'validation'
    old = None
    changed = False
    log = None
    try:
        if os.geteuid() != 0:
            raise ValueError('Root required')
        for directory in (args.source_install_dir, args.install_dir):
            secure_directory(directory)
            private_file(directory / '.env')
        image_path = args.install_dir / 'current-image'
        private_file(image_path)
        image = image_path.read_text().strip()
        if not re.fullmatch(r'ghcr\.io/mattyv/nas-search-mcp:[0-9a-f]{40}', image):
            raise ValueError('Pinned image required')
        script = Path(os.path.abspath(__file__))
        for ancestor in (script.parent,) + tuple(script.parent.parents):
            info = ancestor.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
                raise ValueError('Unsafe installer directory')
        installer = script.parent / 'install-nas.sh'
        # Refuse writable or linked installer inputs before executing privileged code.
        for path in (script, installer, installer.parent / 'upgrade-host.py'):
            info = path.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
                raise ValueError('Unsafe installer')
        spec = importlib.util.spec_from_file_location('restore_upgrade_host', installer.parent / 'upgrade-host.py')
        host = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(host)
        source = host.read_env(args.source_install_dir / '.env')
        target = host.read_env(args.install_dir / '.env')
        validate_config(source, target, args.install_dir)
        check_idle(args.install_dir)
        merged = merge_env((args.source_install_dir / '.env').read_text(), target)
        old = (args.install_dir / '.env').read_text()
        if args.dry_run:
            print('Mobile configuration is ready to restore. No changes made.')
            return 0
        stage = 'backup'
        backup = args.install_dir / '.env.before-mobile-restore'
        fd = os.open(str(backup), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as output:
            output.write(old)
        log_path = args.install_dir / 'restore-mobile.log'
        if log_path.exists() or log_path.is_symlink():
            private_file(log_path)
        fd = os.open(str(log_path), os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
        log = os.fdopen(fd, 'a')
        stage = 'installation'
        check_idle(args.install_dir)
        atomic_write(args.install_dir / '.env', merged)
        changed = True
        restored = dict(source, NAS_UPGRADE_DIR=target['NAS_UPGRADE_DIR'], NAS_UPGRADE_PATH=target['NAS_UPGRADE_PATH'])
        install(installer, args.install_dir, image, restored, log)
        stage = 'OAuth checks'
        check_oauth(restored)
        print('Mobile login configuration restored. OAuth metadata and authentication checks passed.')
        return 0
    except Exception:
        rollback = ''
        if changed:
            try:
                atomic_write(args.install_dir / '.env', old)
                install(installer, args.install_dir, image, target, log)
                rollback = ' Previous configuration restored.'
            except Exception:
                rollback = ' Recovery needs inspection of the private restore log.'
        print('Mobile restore failed during ' + stage + '.' + rollback)
        return 1
    finally:
        if log:
            log.close()


if __name__ == '__main__':
    raise SystemExit(main())
