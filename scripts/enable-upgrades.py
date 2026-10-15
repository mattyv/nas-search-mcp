#!/usr/bin/env python3
"""Enable host upgrades once, from a reviewed checkout, as root on the NAS."""
import argparse
import importlib.util
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys


def validate_image(image):
    if not re.fullmatch(r'ghcr\.io/mattyv/nas-search-mcp:[0-9a-f]{40}', image):
        raise ValueError('Image must name the published repository and exact main commit')


def validate_trusted_ancestors(path):
    if not path.is_absolute() or '..' in path.parts:
        raise ValueError('Target must be an absolute path without traversal')
    for ancestor in (path.parent, *path.parent.parents):
        info = ancestor.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('Target ancestors must be root-owned directories writable only by root')


def create_target(target):
    validate_trusted_ancestors(target)
    target.mkdir(mode=0o700)  # Never replace or adopt an existing deployment.
    os.chown(target, 0, 0)
    target.chmod(0o700)


def load_runner():
    spec = importlib.util.spec_from_file_location('trusted_upgrade_host', Path(__file__).with_name('upgrade-host.py'))
    runner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(runner)
    return runner


def validate_preserved_target(target):
    validate_trusted_ancestors(target)
    info = target.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
        raise ValueError('Preserved target must be a root-owned directory writable only by root')
    for name in ('.env', 'compose.yaml', 'current-image', 'upgrade-host.py', 'enable-upgrades.log', 'upgrade-host.log'):
        path = target / name
        if name.endswith('.log') and not path.exists() and not path.is_symlink():
            continue
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('Preserved files must be root-owned regular files writable only by root')
    info = (target / 'upgrades').lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o1733:
        raise ValueError('Preserved queue must be a root-owned directory with mode 1733')


def enable(source, target, image, resume=False):
    validate_image(image)
    runner = load_runner()
    if resume:
        validate_preserved_target(target)
        env = runner.read_env(target / '.env')
        if env.get('NAS_UPGRADE_DIR') != '/upgrades' or env.get('NAS_UPGRADE_PATH') != str(target / 'upgrades'):
            raise ValueError('Preserved upgrade paths do not match the target')
    else:
        # Inspect snapshots before creating trusted state. Never follow credentials links.
        for name in ('.env', 'compose.yaml', 'current-image'):
            info = (source / name).lstat()
            if not stat.S_ISREG(info.st_mode):
                raise ValueError('Existing deployment snapshots must be regular files')
        env = runner.read_env(source / '.env')
    fields = [('matthew-path', 'NAS_MATTHEW_PATH'), ('photos-path', 'NAS_PHOTOS_PATH'), ('code-path', 'NAS_CODE_PATH'), ('data-path', 'NAS_INDEX_PATH'), ('uid', 'NAS_UID'), ('gid', 'NAS_GID'), ('bind-ip', 'NAS_BIND_IP')]
    env.setdefault('NAS_CODE_PATH', '/volume1/Matthew/Code')
    env.setdefault('NAS_BIND_IP', '127.0.0.1')
    for _, key in fields:
        if not env.get(key):
            raise ValueError('Deployment configuration is incomplete')
    worker = target / 'upgrade-host.py'
    if not resume:
        create_target(target)
        for name in ('.env', 'compose.yaml', 'current-image'):
            shutil.copyfile(source / name, target / name, follow_symlinks=False)
            (target / name).chmod(0o600)
            os.chown(target / name, 0, 0)
        original = (target / '.env').read_text()
        lines = [line for line in original.splitlines() if line.partition('=')[0] not in ('NAS_UPGRADE_DIR', 'NAS_UPGRADE_PATH')]
        lines.extend(['NAS_UPGRADE_DIR=/upgrades', 'NAS_UPGRADE_PATH=' + str(target / 'upgrades')])
        (target / '.env').write_text('\n'.join(lines) + '\n')
        shutil.copyfile(Path(__file__).with_name('upgrade-host.py'), worker)
        os.chown(worker, 0, 0)
        worker.chmod(0o700)
        queue = target / 'upgrades'
        queue.mkdir(mode=0o1733)
        os.chown(queue, 0, 0)
        queue.chmod(0o1733)
    args = ['/bin/sh', str(Path(__file__).with_name('install-nas.sh')), '--install-dir', str(target), '--image', image]
    for option, key in fields:
        args.extend(['--' + option, env[key]])
    # Keep deployment output private, including failures. Leave the target for diagnosis.
    with (target / 'enable-upgrades.log').open('ab' if resume else 'wb') as log:
        os.chmod(target / 'enable-upgrades.log', 0o600)
        subprocess.run(args, stdout=log, stderr=log, check=True)
    with (target / 'upgrade-host.log').open('ab') as log, open(os.devnull, 'rb') as input_stream:
        os.chmod(target / 'upgrade-host.log', 0o600)
        subprocess.Popen([sys.executable, str(worker), '--install-dir', str(target)], stdin=input_stream, stdout=log, stderr=log, start_new_session=True, close_fds=True, cwd=target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--source-install-dir', type=Path, default=Path('/volume1/docker/nas-search'))
    parser.add_argument('--install-dir', type=Path, default=Path('/volume1/@nas-search-upgrader'))
    parser.add_argument('--resume', action='store_true', help='Resume a preserved root-owned setup without replacing its credentials or worker')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run with sudo on the NAS')
    os.umask(0o077)
    try:
        enable(args.source_install_dir, args.install_dir, args.image, resume=args.resume)
    except Exception:
        parser.exit(1, 'Upgrade setup failed. Deployment may have changed; inspect protected target logs and service health.\n')
    print('Upgrade service enabled. Configure a root boot task for its worker to survive NAS restarts.')


if __name__ == '__main__':
    main()
