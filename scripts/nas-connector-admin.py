#!/usr/bin/env python3
"""Fixed, temporary root operations for the NAS connector deployment."""
import os
from pathlib import Path
import re
import secrets
import signal
import stat
import subprocess
import sys
import time
import urllib.request
from urllib.error import HTTPError


PROJECT = Path('/volume1/nas-search-connector')
OLD = Path('/volume1/docker/nas-search')
DOCKER = '/usr/local/bin/docker'
RULE = Path('/etc/sudoers.d/nas-search-connector')
SUDO_RULE_TEXT = '\n'.join(
    'matt ALL=(root) NOPASSWD: /usr/bin/python3 /volume1/nas-search-connector/scripts/nas-connector-admin.py ' + operation
    for operation in ('deploy', 'cleanup'))


def valid_candidate_env(content):
    required = {'NAS_TOKEN', 'NAS_ALLOWED_FOLDERS', 'NAS_OAUTH_ISSUER',
                'NAS_OAUTH_CLIENT_ID', 'NAS_OAUTH_PASSWORD_HASH'}
    values = {}
    for line in content.splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if separator and key in required:
            if key in values:
                return False
            values[key] = value
    return (len(values.get('NAS_TOKEN', '')) >= 24
            and values.get('NAS_ALLOWED_FOLDERS') == 'Matthew,Code,Photos'
            and values.get('NAS_OAUTH_ISSUER') == 'https://mattssynology.tail0fe6aa.ts.net'
            and values.get('NAS_OAUTH_CLIENT_ID') == 'nas-claude'
            and re.fullmatch(r"'\$2[aby]\$12\$[A-Za-z0-9./]{53}'", values.get('NAS_OAUTH_PASSWORD_HASH', '')) is not None)


def remove_rule(path=RULE):
    try:
        info = path.lstat()
    except FileNotFoundError:
        return True
    if not stat.S_ISREG(info.st_mode):
        return False
    if path.read_text().strip() != SUDO_RULE_TEXT:
        return False
    path.unlink()
    return True


def startup_category(logs):
    for marker in ('invalid folder access configuration', 'OAuth configuration invalid',
                   'pipeline changed', 'permission denied', 'unable to open database',
                   'NAS_TOKEN must contain', 'file root must be a directory'):
        if marker in logs:
            return marker
    if 'NAS search listening' in logs:
        return 'started'
    return 'unclassified startup failure'


def command(args, timeout=45):
    process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               start_new_session=True)
    try:
        output, errors = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.communicate()
        raise RuntimeError('Docker command timed out; its process group was stopped.') from None
    if process.returncode:
        raise RuntimeError('Docker command failed (exit %d).' % process.returncode)
    return (output + errors).decode(errors='replace')


def compose(project):
    return [DOCKER, 'compose', '--project-name', 'nas-search',
            '--project-directory', str(project), '-f', str(project / 'compose.yaml')]


def logs_category(container):
    try:
        logs = command([DOCKER, 'logs', '--tail', '80', container], timeout=10)
    except RuntimeError:
        return 'logs unavailable'
    return startup_category(logs)


def diagnose():
    diagnostic = 'nas-oauth-diagnostic-' + secrets.token_hex(6)
    args = compose(PROJECT) + ['run', '-d', '-T', '--interactive=false', '--no-deps',
            '--name', diagnostic, '-e', 'NAS_DATA=/tmp',
            '-e', 'NAS_OAUTH_DB=/tmp/oauth-diagnostic.sqlite3',
            '--entrypoint', '/app/nas-search', 'nas-search']
    try:
        command(args, timeout=30)
        for _ in range(10):
            category = logs_category(diagnostic)
            if category not in ('unclassified startup failure', 'logs unavailable'):
                print('Isolated startup: ' + category)
                return
            time.sleep(2)
        print('Isolated startup: no known startup message within 20 seconds.')
    finally:
        try:
            label = command([DOCKER, 'inspect', '--format',
                             '{{ index .Config.Labels "com.docker.compose.project" }}', diagnostic], timeout=10).strip()
            if label == 'nas-search':
                command([DOCKER, 'rm', '-f', diagnostic], timeout=15)
        except RuntimeError:
            pass


def healthy(url, public=False):
    try:
        with urllib.request.urlopen(url, timeout=3) as response:
            return not public and response.status == 200
    except HTTPError as error:
        return public and error.code == 401 and 'resource_metadata=' in error.headers.get('WWW-Authenticate', '')
    except OSError:
        return False


def wait_healthy(public, timeout, private_url='http://127.0.0.1:8080/healthz'):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if healthy(private_url) and (
                not public or healthy('http://127.0.0.1:8082/mcp', public=True)):
            return True
        time.sleep(2)
    return False


def deploy():
    env = PROJECT / '.env'
    info = env.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        raise RuntimeError('Candidate environment must be a root-owned mode-600 regular file.')
    if not valid_candidate_env(env.read_text()):
        raise RuntimeError('Candidate OAuth or access configuration is incomplete.')
    try:
        command(compose(PROJECT) + ['up', '-d', '--no-build', '--no-deps', 'nas-search'], timeout=90)
        if not wait_healthy(True, 90):
            raise RuntimeError('New service did not pass private health and anonymous OAuth challenge checks.')
    except RuntimeError as error:
        category = logs_category('nas-search-nas-search-1')
        print('New service: ' + category, file=sys.stderr)
        command(compose(OLD) + ['up', '-d', '--no-build', '--no-deps', 'nas-search'], timeout=90)
        if not wait_healthy(False, 60, 'http://192.168.1.4:8080/healthz'):
            raise RuntimeError('Rollback did not restore LAN health; inspect the project immediately.') from error
        raise RuntimeError('New service failed; previous LAN service restored.') from error
    print('New service passed local health and OAuth rejection checks. Funnel state unchanged.')


def main():
    if os.geteuid() != 0 or len(sys.argv) != 2 or sys.argv[1] not in ('diagnose', 'deploy', 'cleanup'):
        raise SystemExit('Run as root with diagnose, deploy, or cleanup.')
    info = PROJECT.lstat()
    if info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o700:
        raise SystemExit('Secure project directory is not root-owned mode 700.')
    script = Path(__file__).resolve()
    info = script.lstat()
    if script != PROJECT / 'scripts/nas-connector-admin.py' or info.st_uid != 0 or not stat.S_ISREG(info.st_mode):
        raise SystemExit('Admin script must be the root-owned secure copy.')
    if sys.argv[1] == 'diagnose':
        diagnose()
    elif sys.argv[1] == 'deploy':
        deploy()
    elif not remove_rule():
        raise SystemExit('Temporary sudo rule differs; inspect it before removal.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1) from None
