import os
from pathlib import Path
import subprocess

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/photo-pilot.sh'
TOKEN = 'secret-photo-pilot-token-12345678'


def setup(tmp_path):
    binaries = tmp_path / 'bin'
    binaries.mkdir()
    source = tmp_path / 'photos'
    source.mkdir()
    install = tmp_path / 'install'
    install.mkdir()
    secret = install / '.env'
    secret.write_text(f'NAS_TOKEN={TOKEN}\nNAS_UID=1000\nNAS_GID=100\nNAS_CPUSET=0\nNAS_DENIED_FOLDERS=Photos/private\n')
    secret.chmod(0o600)
    (install / 'compose.yaml').write_text('original')
    log = tmp_path / 'docker.log'
    (binaries / 'id').write_text('#!/bin/sh\necho 0\n')
    (binaries / 'cat').write_text('#!/bin/sh\nif [ \"$1\" = /proc/meminfo ]; then echo \"MemAvailable: ${AVAILABLE_KB:-1500000} kB\"; else /bin/cat \"$@\"; fi\n')
    (binaries / 'chown').write_text('#!/bin/sh\nexit 0\n')
    (binaries / 'readlink').write_text('''#!/bin/sh
if [ "$2" = "$PHOTO_SOURCE" ]; then echo "${RESOLVED_SOURCE:-/volume1/shared-photos/pilot}"; else /usr/bin/readlink "$@"; fi
''')
    (binaries / 'docker').write_text('''#!/bin/sh
printf '%s\\n' "$*" >> "$DOCKER_LOG"
case "$1" in
info) echo ready;;
exec) [ \"${FAIL_HEALTH:-0}\" = 0 ];;
ps) [ "${FAIL_PS:-0}" = 0 ] || exit 1; printf '%s' "${EXISTING_CONTAINER:-}";;
pull) [ "${FAIL_PULL:-0}" = 0 ];;
esac
''')
    for binary in binaries.iterdir():
        binary.chmod(0o755)
    env = dict(os.environ, PATH=str(binaries) + ':' + os.environ['PATH'], PHOTO_SOURCE=str(source), DOCKER_LOG=str(log))
    args = ['sh', str(SCRIPT), '--image', 'ghcr.io/mattyv/nas-search-mcp@sha256:' + 'a' * 64, '--photos-path', str(source), '--install-dir', str(install)]
    return args, env, log, install


def test_isolated_private_bounded_launch(tmp_path):
    args, env, log, install = setup(tmp_path)
    original = (install / '.env').read_bytes()
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    calls = log.read_text()
    assert '--name nas-search-photo-pilot' in calls
    for option in ['--memory 1g', '--memory-swap 1g', '--cpuset-cpus 0', '--read-only', '--cap-drop ALL', '--security-opt no-new-privileges:true', '--tmpfs /tmp:size=64m,mode=1777', '192.0.2.10:8081:8080', '/volume1/shared-photos/pilot:/files/Photos:ro']:
        assert option in calls
    assert '--restart' not in calls
    assert 'exec nas-search-photo-pilot python3' in calls
    assert TOKEN not in calls + result.stdout + result.stderr
    pilot_env = install / '.env-photo-pilot'
    assert 'NAS_PHOTOS=true' in pilot_env.read_text()
    assert 'NAS_ALLOWED_FOLDERS=Photos' in pilot_env.read_text()
    # Without an all-interfaces listener, the published port refuses every connection.
    assert 'NAS_LISTEN=:8080' in pilot_env.read_text()
    assert 'NAS_DENIED_FOLDERS=Photos/private' in pilot_env.read_text()
    assert pilot_env.stat().st_mode & 0o077 == 0
    assert (install / '.env').read_bytes() == original
    assert (install / 'compose.yaml').read_text() == 'original'
    assert (install / 'data-photo-pilot').is_dir()


def test_dry_run_creates_nothing(tmp_path):
    args, env, log, install = setup(tmp_path)
    result = subprocess.run(args + ['--dry-run'], env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert not (install / 'data-photo-pilot').exists()
    assert not (install / '.env-photo-pilot').exists()
    assert 'pull ' not in log.read_text()


def test_unsafe_or_unavailable_launch_refused(tmp_path):
    args, env, log, install = setup(tmp_path)
    for key, value in [('RESOLVED_SOURCE', '/etc'), ('FAIL_PS', '1'), ('EXISTING_CONTAINER', 'nas-search-photo-pilot\n'), ('FAIL_PULL', '1'), ('AVAILABLE_KB', '1200000')]:
        result = subprocess.run(args, env=dict(env, **{key: value}), capture_output=True)
        assert result.returncode != 0, key
        assert not (install / 'data-photo-pilot').exists()
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:latest'
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0


def test_symlink_private_state_refused(tmp_path):
    args, env, log, install = setup(tmp_path)
    outside = tmp_path / 'outside'
    outside.mkdir()
    (install / 'data-photo-pilot').symlink_to(outside, target_is_directory=True)
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert list(outside.iterdir()) == []


def test_private_credentials_and_input_validation(tmp_path):
    args, env, log, install = setup(tmp_path)
    secret = install / '.env'
    secret.chmod(0o644)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode != 0
    assert 'mode 600 or 400' in result.stderr
    secret.chmod(0o600)
    result = subprocess.run(args + ['--bind-ip', '999.1.1.1'], env=env, capture_output=True)
    assert result.returncode != 0
    original = secret.read_text()
    secret.write_text(original.replace('NAS_CPUSET=0', 'NAS_CPUSET=0;echo BAD'))
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    secret.write_text(original)
    moved = install / 'elsewhere.env'
    secret.rename(moved)
    secret.symlink_to(moved)
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert not (install / 'data-photo-pilot').exists()


def test_kernel_probe_failure_creates_no_pilot_state(tmp_path):
    args, env, log, install = setup(tmp_path)
    docker = tmp_path / 'bin' / 'docker'
    docker.write_text(docker.read_text().replace('pull) [', 'run) exit 1;;\npull) ['))
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode != 0
    assert 'probe failed' in result.stderr
    assert not (install / 'data-photo-pilot').exists()
    assert not (install / '.env-photo-pilot').exists()


def test_full_commit_tag_supported(tmp_path):
    args, env, log, install = setup(tmp_path)
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:' + 'e' * 40
    result = subprocess.run(args + ['--dry-run'], env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr


def test_unhealthy_pilot_does_not_report_ready(tmp_path):
    args, env, log, install = setup(tmp_path)
    (tmp_path / 'bin' / 'sleep').write_text('#!/bin/sh\nexit 0\n')
    (tmp_path / 'bin' / 'sleep').chmod(0o755)
    result = subprocess.run(args, env=dict(env, FAIL_HEALTH='1'), capture_output=True, text=True)
    assert result.returncode != 0
    assert 'authenticated status is unavailable' in result.stderr
    assert 'status passed' not in result.stdout
    assert (install / 'compose.yaml').read_text() == 'original'
