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
