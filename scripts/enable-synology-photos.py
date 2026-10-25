#!/usr/bin/env python3
"""Enable fixed read-only Synology Photos access without DSM credentials."""
import argparse
import fcntl
import hashlib
import http.client
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time

ROOT = Path('/volume1/@nas-photos-broker')
INSTALL = Path('/volume1/@nas-search-upgrader')
RECOVERY = Path('/volume1/@nas-recovery')
CONTAINER_SOCKET = '/synology-photos/photos.sock'
ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin', 'HOME': '/root'}
INPUTS = ('compose.yaml', 'scripts/install-nas.sh', 'scripts/restore-mobile.py',
          'scripts/upgrade-host.py', 'scripts/synology-photos-host.py', 'scripts/nas-recovery.py',
          'scripts/update-upgrade-runner.py')


def validate_image(image):
    if not re.fullmatch(r'ghcr\.io/mattyv/nas-search-mcp:[0-9a-f]{40}', image):
        raise ValueError('Pinned repository image required')


def secure_directory(path):
    if not path.is_absolute() or '..' in path.parts:
        raise ValueError('Absolute directory required')
    for candidate in (path, *path.parents):
        info = candidate.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('Unsafe directory')


def private_file(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        raise ValueError('Unsafe private file')


def protected_script(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
        raise ValueError('Unsafe protected script')


def read_env(contents):
    values = {}
    for line in contents.splitlines():
        if not line or line.lstrip().startswith('#'):
            continue
        if re.match(r'^\s*(?:export\s+)?NAS_', line):
            if not re.match(r'^NAS_[A-Za-z0-9_]+=', line):
                raise ValueError('Noncanonical NAS setting')
            key, value = line.split('=', 1)
            if key in values:
                raise ValueError('Duplicate NAS setting')
            value = value.strip()
            if value.startswith(('"', "'")):
                if len(value) < 2 or value[-1] != value[0]:
                    raise ValueError('Invalid setting quoting')
                value = value[1:-1]
            if any(char in value for char in '\r\n\x00'):
                raise ValueError('Invalid setting value')
            values[key] = value
    return values


def account_scope(env, install):
    value = env['NAS_UID']
    if not re.fullmatch(r'[0-9]+', value):
        raise ValueError('Invalid app UID')
    uid = int(value)
    if not 1 <= uid <= 2147483647 or str(uid) != value:
        raise ValueError('Invalid app UID')
    account = pwd.getpwuid(uid)
    if (account.pw_uid != uid or not isinstance(account.pw_name, str)
            or not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.@-]{0,63}', account.pw_name)
            or pwd.getpwnam(account.pw_name).pw_uid != uid):
        raise ValueError('Local account identity mismatch')
    home = Path(account.pw_dir)
    if not home.is_absolute() or '..' in home.parts:
        raise ValueError('Invalid account home')
    home = home.resolve(strict=True)
    photos = home / 'Photos'
    if photos.is_symlink() or not photos.is_dir():
        raise ValueError('Personal Photos directory unavailable')
    physical = photos.resolve(strict=True)
    if physical.parent != home:
        raise ValueError('Personal Photos scope escaped account home')
    return {'version': 1, 'account': account.pw_name, 'app_uid': uid,
            'install_dir': str(install), 'photos_root': str(physical)}


def docker_capture(args):
    with tempfile.TemporaryFile() as output:
        subprocess.run(['/usr/local/bin/docker'] + args, stdin=subprocess.DEVNULL,
                       stdout=output, stderr=subprocess.DEVNULL, timeout=25, check=True, env=ENV)
        output.seek(0)
        result = output.read(1024 * 1024 + 1)
        if len(result) > 1024 * 1024:
            raise ValueError('Docker metadata too large')
        return result


def inspect_document_mount():
    mounts = json.loads(docker_capture(['inspect', '--format', '{{json .Mounts}}', 'nas-search-nas-search-1']))
    if not isinstance(mounts, list) or len(mounts) > 100:
        raise ValueError('Unsupported active mounts')
    found = []
    for mount in mounts:
        destination = mount.get('Destination', '')
        match = re.fullmatch(r'/files/([A-Za-z0-9_-]+)', destination)
        if not match or match.group(1) in ('Code', 'Photos'):
            continue
        if mount.get('Type') != 'bind' or mount.get('RW') is not False:
            raise ValueError('Active document source must be a read-only bind')
        source = mount.get('Source', '')
        if (not isinstance(source, str) or not source.startswith('/')
                or not re.fullmatch(r'[A-Za-z0-9_./@ -]+', source)):
            raise ValueError('Unsupported active source path')
        physical = Path(source).resolve(strict=True)
        if not physical.is_dir() or str(physical) != source:
            raise ValueError('Active source must be a physical directory')
        found.append((source, match.group(1)))
    if len(found) != 1:
        raise ValueError('Exactly one active document root required')
    return found[0]


def document_aliases(env):
    additions = b''
    legacy = not env.get('NAS_DOCUMENTS_PATH') or not env.get('NAS_DOCUMENTS_LABEL')
    if legacy:
        source, label = inspect_document_mount()
        for key, value in (('NAS_DOCUMENTS_PATH', source), ('NAS_DOCUMENTS_LABEL', label)):
            if key in env and env[key] != value:
                raise ValueError('Active document configuration mismatch')
            if key not in env:
                env[key] = value
                encoded = "'" + value + "'" if key.endswith('_PATH') else value
                additions += (key + '=' + encoded + '\n').encode()
    return legacy, additions


def authorize_photos(original, env, grant_photos_access):
    if type(grant_photos_access) is not bool:
        raise ValueError('Explicit Photos grant flag required')
    allowed = [item.strip() for item in env.get('NAS_ALLOWED_FOLDERS', '').split(',')]
    denied = [item.strip() for item in env.get('NAS_DENIED_FOLDERS', '').split(',')]
    if any(item == 'Photos' or item.startswith('Photos/') for item in denied):
        raise ValueError('Declared Photos denials cannot be removed by setup')
    if 'Photos' in allowed:
        return original
    if not grant_photos_access:
        raise ValueError('Full Photos scope requires explicit --grant-photos-access')
    previous = env.get('NAS_ALLOWED_FOLDERS', '')
    suffix = (b',' if previous else b'') + b'Photos'
    env['NAS_ALLOWED_FOLDERS'] = previous + (',' if previous else '') + 'Photos'
    key = b'NAS_ALLOWED_FOLDERS='
    lines = original.splitlines(keepends=True)
    for index, line in enumerate(lines):
        if not line.startswith(key):
            continue
        body = line.rstrip(b'\r\n')
        ending = line[len(body):]
        raw = body[len(key):]
        value = raw.strip(b' \t')
        leading = raw[:len(raw) - len(raw.lstrip(b' \t'))]
        trailing = raw[len(raw.rstrip(b' \t')):] if value else b''
        if value[:1] in (b'"', b"'"):
            value = value[:-1] + suffix + value[-1:]
        else:
            value += suffix
        lines[index] = key + leading + value + trailing + ending
        return b''.join(lines)
    return original + (b'' if original.endswith(b'\n') else b'\n') + key + b'Photos\n'


def atomic_write(path, body, mode=0o600):
    fd, temporary = tempfile.mkstemp(prefix='.photos-', dir=str(path.parent))
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        os.chown(temporary, 0, 0)
        os.chmod(temporary, mode)
        os.replace(temporary, str(path))
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def source_inputs(source):
    bodies = {}
    for name in INPUTS:
        fd = os.open(str(source / name), os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, 'rb') as stream:
            if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
                raise ValueError('Regular installer inputs required')
            body = stream.read(1024 * 1024 + 1)
            if len(body) > 1024 * 1024:
                raise ValueError('Installer input too large')
            bodies[name] = body
    return bodies


def snapshot(bodies):
    root = ROOT / 'setup'
    root.mkdir(mode=0o700)
    (root / 'scripts').mkdir(mode=0o700)
    for name, body in bodies.items():
        atomic_write(root / name, body, 0o700 if name.startswith('scripts/') else 0o600)
    atomic_write(ROOT / 'synology-photos-host.py', bodies['scripts/synology-photos-host.py'], 0o700)
    (ROOT / 'run').mkdir(mode=0o755)
    (ROOT / 'run').chmod(0o755)
    return root


def save_previous_state(contents):
    directory = ROOT / 'previous-state'
    directory.mkdir(mode=0o700)
    for name, body in contents.items():
        atomic_write(directory / name, body)
    # Persist both the file renames and newly created recovery directories before
    # touching a worker, service or deployment setting. Interrupted setup remains
    # an administrator recovery operation, never an automatic resume.
    for path in (directory, ROOT, ROOT.parent):
        fd = os.open(str(path), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)


def load_restore(source):
    spec = importlib.util.spec_from_file_location('photos_restore', source / 'scripts/restore-mobile.py')
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def prepare_runner(source, install):
    spec = importlib.util.spec_from_file_location('photos_runner_updater', source / 'scripts/update-upgrade-runner.py')
    updater = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(updater)
    workers = updater.find_workers(install)
    if len(workers) != 1:
        raise ValueError('Exactly one root upgrade worker required')
    protocol = install / '.recovery-protocol.json'
    private_file(protocol)
    with protocol.open('rb') as stream:
        record = json.loads(stream.read(4097))
    worker = workers[0]
    if record != {'version': 1, 'pid': worker.pid, 'start': worker.identity}:
        raise ValueError('Active worker must share deployment exclusion')
    return updater, worker


def update_root_runner(source, install, updater, worker):
    proc = Path('/proc') / str(worker.pid)
    if (updater.process_identity(proc) != worker.identity
            or not updater.worker_matches(proc, install)):
        raise ValueError('Paused worker changed')
    state = next(line for line in (proc / 'status').read_text().splitlines() if line.startswith('State:')).split()[1]
    if state not in ('T', 't'):
        raise ValueError('Worker was not paused by setup')
    os.kill(worker.pid, signal.SIGCONT)
    candidate = source / 'scripts/upgrade-host.py'
    if candidate.read_bytes() != (install / 'upgrade-host.py').read_bytes():
        updater.update_runner(install, candidate, hashlib.sha256(candidate.read_bytes()).hexdigest())


def restore_root_runner(install, updater, previous):
    # Only the freshly installed, exclusion-aware worker can be running here.
    # The caller still holds .deployment.lock, so no installer can be in flight.
    workers = updater.find_workers(install)
    if len(workers) != 1:
        raise ValueError('Worker rollback requires inspection')
    updater.stop_worker(workers[0], install)
    updater.atomic_install(install / 'upgrade-host.py', previous)
    launched = updater.start_worker(install)
    updater.wait_alive(install, launched.pid)


def start_broker(_log):
    fd = os.open(str(ROOT / 'host.log'), os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'ab') as log:
        return subprocess.Popen(['/usr/bin/python3', str(ROOT / 'synology-photos-host.py'), 'serve'],
                                stdin=subprocess.DEVNULL, stdout=log, stderr=log, close_fds=True,
                                start_new_session=True, cwd=str(ROOT), env=ENV)


class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(str(ROOT / 'run/photos.sock'))


def wait_broker():
    deadline = time.monotonic() + 35
    while True:
        connection = UnixHTTP('synology-photos', timeout=30)
        try:
            connection.request('POST', '/people', body=json.dumps({'query': '', 'offset': 0, 'limit': 1}),
                               headers={'Content-Type': 'application/json'})
            response = connection.getresponse()
            body = response.read(1024 * 1024 + 1)
            if (response.status == 200 and len(body) <= 1024 * 1024
                    and isinstance(json.loads(body).get('people'), list)):
                return
        except (OSError, ValueError, http.client.HTTPException):
            pass
        finally:
            connection.close()
        if time.monotonic() >= deadline:
            raise ValueError('Broker readiness failed')
        time.sleep(0.2)


def run_install(source, install, image, env, log, lock_fd):
    args = ['/bin/sh', str(source / 'scripts/install-nas.sh'), '--image', image, '--install-dir', str(install)]
    for option, key in [('documents-path', 'NAS_DOCUMENTS_PATH'), ('documents-label', 'NAS_DOCUMENTS_LABEL'), ('photos-path', 'NAS_PHOTOS_PATH'),
                        ('code-path', 'NAS_CODE_PATH'), ('data-path', 'NAS_INDEX_PATH'),
                        ('uid', 'NAS_UID'), ('gid', 'NAS_GID'), ('bind-ip', 'NAS_BIND_IP')]:
        args.extend(['--' + option, env[key]])
    subprocess.run(args, stdout=log, stderr=log, stdin=subprocess.DEVNULL,
                   check=True, env=ENV, pass_fds=(lock_fd,))


def verify_mcp(env):
    # The token remains inside the existing container; no secret appears in argv.
    check = '''import json,os,urllib.request
def call(body):
 request=urllib.request.Request("http://127.0.0.1:8080/mcp",data=json.dumps(body).encode(),headers={"Authorization":"Bearer "+os.environ["NAS_TOKEN"],"Content-Type":"application/json","Accept":"application/json,text/event-stream"})
 reply=urllib.request.urlopen(request,timeout=30).read(1048577).decode()
 if not reply.lstrip().startswith("{"):reply="\\n".join(line[5:].strip() for line in reply.splitlines() if line.startswith("data:"))
 result=json.loads(reply)
 assert "error" not in result
 return result["result"]
tools={tool["name"] for tool in call({"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}})["tools"]}
assert {"photos_people","photos_for_person","photos_preview"}<=tools
result=call({"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"photos_people","arguments":{"query":"","offset":0,"limit":1}}})
assert not result.get("isError",False)
'''
    with tempfile.TemporaryFile() as output:
        subprocess.run(['/usr/local/bin/docker', 'exec', 'nas-search-nas-search-1', 'python3', '-c', check],
                       stdout=output, stderr=output, stdin=subprocess.DEVNULL, timeout=45,
                       check=True, env=ENV)


def rollback_legacy(install, original_compose, original_image, env, log, lock_fd):
    atomic_write(install / 'compose.yaml', original_compose)
    atomic_write(install / 'current-image', original_image)
    args = ['/usr/local/bin/docker', 'compose']
    if env.get('NAS_UPLOAD_INDEXED_DESTINATIONS') == 'true':
        args.extend(['--profile', 'indexed-upload'])
    args.extend(['--env-file', str(install / '.env'), '--project-name', 'nas-search',
                 '--project-directory', str(install), '-f', str(install / 'compose.yaml'),
                 'up', '-d', '--no-build', '--remove-orphans'])
    subprocess.run(args, stdout=log, stderr=log, stdin=subprocess.DEVNULL,
                   check=True, env=ENV, pass_fds=(lock_fd,))
    check = '''import json,os,urllib.request,urllib.error
assert urllib.request.urlopen("http://127.0.0.1:8080/healthz",timeout=10).status==200
issuer=os.environ.get("NAS_OAUTH_ISSUER","")
if issuer:
 metadata=json.load(urllib.request.urlopen("http://127.0.0.1:8082/.well-known/oauth-authorization-server",timeout=10))
 assert metadata.get("issuer")==issuer
 try:
  urllib.request.urlopen("http://127.0.0.1:8082/mcp",timeout=10)
 except urllib.error.HTTPError as error:
  assert error.code==401 and "resource_metadata" in error.headers.get("WWW-Authenticate","")
 else:
  raise AssertionError("authentication required")
'''
    for attempt in range(3):
        try:
            subprocess.run(['/usr/local/bin/docker', 'exec', 'nas-search-nas-search-1', 'python3', '-c', check],
                           stdout=log, stderr=log, stdin=subprocess.DEVNULL, timeout=45, check=True, env=ENV)
            return
        except subprocess.CalledProcessError:
            if attempt == 2:
                raise
            time.sleep(2)


def enable(source, install, image, dry_run=False, grant_photos_access=False):
    validate_image(image)
    secure_directory(install)
    secure_directory(ROOT.parent)
    secure_directory(RECOVERY)
    protected_script(RECOVERY / 'nas-recovery.py')
    private_file(install / '.env')
    private_file(install / 'current-image')
    private_file(install / 'compose.yaml')
    protected_script(install / 'upgrade-host.py')
    original = (install / '.env').read_bytes()
    original_compose = (install / 'compose.yaml').read_bytes()
    original_image = (install / 'current-image').read_bytes()
    original_worker = (install / 'upgrade-host.py').read_bytes()
    env = read_env(original.decode('utf-8'))
    legacy, aliases = document_aliases(env)
    required = ('NAS_TOKEN', 'NAS_UID', 'NAS_GID', 'NAS_DOCUMENTS_PATH', 'NAS_CODE_PATH',
                'NAS_PHOTOS_PATH', 'NAS_INDEX_PATH', 'NAS_BIND_IP')
    if any(not env.get(key) for key in required):
        raise ValueError('Existing deployment configuration incomplete')
    authorized = authorize_photos(original, env, grant_photos_access)
    env.setdefault('NAS_DOCUMENTS_LABEL', 'Documents')
    if not re.fullmatch(r'[A-Za-z0-9_-]+', env['NAS_DOCUMENTS_LABEL']):
        raise ValueError('Invalid document label')
    account = account_scope(env, install)
    if ROOT.exists() or ROOT.is_symlink():
        raise FileExistsError('Photos setup already exists; inspect protected state before retrying')
    if any(key in env for key in ('NAS_SYNOLOGY_PHOTOS_SOCKET', 'NAS_SYNOLOGY_PHOTOS_HOST_PATH')):
        raise ValueError('Photos settings already configured')
    previous_image = (install / 'current-image').read_text().strip()
    validate_image(previous_image)
    bodies = source_inputs(source)
    if dry_run:
        return
    ROOT.mkdir(mode=0o700)
    root = snapshot(bodies)
    save_previous_state({'.env': original, 'compose.yaml': original_compose,
                         'current-image': original_image, 'upgrade-host.py': original_worker})
    maintenance = load_restore(root)
    updater, previous_worker = prepare_runner(root, install)
    child = None
    lock_fd = None
    with (ROOT / 'setup.log').open('xb') as log:
        os.chmod(str(ROOT / 'setup.log'), 0o600)
        try:
            with maintenance.pause_workers(install):
                lock_fd = os.open(str(install / '.deployment.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
                try:
                    fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    maintenance.check_idle(install)
                    if ((install / '.env').read_bytes() != original
                            or (install / 'current-image').read_bytes() != original_image
                            or (install / 'compose.yaml').read_bytes() != original_compose
                            or (install / 'upgrade-host.py').read_bytes() != original_worker):
                        raise ValueError('Deployment changed during preflight')
                    backup = install / '.env.before-synology-photos'
                    fd = os.open(str(backup), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                    with os.fdopen(fd, 'wb') as stream:
                        stream.write(original)
                        stream.flush()
                        os.fsync(stream.fileno())
                    atomic_write(ROOT / 'account.json', json.dumps(account, sort_keys=True).encode())
                    suffix = ('NAS_SYNOLOGY_PHOTOS_HOST_PATH=' + str(ROOT / 'run') + '\n'
                              'NAS_SYNOLOGY_PHOTOS_SOCKET=' + CONTAINER_SOCKET + '\n').encode()
                    merged = authorized + (b'' if authorized.endswith(b'\n') else b'\n') + aliases + suffix
                    runner_changed = False
                    install_started = False
                    try:
                        # The host rechecks private policy for every request. Make
                        # the explicitly authorized candidate visible before probing
                        # it, and restore the original on any subsequent failure.
                        atomic_write(install / '.env', merged)
                        child = start_broker(log)
                        wait_broker()
                        install_started = True
                        run_install(root, install, image, env, log, lock_fd)
                        verify_mcp(env)
                        update_root_runner(root, install, updater, previous_worker)
                        runner_changed = True
                        atomic_write(RECOVERY / 'nas-recovery.py', bodies['scripts/nas-recovery.py'], 0o700)
                    except Exception:
                        try:
                            if runner_changed:
                                restore_root_runner(install, updater, original_worker)
                        finally:
                            atomic_write(install / '.env', original)
                            if install_started:
                                if legacy:
                                    rollback_legacy(install, original_compose, original_image, env, log, lock_fd)
                                else:
                                    run_install(root, install, previous_image, env, log, lock_fd)
                        raise
                finally:
                    os.close(lock_fd)
                    lock_fd = None
        except Exception:
            if child is not None and child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=5)
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--source-dir', type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument('--install-dir', type=Path, default=INSTALL)
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--grant-photos-access', action='store_true',
                        help='Explicitly add Photos to the existing allowlist; never remove any declared denial')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run as root on the NAS')
    os.umask(0o077)
    try:
        enable(args.source_dir, args.install_dir, args.image, args.dry_run, args.grant_photos_access)
    except Exception:
        print('Photos setup failed. Inspect protected setup logs and service health; no credentials were printed.')
        return 1
    if args.dry_run:
        print('Photos setup is ready. No changes made.')
    else:
        print('Read-only Photos setup passed broker, MCP and service checks. Existing recovery tasks now supervise it.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
