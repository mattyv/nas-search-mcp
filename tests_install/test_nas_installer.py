import os
from pathlib import Path
import subprocess

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/install-nas.sh'


def setup(tmp_path):
    binaries = tmp_path / 'bin'
    binaries.mkdir()
    log = tmp_path / 'docker.log'
    (binaries / 'id').write_text('#!/bin/sh\ncase "$1" in -u) echo 0;; -g) echo 100;; esac\n')
    (binaries / 'docker').write_text('''#!/bin/sh
printf '%s\\n' "$*" >> "$DOCKER_LOG"
case "$1" in
info) echo 'Server ready';;
inspect) [ -n "${ACTIVE_INSTALL:-}" ] || exit 1; printf '%s\\n' "$ACTIVE_INSTALL";;
pull) [ "${FAIL_PULL:-0}" = 0 ];;
run) [ "${FAIL_LIMITS:-0}" = 0 ];;
compose) case "$*" in *"exec -T nas-search"*) [ "${FAIL_HEALTH:-0}" = 0 ];; esac;;
esac
''')
    (binaries / 'chown').write_text('#!/bin/sh\nprintf \"%s\\n\" \"$*\" >> \"$CHOWN_LOG\"\n')
    (binaries / 'sleep').write_text('#!/bin/sh\nexit 0\n')
    for binary in binaries.iterdir():
        binary.chmod(0o755)
    matt = tmp_path / 'Matthew'
    photos = tmp_path / 'Photos'
    matt.mkdir()
    photos.mkdir()
    code = tmp_path / 'Code'
    code.mkdir()
    env = dict(os.environ, PATH=str(binaries) + ':' + os.environ['PATH'], DOCKER_LOG=str(log), CHOWN_LOG=str(tmp_path / 'chown.log'), SUDO_UID='1026', SUDO_GID='100')
    args = ['sh', str(SCRIPT), '--image', 'ghcr.io/mattyv/nas-search-mcp:abc123', '--install-dir', str(tmp_path / 'install'), '--matthew-path', str(matt), '--photos-path', str(photos), '--code-path', str(code), '--data-path', str(tmp_path / 'index'), '--bind-ip', '192.168.1.4']
    return args, env, log


def test_install_order_and_private_state(tmp_path):
    args, env, log = setup(tmp_path)
    result = subprocess.run(args, env=env, text=True, capture_output=True)
    assert result.returncode == 0, result.stderr
    state = tmp_path / 'install'
    contents = (state / '.env').read_text()
    assert 'NAS_UID=1026' in contents
    assert 'NAS_TOKEN=' in contents
    assert 'NAS_ALLOWED_FOLDERS=Matthew,Code,Photos' in contents
    assert (state / '.env').stat().st_mode & 0o077 == 0
    assert f'0:0 {state / ".env"}' in (tmp_path / 'chown.log').read_text()
    assert 'NAS_TOKEN=' not in result.stdout
    calls = log.read_text()
    assert calls.index('pull ') < calls.index('run ') < calls.index('up -d --no-build') < calls.index('exec -T nas-search')
    compose = (state / 'compose.yaml').read_text()
    assert 'build:' not in compose and 'cpuset:' in compose
    assert 'cpus:' not in compose and 'pids_limit:' not in compose
    assert (state / 'current-image').read_text().strip().endswith(':abc123')


def test_dry_run_and_reject_latest(tmp_path):
    args, env, log = setup(tmp_path)
    result = subprocess.run(args + ['--dry-run'], env=env, capture_output=True, text=True)
    assert result.returncode == 0
    assert not (tmp_path / 'install').exists()
    assert 'pull ' not in log.read_text()
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:latest'
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0


def test_failed_limits_do_not_create_state(tmp_path):
    args, env, log = setup(tmp_path)
    env['FAIL_LIMITS'] = '1'
    result = subprocess.run(args, env=env, capture_output=True)
    assert result.returncode != 0
    assert not (tmp_path / 'install').exists()
    assert 'up -d' not in log.read_text()


def test_existing_state_and_failed_upgrade_rollback(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    state = tmp_path / 'install'
    original = (state / '.env').read_text()
    token = next(line for line in original.splitlines() if line.startswith('NAS_TOKEN='))
    (state / '.env').write_text('\n'.join(line for line in original.splitlines() if not line.startswith('NAS_ALLOWED_FOLDERS=')) + '\nNAS_DENIED_FOLDERS=Photos/private\n')
    (tmp_path / 'index' / 'keep.sqlite3').write_bytes(b'untouched')
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:def456'
    env['FAIL_HEALTH'] = '1'
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    updated = (state / '.env').read_text()
    assert token in updated
    assert 'NAS_ALLOWED_FOLDERS=Matthew,Code,Photos' in updated
    assert 'NAS_DENIED_FOLDERS=Photos/private' in updated
    assert (state / 'current-image').read_text().strip().endswith(':abc123')
    assert (tmp_path / 'index' / 'keep.sqlite3').read_bytes() == b'untouched'
    assert 'Rollback' in log.read_text() or log.read_text().count('up -d --no-build') >= 3


def test_spaces_and_persistent_compose_image(tmp_path):
    args, env, log = setup(tmp_path)
    source = tmp_path / 'Matthew' / 'Berklee' / 'Week 10'
    source.mkdir(parents=True)
    args[args.index('--matthew-path') + 1] = str(source)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    compose = (tmp_path / 'install' / 'compose.yaml').read_text()
    assert 'image: ghcr.io/mattyv/nas-search-mcp:abc123' in compose
    assert 'NAS_INSTALL_IMAGE' not in compose
    assert f"NAS_MATTHEW_PATH='{source}'" in (tmp_path / 'install' / '.env').read_text()


def test_failed_upgrade_restores_compose(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    state = tmp_path / 'install'
    original = (state / 'compose.yaml').read_bytes()
    assert b'image: ghcr.io/mattyv/nas-search-mcp:abc123' in original
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:def456'
    env['FAIL_HEALTH'] = '1'
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert (state / 'compose.yaml').read_bytes() == original


def test_canonical_source_containment(tmp_path):
    args, env, log = setup(tmp_path)
    (tmp_path / 'outside').symlink_to(tmp_path / 'Matthew', target_is_directory=True)
    args[args.index('--data-path') + 1] = str(tmp_path / 'outside' / 'index')
    result = subprocess.run(args, env=env, capture_output=True)
    assert result.returncode != 0
    assert not (tmp_path / 'install').exists()


def test_missing_existing_index_rejected_without_state_changes(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    state = tmp_path / 'install'
    original = (state / '.env').read_bytes()
    (tmp_path / 'index').rmdir()
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert (state / '.env').read_bytes() == original
    assert not (tmp_path / 'index').exists()


def test_existing_env_permissions_and_symlink_refusal(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_bytes()
    secret.chmod(0o644)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    assert secret.stat().st_mode & 0o077 == 0
    assert secret.read_bytes() == original
    moved = tmp_path / 'elsewhere.env'
    secret.rename(moved)
    secret.symlink_to(moved)
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert moved.read_bytes() == original


def test_code_source_mount_and_env(tmp_path):
    args, env, log = setup(tmp_path)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert f"NAS_CODE_PATH='{tmp_path / 'Code'}'" in (tmp_path / 'install' / '.env').read_text()
    assert f"{tmp_path / 'Code'}:/check-code:ro" in log.read_text()
    (tmp_path / 'Code').rmdir()
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0


def test_existing_access_scope_is_preserved(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    secret.write_text(secret.read_text().replace('NAS_ALLOWED_FOLDERS=Matthew,Code,Photos', 'NAS_ALLOWED_FOLDERS=Matthew') + 'NAS_DENIED_FOLDERS=Matthew/private\n')
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:def456'
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    contents = secret.read_text()
    assert 'NAS_ALLOWED_FOLDERS=Matthew\n' in contents
    assert 'NAS_DENIED_FOLDERS=Matthew/private\n' in contents


def test_synology_protected_upgrade_path(tmp_path):
    args, env, log = setup(tmp_path)
    protected = tmp_path / '@nas-search-upgrader'
    args[args.index('--install-dir') + 1] = str(protected)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert (protected / 'current-image').read_text().strip().endswith(':abc123')
    assert 'up -d --no-build' in log.read_text()
    # Every subsequent MCP-triggered upgrade uses this same protected path.
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:def456'
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert (protected / 'current-image').read_text().strip().endswith(':def456')


def test_default_install_preserves_active_deployment(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    state = tmp_path / 'install'
    original = (state / '.env').read_bytes()
    i = args.index('--install-dir')
    del args[i:i + 2]
    env['ACTIVE_INSTALL'] = str(state)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert (state / '.env').read_bytes() == original
    assert f'--project-directory {state}' in log.read_text()


def test_oauth_check_rejects_unavailable_login(monkeypatch):
    import json
    import urllib.request
    import pytest
    check = SCRIPT.read_text().split("check='", 1)[1].split("'\nready=false", 1)[0]
    class Reply:
        status = 200
        def read(self):
            return json.dumps({'result': {}}).encode()
    calls = []
    def request(url, **kwargs):
        address = url if isinstance(url, str) else url.full_url
        calls.append(address)
        if ':8082/' in address:
            raise OSError('OAuth unavailable')
        return Reply()
    monkeypatch.setenv('NAS_TOKEN', 'private')
    monkeypatch.setenv('NAS_OAUTH_ISSUER', 'https://nas.example')
    monkeypatch.setattr(urllib.request, 'urlopen', request)
    with pytest.raises(OSError, match='OAuth unavailable'):
        exec(check, {})
    assert any(':8082/' in address for address in calls)


def test_oauth_check_validates_issuer_and_authentication(monkeypatch):
    import io
    import json
    import urllib.request
    import urllib.error
    import pytest
    check = SCRIPT.read_text().split("check='", 1)[1].split("'\nready=false", 1)[0]
    class Reply:
        status = 200
        def __init__(self, body):
            self.body = body
        def read(self):
            return json.dumps(self.body).encode()
    monkeypatch.setenv('NAS_TOKEN', 'private')
    monkeypatch.setenv('NAS_OAUTH_ISSUER', 'https://nas.example')
    scenario = {'issuer': 'https://nas.example', 'challenge': 'Bearer resource_metadata="https://nas.example/.well-known/oauth-protected-resource"', 'status': 401}
    def request(url, **kwargs):
        address = url if isinstance(url, str) else url.full_url
        if address.endswith('/.well-known/oauth-authorization-server'):
            return Reply({'issuer': scenario['issuer']})
        if ':8082/mcp' in address:
            if scenario['status'] == 200:
                return Reply({})
            raise urllib.error.HTTPError(address, scenario['status'], 'Unauthorized', {'WWW-Authenticate': scenario['challenge']}, io.BytesIO())
        return Reply({'result': {}})
    monkeypatch.setattr(urllib.request, 'urlopen', request)
    exec(check, {})
    for key, value in [('issuer', 'https://wrong.example'), ('challenge', 'Bearer'), ('status', 200), ('status', 403)]:
        original = scenario[key]
        scenario[key] = value
        with pytest.raises(AssertionError):
            exec(check, {})
        scenario[key] = original


def test_default_install_rejects_invalid_detected_path_before_mutation(tmp_path):
    args, env, log = setup(tmp_path)
    i = args.index('--install-dir')
    del args[i:i + 2]
    env['ACTIVE_INSTALL'] = '/volume1/../private'
    result = subprocess.run(args, env=env, capture_output=True)
    assert result.returncode != 0
    assert 'pull ' not in log.read_text()
    assert not (tmp_path / 'install').exists()
