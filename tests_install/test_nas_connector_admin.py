import importlib.util
from pathlib import Path
from unittest.mock import patch


def module():
    path = Path(__file__).resolve().parents[1] / 'scripts/nas-connector-admin.py'
    spec = importlib.util.spec_from_file_location('nas_connector_admin', path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def test_sudo_rule_is_exact_and_removable(tmp_path):
    admin = module()
    expected = admin.sudo_rule_text(tmp_path, 'admin-user')
    assert expected.splitlines() == [
        f'admin-user ALL=(root) NOPASSWD: /usr/bin/python3 {tmp_path / "scripts/nas-connector-admin.py"} deploy',
        f'admin-user ALL=(root) NOPASSWD: /usr/bin/python3 {tmp_path / "scripts/nas-connector-admin.py"} cleanup',
    ]
    rule = tmp_path / 'rule'
    rule.write_text(expected + '\n')
    assert admin.remove_rule(rule, expected=expected)
    assert not rule.exists()
    rule.write_text('unrelated grant\n')
    assert not admin.remove_rule(rule, expected=expected)
    assert rule.exists()
    rule.unlink()
    rule.symlink_to(tmp_path / 'missing')
    assert not admin.remove_rule(rule, expected=expected)
    assert rule.is_symlink()


def test_diagnostic_exposes_only_known_startup_categories():
    admin = module()
    assert admin.startup_category('2026/10/14 invalid folder access configuration: private path') == 'invalid folder access configuration'
    assert admin.startup_category('2026/10/14 NAS search listening on :8080 (bearer authentication required)') == 'started'
    assert admin.startup_category('secret document title') == 'unclassified startup failure'


def test_docker_stderr_is_available_for_classification():
    admin = module()
    class Process:
        returncode = 0
        def communicate(self, timeout):
            return b'', b'NAS search listening on :8080'
    with patch.object(admin.subprocess, 'Popen', return_value=Process()):
        assert admin.startup_category(admin.command(['docker'])) == 'started'


def test_rollback_health_uses_existing_lan_binding():
    admin = module()
    with patch.object(admin, 'healthy', return_value=True) as healthy:
        assert admin.wait_healthy(False, 1, 'http://192.0.2.10:8080/healthz')
    healthy.assert_called_once_with('http://192.0.2.10:8080/healthz')


def test_candidate_env_rejects_comments_and_duplicate_security_values():
    admin = module()
    valid = ("NAS_TOKEN=" + "x" * 24 + "\nNAS_DOCUMENTS_LABEL=Documents\nNAS_ALLOWED_FOLDERS=Documents,Code,Photos\n"
             "NAS_OAUTH_ISSUER=https://login.example\n"
             "NAS_OAUTH_CLIENT_ID=nas-claude\n"
             "NAS_OAUTH_PASSWORD_HASH='$2b$12$" + "x" * 53 + "'\n")
    assert admin.valid_candidate_env(valid)
    assert not admin.valid_candidate_env('# ' + valid)
    assert not admin.valid_candidate_env(valid + 'NAS_ALLOWED_FOLDERS=\n')
    assert not admin.valid_candidate_env(valid.replace('NAS_OAUTH_CLIENT_ID=nas-claude', 'NAS_OAUTH_CLIENT_ID=other'))


def test_diagnostic_does_not_remove_a_container_without_project_label():
    admin = module()
    calls = []
    def fake_command(args, timeout=45):
        calls.append(args)
        if 'inspect' in args:
            return 'other-project\n'
        return ''
    with patch.object(admin, 'command', side_effect=fake_command), patch.object(admin, 'logs_category', return_value='started'):
        admin.diagnose()
    assert not any('rm' in args for args in calls)
