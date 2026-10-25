#!/usr/bin/env python3
"""Enable fixed read-only Synology Photos access without DSM credentials."""
import argparse
import fcntl
import http.client
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import socket
import stat
import subprocess
import sys
import tempfile
import time

ROOT = Path('/volume1/@nas-photos-broker')
INSTALL = Path('/volume1/@nas-search-upgrader')
RECOVERY = Path('/volume1/@nas-recovery')
PERSONAL_PHOTOS = Path('/volume1/homes/matt/Photos')
CONTAINER_SOCKET = '/synology-photos/photos.sock'
ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin', 'HOME': '/root'}
INPUTS = ('compose.yaml', 'scripts/install-nas.sh', 'scripts/restore-mobile.py',
          'scripts/upgrade-host.py', 'scripts/synology-photos-host.py', 'scripts/nas-recovery.py')


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


def load_restore(source):
    spec = importlib.util.spec_from_file_location('photos_restore', source / 'scripts/restore-mobile.py')
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


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
            connection.request('POST', '/people', body=json.dumps({'query': 'Lucas', 'offset': 0, 'limit': 5}),
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
    for option, key in [('matthew-path', 'NAS_MATTHEW_PATH'), ('photos-path', 'NAS_PHOTOS_PATH'),
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
result=call({"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"photos_people","arguments":{"query":"Lucas","offset":0,"limit":5}}})
assert not result.get("isError",False)
'''
    with tempfile.TemporaryFile() as output:
        subprocess.run(['/usr/local/bin/docker', 'exec', 'nas-search-nas-search-1', 'python3', '-c', check],
                       stdout=output, stderr=output, stdin=subprocess.DEVNULL, timeout=45,
                       check=True, env=ENV)


def enable(source, install, image, dry_run=False):
    validate_image(image)
    secure_directory(install)
    secure_directory(ROOT.parent)
    secure_directory(RECOVERY)
    protected_script(RECOVERY / 'nas-recovery.py')
    private_file(install / '.env')
    private_file(install / 'current-image')
    original = (install / '.env').read_bytes()
    env = read_env(original.decode('utf-8'))
    required = ('NAS_TOKEN', 'NAS_UID', 'NAS_GID', 'NAS_MATTHEW_PATH', 'NAS_CODE_PATH',
                'NAS_PHOTOS_PATH', 'NAS_INDEX_PATH', 'NAS_BIND_IP')
    if any(not env.get(key) for key in required):
        raise ValueError('Existing deployment configuration incomplete')
    allowed = [item.strip() for item in env.get('NAS_ALLOWED_FOLDERS', '').split(',')]
    denied = [item.strip() for item in env.get('NAS_DENIED_FOLDERS', '').split(',')]
    if ('Photos' not in allowed
            or any(item == 'Photos' or item.startswith('Photos/') for item in denied)):
        raise ValueError('Full Photos scope is required without denied photo subfolders')
    if env['NAS_UID'] != str(pwd.getpwnam('matt').pw_uid):
        raise ValueError('App UID must match matt')
    if not PERSONAL_PHOTOS.resolve(strict=True).is_dir():
        raise ValueError('Personal Photos directory unavailable')
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
    maintenance = load_restore(root)
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
                            or (install / 'current-image').read_text().strip() != previous_image):
                        raise ValueError('Deployment changed during preflight')
                    backup = install / '.env.before-synology-photos'
                    fd = os.open(str(backup), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                    with os.fdopen(fd, 'wb') as stream:
                        stream.write(original)
                        stream.flush()
                        os.fsync(stream.fileno())
                    child = start_broker(log)
                    wait_broker()
                    suffix = ('NAS_SYNOLOGY_PHOTOS_HOST_PATH=' + str(ROOT / 'run') + '\n'
                              'NAS_SYNOLOGY_PHOTOS_SOCKET=' + CONTAINER_SOCKET + '\n').encode()
                    merged = original + (b'' if original.endswith(b'\n') else b'\n') + suffix
                    atomic_write(install / '.env', merged)
                    try:
                        run_install(root, install, image, env, log, lock_fd)
                        verify_mcp(env)
                        atomic_write(RECOVERY / 'nas-recovery.py', bodies['scripts/nas-recovery.py'], 0o700)
                    except Exception:
                        atomic_write(install / '.env', original)
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
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('Run as root on the NAS')
    os.umask(0o077)
    try:
        enable(args.source_dir, args.install_dir, args.image, args.dry_run)
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
