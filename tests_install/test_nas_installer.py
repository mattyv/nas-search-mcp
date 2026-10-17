import os
from pathlib import Path
import subprocess

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/install-nas.sh'


def setup(tmp_path):
    binaries = tmp_path / 'bin'
    binaries.mkdir()
    log = tmp_path / 'docker.log'
    (binaries / 'id').write_text('#!/bin/sh\ncase "$1" in -u) echo 0;; -g) echo 1234;; esac\n')
    (binaries / 'docker').write_text('''#!/bin/sh
printf '%s\\n' "$*" >> "$DOCKER_LOG"
case "$1" in
info) echo 'Server ready';;
inspect) [ -n "${ACTIVE_INSTALL:-}" ] || exit 1; printf '%s\\n' "$ACTIVE_INSTALL";;
pull) [ "${FAIL_PULL:-0}" = 0 ];;
run) [ "${FAIL_LIMITS:-0}" = 0 ];;
compose) if [ "${CHECK_NAS_ENV:-0}" = 1 ] && [ "${2:-}" != version ]; then
    [ -z "$(env | sed -n '/^NAS_[A-Za-z0-9_]*=/p')" ] || exit 91
fi
case "$*" in *"exec -T nas-search"*) [ "${FAIL_HEALTH:-0}" = 0 ];; esac;;
esac
''')
    (binaries / 'chown').write_text('#!/bin/sh\nprintf \"%s\\n\" \"$*\" >> \"$CHOWN_LOG\"\n')
    (binaries / 'sleep').write_text('#!/bin/sh\nexit 0\n')
    for binary in binaries.iterdir():
        binary.chmod(0o755)
    documents = tmp_path / 'Documents'
    photos = tmp_path / 'Photos'
    documents.mkdir()
    photos.mkdir()
    code = tmp_path / 'Code'
    code.mkdir()
    env = dict(os.environ, PATH=str(binaries) + ':' + os.environ['PATH'], DOCKER_LOG=str(log), CHOWN_LOG=str(tmp_path / 'chown.log'), SUDO_UID='1234', SUDO_GID='1234')
    args = ['sh', str(SCRIPT), '--image', 'ghcr.io/mattyv/nas-search-mcp:abc123', '--install-dir', str(tmp_path / 'install'), '--documents-path', str(documents), '--photos-path', str(photos), '--code-path', str(code), '--data-path', str(tmp_path / 'index'), '--bind-ip', '192.0.2.10']
    return args, env, log


def test_install_order_and_private_state(tmp_path):
    args, env, log = setup(tmp_path)
    result = subprocess.run(args, env=env, text=True, capture_output=True)
    assert result.returncode == 0, result.stderr
    state = tmp_path / 'install'
    contents = (state / '.env').read_text()
    assert 'NAS_UID=1234' in contents
    assert 'NAS_TOKEN=' in contents
    assert 'NAS_DOCUMENTS_LABEL=Documents' in contents
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
    assert not log.exists() or 'pull ' not in log.read_text()
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
    assert 'NAS_ALLOWED_FOLDERS=' not in updated
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
    protected = tmp_path / '@protected-upgrader'
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
    env['ACTIVE_INSTALL'] = '/srv/../private'
    result = subprocess.run(args, env=env, capture_output=True)
    assert result.returncode != 0
    assert not log.exists() or 'pull ' not in log.read_text()
    assert not (tmp_path / 'install').exists()


def test_empty_scope_survives_success_and_failure(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_bytes().replace(b'NAS_ALLOWED_FOLDERS=Documents,Code,Photos', b'NAS_ALLOWED_FOLDERS=')
    secret.write_bytes(original)
    for failure in ['0', '1']:
        env['FAIL_HEALTH'] = failure
        result = subprocess.run(args, env=env, capture_output=True)
        assert (result.returncode == 0) == (failure == '0')
        assert secret.read_bytes() == original


def test_sources_cannot_be_inside_writable_data(tmp_path):
    args, env, log = setup(tmp_path)
    args[args.index('--data-path') + 1] = str(tmp_path)
    result = subprocess.run(args, env=env, capture_output=True)
    assert result.returncode != 0
    assert not log.exists() or 'pull ' not in log.read_text()


def test_existing_queue_cannot_overlap_sources_or_index(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_bytes()
    for queue in [tmp_path / 'Documents', tmp_path / 'Documents' / 'queue', tmp_path, tmp_path / 'index']:
        secret.write_bytes(original + f'NAS_UPGRADE_PATH={queue}\n'.encode())
        log.write_text('')
        result = subprocess.run(args, env=env, capture_output=True)
        assert result.returncode != 0, queue
        assert not log.exists() or 'pull ' not in log.read_text()


def test_effective_loaded_paths_reject_alias_overlap(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    alias = tmp_path / 'alias'
    alias.symlink_to(tmp_path / 'Documents', target_is_directory=True)
    secret = tmp_path / 'install' / '.env'
    secret.write_text(secret.read_text() + f'NAS_UPGRADE_PATH={alias}/queue\n')
    log.write_text('')
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert not log.exists() or 'pull ' not in log.read_text()


def test_bind_effective_mappings_and_legacy_preservation(tmp_path):
    args, env, log = setup(tmp_path)
    state = tmp_path / 'install'
    for bind in ['127.0.0.1', '192.0.2.10', '0.0.0.0']:
        args[args.index('--bind-ip') + 1] = bind
        if (state / '.env').exists():
            (state / '.env').unlink()
        assert subprocess.run(args, env=env, capture_output=True).returncode == 0
        compose = (state / 'compose.yaml').read_text()
        assert f'"{bind}:8080:8080"' in compose
        assert compose.count('127.0.0.1:8080:8080') == (0 if bind == '0.0.0.0' else 1)
        assert ('192.0.2.10:8080:8080' in compose) == (bind == '192.0.2.10')
    secret = state / '.env'
    original = secret.read_bytes().replace(b'NAS_BIND_IP=0.0.0.0', b'NAS_LAN_IP=192.0.2.11')
    secret.write_bytes(original)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    assert '192.0.2.11:8080:8080' in (state / 'compose.yaml').read_text()
    assert secret.read_bytes() == original
    env['FAIL_HEALTH'] = '1'
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert secret.read_bytes() == original


def test_malformed_bind_rejected_before_pull(tmp_path):
    args, env, log = setup(tmp_path)
    for bind in ['999.1.2.3', '127.1', '1..2.3', '01.2.3.4']:
        args[args.index('--bind-ip') + 1] = bind
        assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert not log.exists() or 'pull ' not in log.read_text()


def test_writable_index_cannot_contain_install_state(tmp_path):
    args, env, log = setup(tmp_path)
    args[args.index('--data-path') + 1] = str(tmp_path / 'writable')
    args[args.index('--install-dir') + 1] = str(tmp_path / 'writable' / 'install')
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert not log.exists() or 'pull ' not in log.read_text()


def test_ordinary_install_children_are_allowed(tmp_path):
    args, env, log = setup(tmp_path)
    args[args.index('--data-path') + 1] = str(tmp_path / 'install' / 'data')
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    secret.write_text(secret.read_text() + f'NAS_UPGRADE_PATH={tmp_path / "install" / "queue"}\n')
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0


def test_root_writable_mount_rejected(tmp_path):
    args, env, log = setup(tmp_path)
    args[args.index('--data-path') + 1] = '/'
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert not log.exists() or 'pull ' not in log.read_text()


def test_compose_uses_checked_env_not_ambient_overrides(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_bytes().replace(b'NAS_ALLOWED_FOLDERS=Documents,Code,Photos', b'NAS_ALLOWED_FOLDERS=')
    secret.write_bytes(original)
    env.update(CHECK_NAS_ENV='1', NAS_ALLOWED_FOLDERS='Documents,Code,Photos',
               NAS_INDEX_PATH=str(tmp_path), NAS_UPGRADE_PATH=str(tmp_path / 'Documents'),
               NAS_TOKEN='ambient-private-token', NAS_FUTURE_SETTING='future-override')
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert secret.read_bytes() == original
    assert 'ambient-private-token' not in result.stdout + result.stderr + log.read_text()


def test_ambiguous_dotenv_assignments_rejected_without_changes(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_bytes()
    for extra in [f'NAS_INDEX_PATH={tmp_path / "Documents"}',
                  f'export NAS_INDEX_PATH={tmp_path / "Documents"}',
                  f' NAS_INDEX_PATH = {tmp_path / "Documents"}',
                  'NAS_FUTURE_FIELD=one\nNAS_FUTURE_FIELD=two']:
        contents = original + (extra + '\n').encode()
        secret.write_bytes(contents)
        secret.chmod(0o644)
        log.write_text('')
        result = subprocess.run(args, env=env, capture_output=True, text=True)
        assert result.returncode != 0, extra
        assert 'pull ' not in log.read_text()
        assert secret.read_bytes() == contents
        assert secret.stat().st_mode & 0o077 == 0o044
        assert str(tmp_path / 'Documents') not in result.stderr


def test_canonical_dotenv_preserves_quoted_hash_and_space_values(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_bytes() + b"# NAS_INDEX_PATH=ignored comment\nOTHER_SETTING=value\nNAS_OAUTH_PASSWORD_HASH='$2b$12$literal'\nNAS_FUTURE_FIELD='space value'\n"
    secret.write_bytes(original)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert secret.read_bytes() == original


def test_dotenv_unquoted_path_whitespace_cannot_hide_source_overlap(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    (tmp_path / 'Documents ').mkdir()
    secret = tmp_path / 'install' / '.env'
    original = secret.read_text()
    for value in [str(tmp_path / 'Documents') + ' ', '  ' + str(tmp_path / 'Documents') + '  ']:
        secret.write_text(original.replace(f"NAS_INDEX_PATH='{tmp_path / 'index'}'", f'NAS_INDEX_PATH={value}'))
        log.write_text('')
        result = subprocess.run(args, env=env, capture_output=True)
        assert result.returncode != 0
        assert 'pull ' not in log.read_text()


def test_dotenv_quoted_path_inner_spaces_preserved(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    (tmp_path / 'index ').mkdir()
    secret = tmp_path / 'install' / '.env'
    original = secret.read_text().replace(f"NAS_INDEX_PATH='{tmp_path / 'index'}'", f'NAS_INDEX_PATH=  "{tmp_path / "index "}"  ')
    secret.write_text(original)
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert f"{tmp_path / 'index '}:/check-data" in log.read_text()
    assert secret.read_text() == original


def test_dotenv_path_unbalanced_or_internal_quotes_fail_closed(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    secret = tmp_path / 'install' / '.env'
    original = secret.read_text()
    for value in [f'"{tmp_path / "index"}', str(tmp_path / 'in"dex'), f"'{tmp_path / 'index'}\"'\""]:
        secret.write_text(original.replace(f"NAS_INDEX_PATH='{tmp_path / 'index'}'", f'NAS_INDEX_PATH={value}'))
        log.write_text('')
        assert subprocess.run(args, env=env, capture_output=True).returncode != 0
        assert 'pull ' not in log.read_text()


def test_upload_mount_only_for_existing_documents_subfolder(tmp_path):
    args, env, log = setup(tmp_path)
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    state = tmp_path / 'install'
    assert ':/uploads:rw' not in (state / 'compose.yaml').read_text()
    secret = state / '.env'
    original = secret.read_text()
    uploads = tmp_path / 'Documents' / 'Uploads'
    uploads.mkdir()
    secret.write_text(original + f'NAS_UPLOAD_ENABLED=true\nNAS_UPLOAD_PATH={uploads}\n')
    args[args.index('--image') + 1] = 'ghcr.io/mattyv/nas-search-mcp:def456'
    result = subprocess.run(args, env=env, capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert '${NAS_UPLOAD_PATH}:/uploads:rw' in (state / 'compose.yaml').read_text()
    assert secret.read_text() == original + f'NAS_UPLOAD_ENABLED=true\nNAS_UPLOAD_PATH={uploads}\n'
    assert f'NAS_UPLOAD_PATH={uploads}' not in result.stdout
    outside = tmp_path / 'outside' / 'Uploads'
    outside.mkdir(parents=True)
    secret.write_text(original + f'NAS_UPLOAD_ENABLED=true\nNAS_UPLOAD_PATH={outside}\n')
    log.write_text('')
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert 'pull ' not in log.read_text()
