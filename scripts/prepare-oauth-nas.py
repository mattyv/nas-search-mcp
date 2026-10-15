#!/usr/bin/env python3
"""Install the staged, already-compiled connector with a private password prompt."""
import getpass
import argparse
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from urllib.error import HTTPError


PROJECT = Path(__file__).resolve().parents[1]
DOCKER = '/usr/local/bin/docker'
SUDO_RULE = Path('/etc/sudoers.d/nas-search-connector')


def installer_sudo_rule(project, user):
    if not re.fullmatch(r'[a-z_][a-z0-9_-]*[$]?', user):
        raise ValueError('Administrator account is invalid.')
    return f'{user} ALL=(root) NOPASSWD: /usr/bin/python3 {project / "scripts/prepare-oauth-nas.py"}'


def prepared_env(original: str, password_hash: str, issuer: str) -> str:
    label = next((line.partition('=')[2] for line in original.splitlines()
                  if line.startswith('NAS_DOCUMENTS_LABEL=')), 'Documents')
    if not re.fullmatch(r'[A-Za-z0-9_-]+', label):
        raise ValueError('Document label is invalid.')
    updates = {
        'COMPOSE_PROJECT_NAME': 'nas-search',
        'NAS_DOCUMENTS_LABEL': label,
        'NAS_ALLOWED_FOLDERS': label + ',Code,Photos',
        'NAS_OAUTH_CLIENT_ID': 'nas-claude',
        'NAS_OAUTH_ISSUER': issuer,
        'NAS_OAUTH_PASSWORD_HASH': "'" + password_hash + "'",
    }
    result = []
    for line in original.splitlines():
        key, sep, _ = line.partition('=')
        if sep and key in updates:
            continue
        result.append(line)
    result.extend(key + '=' + value for key, value in updates.items())
    return '\n'.join(result) + '\n'


def run(command):
    # Docker commands here receive no password or token in arguments or logs.
    subprocess.run(command, check=True, stdout=subprocess.DEVNULL)


def verify_image_smoke(uid, gid):
    # Exercise vec0 startup before replacing the live container; no source folders are mounted.
    script = '''set -eu
mkdir /tmp/source /tmp/data
/app/nas-search >/tmp/smoke.log 2>&1 &
service_pid=$!
trap 'kill "$service_pid" 2>/dev/null || true; wait "$service_pid" 2>/dev/null || true' EXIT
attempt=0
while [ "$attempt" -lt 20 ]; do
  if python3 -c "import urllib.request; r=urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=1); assert r.status == 200" >/dev/null 2>&1; then
    echo 'vec0 startup OK'
    exit 0
  fi
  if ! kill -0 "$service_pid" 2>/dev/null; then exit 1; fi
  attempt=$((attempt + 1))
  sleep 1
done
exit 1
'''
    args = [DOCKER, 'run', '--rm', '-i', '--network', 'none', '--read-only',
            '--tmpfs', '/tmp:size=32m,mode=1777', '--user', str(uid) + ':' + str(gid),
            '-e', 'NAS_ROOT=/tmp/source', '-e', 'NAS_DATA=/tmp/data',
            '-e', 'NAS_VEC_EXTENSION=/opt/vec0.so', '-e', 'NAS_ALLOWED_FOLDERS=',
            '-e', 'NAS_SEMANTIC=false', '-e', 'NAS_SCAN_INTERVAL=3600',
            '-e', 'NAS_TOKEN=diagnostic-only-0123456789',
            '--entrypoint', '/bin/sh', 'nas-search-mcp:local', '-s']
    try:
        result = subprocess.run(args, input=script, text=True, capture_output=True, timeout=40)
    except subprocess.TimeoutExpired:
        raise RuntimeError('Built image startup check timed out; service unchanged.') from None
    if result.returncode or 'vec0 startup OK' not in result.stdout:
        raise RuntimeError('Built image cannot start with vec0; service unchanged.')


def verify_service():
    with urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=3) as response:
        if response.status != 200:
            raise RuntimeError('Private service health check failed.')
    try:
        with urllib.request.urlopen('http://127.0.0.1:8082/mcp', timeout=3):
            raise RuntimeError('Public MCP accepted an anonymous request.')
    except HTTPError as error:
        challenge = error.headers.get('WWW-Authenticate', '')
        if error.code != 401 or 'resource_metadata=' not in challenge:
            raise RuntimeError('Public MCP did not return an OAuth challenge.') from None


def remove_temporary_sudo_rule(path=SUDO_RULE, expected=''):
    if not path.exists():
        return True
    if not expected or path.read_text().strip() != expected:
        return False
    path.unlink()
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--existing-dir', type=Path, required=True)
    parser.add_argument('--issuer', required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise SystemExit('Run this setup as root from your NAS terminal.')
    project_info = PROJECT.lstat()
    if project_info.st_uid != 0 or stat.S_IMODE(project_info.st_mode) != 0o700:
        raise SystemExit('Secure project directory must be root-owned with mode 700.')
    source = args.existing_dir / '.env'
    if PROJECT.joinpath('.env').exists() or PROJECT.joinpath('.env').is_symlink():
        raise SystemExit('Candidate .env already exists; inspect it before retrying.')
    info = source.lstat()
    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
        raise SystemExit('Existing .env must be a regular file with mode 600.')
    original = source.read_text()
    if not any(line.startswith('NAS_TOKEN=') and len(line.partition('=')[2]) >= 24 for line in original.splitlines()):
        raise SystemExit('Existing private MCP token is missing or too short.')
    binary = PROJECT / 'build/nas-search'
    with binary.open('rb') as stream:
        header = stream.read(20)
    if len(header) < 20 or header[:5] != b'\x7fELF\x02' or header[18:20] != b'\x3e\x00':
        raise SystemExit('Staged service must be a Linux x86_64 binary.')
    settings = {}
    for line in original.splitlines():
        key, separator, value = line.partition('=')
        if separator and key.startswith('NAS_'):
            settings[key] = value.strip().strip("'\"")
    paths = [settings.get(key, '') for key in ('NAS_DOCUMENTS_PATH', 'NAS_CODE_PATH', 'NAS_PHOTOS_PATH')]
    for name in paths:
        path = Path(name)
        if not name or not path.is_dir() or str(path.resolve()) != name:
            raise SystemExit('A configured source folder no longer resolves to its saved physical path.')
    data_value = settings.get('NAS_INDEX_PATH', '')
    data = Path(data_value)
    if not data_value or not data.is_dir() or stat.S_IMODE(data.stat().st_mode) != 0o700:
        raise SystemExit('Private data directory must exist with mode 700.')
    try:
        first = getpass.getpass('New connector password (12–72 bytes): ')
        second = getpass.getpass('Repeat connector password: ')
    except (EOFError, KeyboardInterrupt):
        raise SystemExit('Password entry cancelled.')
    if first != second or not 12 <= len(first.encode()) <= 72:
        raise SystemExit('Passwords differ or fall outside 12–72 bytes.')
    run([DOCKER, 'build', '--pull=false', '--network=none', '--memory=256m', '--cpuset-cpus=0', '-t', 'nas-search-mcp:local', '-f', str(PROJECT / 'Dockerfile.oauth'), str(PROJECT)])
    uid, gid = settings.get('NAS_UID', ''), settings.get('NAS_GID', '')
    if not uid.isdigit() or not gid.isdigit():
        raise SystemExit('Saved container UID/GID must be numeric.')
    verify_image_smoke(uid, gid)
    hashed = subprocess.run([DOCKER, 'run', '--rm', '-i', '--network=none', '--entrypoint', '/app/nas-search', 'nas-search-mcp:local', 'hash-password'], input=first.encode(), capture_output=True, check=True).stdout.decode()
    first = second = ''
    if not re.fullmatch(r'\$2[aby]\$12\$[A-Za-z0-9./]{53}', hashed):
        raise SystemExit('Password hashing failed; service unchanged.')
    issuer = urllib.parse.urlsplit(args.issuer)
    if issuer.scheme != 'https' or not issuer.hostname or issuer.username or issuer.password or issuer.query or issuer.fragment:
        raise SystemExit('Issuer must be an HTTPS URL without credentials, query, or fragment.')
    content = prepared_env(original, hashed, args.issuer)
    fd, temp = tempfile.mkstemp(prefix='.env-', dir=PROJECT)
    try:
        os.fchmod(fd, 0o600)
        os.fchown(fd, 0, 0)
        with os.fdopen(fd, 'w') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, PROJECT / '.env')
    finally:
        if os.path.exists(temp):
            os.unlink(temp)
    # Recreate only this project's service. Roll back its old image if startup fails.
    new = [DOCKER, 'compose', '--project-name', 'nas-search', '--project-directory', str(PROJECT), '-f', str(PROJECT / 'compose.yaml')]
    old = [DOCKER, 'compose', '--project-name', 'nas-search', '--project-directory', str(args.existing_dir), '-f', str(args.existing_dir / 'compose.yaml')]
    try:
        run(new + ['up', '-d', '--no-build', '--no-deps', 'nas-search'])
        for attempt in range(10):
            try:
                verify_service()
                break
            except OSError:
                if attempt == 9:
                    raise
                time.sleep(2)
    except (subprocess.CalledProcessError, OSError, RuntimeError):
        run(old + ['up', '-d', '--no-build', '--no-deps', 'nas-search'])
        raise SystemExit('New service failed verification; restored existing project service. Check local diagnostics.')
    os.chown(source, 0, 0)
    os.chmod(source, 0o600)
    print('New connector service started on host loopback ports 8080 and 8082. Public Funnel remains off.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, subprocess.CalledProcessError) as error:
        raise SystemExit('Setup failed; check local Docker status before retrying.') from None
    finally:
        if os.geteuid() == 0:
            try:
                expected = installer_sudo_rule(PROJECT, os.environ.get('SUDO_USER', ''))
                if not remove_temporary_sudo_rule(expected=expected):
                    print('Temporary sudo rule differs from the installer grant; remove it manually.', file=sys.stderr)
            except OSError:
                print('Temporary sudo rule could not be removed; remove it manually.', file=sys.stderr)
