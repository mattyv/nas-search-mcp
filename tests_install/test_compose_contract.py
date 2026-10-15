"""Render installer output with real Compose when available (required on CI)."""
import json
import os
from pathlib import Path
import shutil
import subprocess

import pytest

from test_nas_installer import setup

DOCKER = shutil.which('docker')
pytestmark = pytest.mark.skipif(DOCKER is None, reason='Docker Compose is checked on CI')


@pytest.mark.parametrize('bind,expected', [
    ('127.0.0.1', {'127.0.0.1'}),
    ('192.0.2.10', {'127.0.0.1', '192.0.2.10'}),
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
