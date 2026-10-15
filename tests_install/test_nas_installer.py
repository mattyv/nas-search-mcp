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
pull) [ "${FAIL_PULL:-0}" = 0 ];;
run) [ "${FAIL_LIMITS:-0}" = 0 ];;
compose) case "$*" in *"exec -T nas-search"*) [ "${FAIL_HEALTH:-0}" = 0 ];; esac;;
esac
''')
    (binaries / 'chown').write_text('#!/bin/sh\nprintf \"%s\\n\" \"$*\" >> \"$CHOWN_LOG\"\n')
    (binaries / 'sleep').write_text('#!/bin/sh\nexit 0\n')
    for binary in binaries.iterdir():
        binary.chmod(0o755)
    owner = tmp_path / 'Documents'
    photos = tmp_path / 'Photos'
    owner.mkdir()
    photos.mkdir()
    code = tmp_path / 'Code'
    code.mkdir()
    env = dict(os.environ, PATH=str(binaries) + ':' + os.environ['PATH'], DOCKER_LOG=str(log), CHOWN_LOG=str(tmp_path / 'chown.log'), SUDO_UID='1000', SUDO_GID='100')
    args = ['sh', str(SCRIPT), '--image', 'ghcr.io/mattyv/nas-search-mcp:abc123', '--install-dir', str(tmp_path / 'install'), '--documents-path', str(owner), '--photos-path', str(photos), '--code-path', str(code), '--data-path', str(tmp_path / 'index'), '--bind-ip', '192.0.2.10']
    return args, env, log


def test_install_order_and_private_state(tmp_path):
    args, env, log = setup(tmp_path)
    result = subprocess.run(args, env=env, text=True, capture_output=True)
    assert result.returncode == 0, result.stderr
    state = tmp_path / 'install'
    contents = (state / '.env').read_text()
    assert 'NAS_UID=1000' in contents
    assert 'NAS_TOKEN=' in contents
    assert 'NAS_ALLOWED_FOLDERS=Documents,Code,Photos' in contents
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
    assert 'NAS_ALLOWED_FOLDERS=Documents,Code,Photos' in updated
    assert 'NAS_DENIED_FOLDERS=Photos/private' in updated
    assert (state / 'current-image').read_text().strip().endswith(':abc123')
    assert (tmp_path / 'index' / 'keep.sqlite3').read_bytes() == b'untouched'
    assert 'Rollback' in log.read_text() or log.read_text().count('up -d --no-build') >= 3


def test_spaces_and_persistent_compose_image(tmp_path):
    args, env, log = setup(tmp_path)
    source = tmp_path / 'Documents' / 'Course' / 'Week 10'
    source.mkdir(parents=True)
    args[args.index('--documents-path') + 1] = str(source)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    compose = (tmp_path / 'install' / 'compose.yaml').read_text()
    assert 'image: ghcr.io/mattyv/nas-search-mcp:abc123' in compose
    assert 'NAS_INSTALL_IMAGE' not in compose
    assert f"NAS_DOCUMENTS_PATH='{source}'" in (tmp_path / 'install' / '.env').read_text()


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
    (tmp_path / 'outside').symlink_to(tmp_path / 'Documents', target_is_directory=True)
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
    secret.write_text(secret.read_text().replace('NAS_ALLOWED_FOLDERS=Documents,Code,Photos', 'NAS_ALLOWED_FOLDERS=Documents') + 'NAS_DENIED_FOLDERS=Documents/private\n')
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:def456'
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    contents = secret.read_text()
    assert 'NAS_ALLOWED_FOLDERS=Documents\n' in contents
    assert 'NAS_DENIED_FOLDERS=Documents/private\n' in contents


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
