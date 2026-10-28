"""Render installer output with real Compose when available (required on CI)."""
import json
import os
from pathlib import Path
import shutil
import subprocess

import pytest

from test_nas_installer import setup, photos_config

DOCKER = shutil.which('docker')
needs_docker = pytest.mark.skipif(DOCKER is None, reason='Docker Compose is checked on CI')
COMPOSE = Path(__file__).resolve().parents[1] / 'compose.yaml'


@needs_docker
@pytest.mark.parametrize('bind,expected', [
    ('127.0.0.1', {'127.0.0.1'}),
    ('192.0.2.4', {'127.0.0.1', '192.0.2.4'}),
    ('0.0.0.0', {'0.0.0.0'}),
])
@pytest.mark.parametrize('quoted_space', [False, True])
def test_rendered_compose_matches_validated_paths_scope_and_binding(tmp_path, bind, expected, quoted_space):
    args, adapter_env, _ = setup(tmp_path)
    args[args.index('--bind-ip') + 1] = bind
    installed = subprocess.run(args, env=adapter_env, capture_output=True, text=True)
    assert installed.returncode == 0, installed.stderr
    state = tmp_path / 'install'
    data = tmp_path / ('index with space ' if quoted_space else 'index')
    data.mkdir(exist_ok=True)
    value = "'" + str(data) + "'" if quoted_space else str(data) + '   '
    private_env = state / '.env'
    lines = private_env.read_text().splitlines()
    lines = ['NAS_INDEX_PATH=' + value if line.startswith('NAS_INDEX_PATH=') else
             'NAS_ALLOWED_FOLDERS=' if line.startswith('NAS_ALLOWED_FOLDERS=') else line
             for line in lines]
    private_env.write_text('\n'.join(lines) + '\n')
    installed = subprocess.run(args, env=adapter_env, capture_output=True, text=True)
    assert installed.returncode == 0, installed.stderr
    clean_env = {key: value for key, value in os.environ.items() if not key.startswith('NAS_')}
    rendered = subprocess.run([DOCKER, 'compose', '--env-file', str(private_env),
                               '-f', str(state / 'compose.yaml'), 'config', '--format', 'json'],
                              env=clean_env, capture_output=True, text=True, check=True)
    service = json.loads(rendered.stdout)['services']['nas-search']
    mounts = {volume['target']: volume for volume in service['volumes']}
    assert mounts['/data']['source'] == str(data)
    assert not mounts['/data'].get('read_only', False)
    for target in ('/files/Documents', '/files/Code', '/files/Photos'):
        assert mounts[target]['read_only'] is True
    assert service['environment']['NAS_ALLOWED_FOLDERS'] == ''
    private_ports = {port.get('host_ip') for port in service['ports'] if port['target'] == 8080}
    assert private_ports == expected


@needs_docker
@pytest.mark.parametrize('indexed_upload', [False, True])
def test_optional_photos_broker_mount_is_read_only_in_real_compose(tmp_path, indexed_upload):
    args, adapter_env, _, private_env = photos_config(tmp_path)
    if indexed_upload:
        with private_env.open('a') as stream:
            stream.write('NAS_UPLOAD_ENABLED=true\nNAS_UPLOAD_INDEXED_DESTINATIONS=true\n')
    installed = subprocess.run(args, env=adapter_env, capture_output=True, text=True)
    assert installed.returncode == 0, installed.stderr
    clean_env = {key: value for key, value in os.environ.items() if not key.startswith('NAS_')}
    profile_args = ['--profile', 'indexed-upload'] if indexed_upload else []
    rendered = subprocess.run([DOCKER, 'compose', *profile_args, '--env-file', str(private_env),
                               '-f', str(tmp_path / 'install' / 'compose.yaml'), 'config', '--format', 'json'],
                              env=clean_env, capture_output=True, text=True, check=True)
    services = json.loads(rendered.stdout)['services']
    service = services['nas-search']
    mounts = {volume['target']: volume for volume in service['volumes']}
    assert mounts['/synology-photos']['source'] == str(tmp_path / 'protected-photos' / 'run')
    assert mounts['/synology-photos']['read_only'] is True
    assert service['environment']['NAS_SYNOLOGY_PHOTOS_SOCKET'] == '/synology-photos/photos.sock'
    for target in ('/files/Documents', '/files/Code', '/files/Photos'):
        assert mounts[target]['read_only'] is True
    writer = services.get('nas-upload-writer')
    if writer is not None:
        writer_mounts = {volume['target']: volume for volume in writer['volumes']}
        assert '/synology-photos' not in writer_mounts
        assert writer['network_mode'] == 'none'
        assert writer['read_only'] is True
        for target in ('/write/Documents', '/write/Code', '/write/Photos', '/ipc'):
            assert writer_mounts[target].get('read_only', False) is False


@pytest.mark.parametrize('service', ['nas-search', 'nas-upload-writer'])
def test_services_use_both_cores_with_low_cpu_shares(service):
    text = COMPOSE.read_text()
    block = text.split(f'\n  {service}:\n', 1)[1].split('\n  nas-', 1)[0]
    assert 'cpuset: "${NAS_CPUSET:-0-1}"' in block
    assert 'cpu_shares: ${NAS_CPU_SHARES:-512}' in block


def test_main_service_keeps_single_threaded_workers():
    text = COMPOSE.read_text()
    assert 'GOMAXPROCS: "1"' in text and 'OMP_THREAD_LIMIT: "1"' in text


def memory_block():
    return COMPOSE.read_text().split('\n  nas-memory:\n', 1)[1].split('\n  nas-', 1)[0]


def test_memory_service_is_private_bounded_and_opt_in():
    block = memory_block()
    for line in ['profiles: [memory]', 'image: nas-search-mcp:local', 'command: [memory-service]',
                 'restart: unless-stopped', 'init: true', 'user: "${NAS_UID:-1000}:${NAS_GID:-1000}"',
                 'read_only: true', 'cap_drop: [ALL]', 'security_opt: [no-new-privileges:true]',
                 '- /tmp:size=8m,mode=1777', 'mem_limit: 128m', 'memswap_limit: 128m',
                 'cpuset: "${NAS_CPUSET:-0-1}"', 'cpu_shares: ${NAS_CPU_SHARES:-512}',
                 '"127.0.0.1:${NAS_EDITOR_PORT:-8083}:8083"',
                 '"${NAS_MEMORY_PATH:-/volume1/app-data/nas-search/memory}:/memory:rw"',
                 '"${NAS_INDEX_PATH:-/volume1/app-data/nas-search/index}/memory-ipc:/ipc:rw"',
                 'test: [CMD, /app/nas-search, memory-health]']:
        assert line in block, line
    assert 'network_mode' not in block
    assert 'networks: [memory]' in block
    assert block.count('127.0.0.1:') == 1 and '0.0.0.0' not in block


def test_memory_service_environment_names():
    block = memory_block()
    for name in ['NAS_MEMORY_DB: /memory/memory.sqlite3', 'NAS_MEMORY_LISTEN_SOCKET: /ipc/memory.sock',
                 'NAS_MEMORY_SCOPES: "${NAS_MEMORY_SCOPES:-personal}"', 'NAS_EDITOR_LISTEN: ":8083"',
                 'NAS_EDITOR_OWNER: "${NAS_EDITOR_OWNER:-}"', 'NAS_EDITOR_ORIGIN: "${NAS_EDITOR_ORIGIN:-}"',
                 'NAS_JEV_API_KEY: "${NAS_JEV_API_KEY:-}"', 'NAS_JEV_URL: "${NAS_JEV_URL:-https://api.typesafe.ai}"',
                 'NAS_JEV_MODEL: "${NAS_JEV_MODEL:-jev-latest}"', 'NAS_JEV_SCOPES: "${NAS_JEV_SCOPES:-personal}"',
                 'NAS_MEMORY_ACCEPT_AT: "${NAS_MEMORY_ACCEPT_AT:-900}"', 'NAS_MEMORY_REJECT_AT: "${NAS_MEMORY_REJECT_AT:-100}"']:
        assert name in block, name


def test_main_service_gains_memory_settings_that_default_to_off():
    block = COMPOSE.read_text().split('\n  nas-search:\n', 1)[1].split('\n  nas-', 1)[0]
    for name in ['NAS_MEMORY_ENABLED: "${NAS_MEMORY_ENABLED:-false}"', 'NAS_MEMORY_SOCKET: /data/memory-ipc/memory.sock',
                 'NAS_MEMORY_SCOPES: "${NAS_MEMORY_SCOPES:-personal}"', 'NAS_OAUTH_PASSKEYS: "${NAS_OAUTH_PASSKEYS:-}"',
                 'NAS_EDITOR_ORIGIN: "${NAS_EDITOR_ORIGIN:-}"']:
        assert name in block, name
    assert 'networks' not in block
    assert 'JEV' not in block and '/memory:' not in block


@needs_docker
def test_memory_service_exists_only_with_its_profile():
    clean_env = {key: value for key, value in os.environ.items() if not key.startswith('NAS_')}
    base = [DOCKER, 'compose', '-f', str(COMPOSE), '--env-file', os.devnull]
    env = dict(clean_env, NAS_TOKEN='x' * 32)
    off = json.loads(subprocess.run(base + ['config', '--format', 'json'], env=env, capture_output=True, text=True, check=True).stdout)['services']
    assert 'nas-memory' not in off
    assert off['nas-search']['environment']['NAS_MEMORY_ENABLED'] == 'false'
    on = json.loads(subprocess.run(base + ['--profile', 'memory', 'config', '--format', 'json'], env=env, capture_output=True, text=True, check=True).stdout)['services']
    memory = on['nas-memory']
    assert memory['mem_limit'] == str(128 * 1024 * 1024) or memory['mem_limit'] == 134217728
    ports = memory['ports']
    assert [(p['host_ip'], p['target'], int(p['published'])) for p in ports] == [('127.0.0.1', 8083, 8083)]
    assert memory['read_only'] is True
    assert {v['target'] for v in memory['volumes']} == {'/memory', '/ipc'}
    assert memory['healthcheck']['test'] == ['CMD', '/app/nas-search', 'memory-health']
    assert list(memory['networks']) == ['memory']
    assert 'memory' not in on['nas-search'].get('networks', {})


def test_memory_network_is_declared_at_top_level():
    assert '\nnetworks:\n  memory: {}' in COMPOSE.read_text()
