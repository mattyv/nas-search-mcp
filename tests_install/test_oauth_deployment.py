import importlib.util
from pathlib import Path
from urllib.error import HTTPError
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('prepare_oauth', Path(__file__).resolve().parents[1] / 'scripts/prepare-oauth-nas.py')


def test_prepared_env_preserves_private_token_and_limits_scope():
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    original = 'NAS_TOKEN=private-existing-token\nNAS_MATTHEW_PATH=/volume1/Matthew/Documents\nNAS_PHOTOS_PATH=/volume1/docker/nas-search/pilot-photos\nNAS_DENIED_FOLDERS=Matthew/Private\nNAS_BIND_IP=192.168.1.4\n'
    result = module.prepared_env(original, '$2b$12$example-hash', 'https://mattssynology.tail0fe6aa.ts.net')
    assert result.count('NAS_TOKEN=private-existing-token') == 1
    assert 'NAS_ALLOWED_FOLDERS=Matthew,Code,Photos' in result
    assert 'NAS_DENIED_FOLDERS=Matthew/Private' in result
    assert "NAS_OAUTH_PASSWORD_HASH='$2b$12$example-hash'" in result
    assert 'COMPOSE_PROJECT_NAME=nas-search' in result
    assert 'NAS_PHOTOS_PATH=/volume1/docker/nas-search/pilot-photos' in result
    assert 'NAS_BIND_IP=192.168.1.4' in result


def test_verify_service_requires_private_health_and_public_oauth_challenge():
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    class Healthy:
        status = 200
        def __enter__(self): return self
        def __exit__(self, *_): pass
    challenge = HTTPError('http://127.0.0.1:8082/mcp', 401, 'Unauthorized', {'WWW-Authenticate': 'Bearer resource_metadata="https://mattssynology.tail0fe6aa.ts.net/.well-known/oauth-protected-resource"'}, None)
    with patch.object(module.urllib.request, 'urlopen', side_effect=[Healthy(), challenge]):
        module.verify_service()
    with patch.object(module.urllib.request, 'urlopen', side_effect=[Healthy(), Healthy()]):
        try:
            module.verify_service()
        except RuntimeError:
            pass
        else:
            assert False, 'public MCP must reject anonymous access'


def test_temporary_sudo_rule_removes_only_exact_installer_grant(tmp_path):
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    rule = tmp_path / 'sudo-rule'
    rule.write_text('different admin grant\n')
    module.remove_temporary_sudo_rule(rule)
    assert rule.exists()
    rule.write_text(module.INSTALLER_SUDO_RULE + '\n')
    module.remove_temporary_sudo_rule(rule)
    assert not rule.exists()


def test_image_smoke_uses_empty_sources_and_rejects_failed_startup():
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    class Result:
        returncode = 0
        stdout = 'vec0 startup OK\n'
        stderr = ''
    with patch.object(module.subprocess, 'run', return_value=Result()) as run:
        module.verify_image_smoke()
    args = run.call_args.args[0]
    assert '--network' in args and args[args.index('--network') + 1] == 'none'
    assert '-v' not in args and '--mount' not in args and '-p' not in args
    assert 'NAS_ROOT=/tmp/source' in args
    class Failed(Result):
        returncode = 1
        stdout = ''
    with patch.object(module.subprocess, 'run', return_value=Failed()):
        try:
            module.verify_image_smoke()
        except RuntimeError:
            pass
        else:
            assert False, 'a failed startup must stop installation'
