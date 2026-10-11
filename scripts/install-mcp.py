#!/usr/bin/env python3
"""Install NAS search into Codex, Claude Code, or Claude Desktop without exposing tokens.

Uses a local stdio bridge and each CLI's supported configuration commands.
Claude Desktop must be restarted after installation. ChatGPT is not configured.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

# Load the sibling module without requiring a package or modifying sys.path.
_spec = importlib.util.spec_from_file_location('nas_search_bridge', Path(__file__).with_name('mcp_bridge.py'))
bridge = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bridge)

class InstallError(Exception):
    pass

def atomic_write(path, data, mode=0o600):
    path = Path(path)
    if path.is_symlink():
        raise InstallError('refusing to overwrite a symbolic link')
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)

def desktop_path():
    if sys.platform == 'darwin':
        return Path.home() / 'Library/Application Support/Claude/claude_desktop_config.json'
    if sys.platform == 'win32':
        return Path(os.environ.get('APPDATA', str(Path.home()))) / 'Claude/claude_desktop_config.json'
    raise InstallError('Claude Desktop has no official Linux install; provide --config for your existing desktop client')

def cli_config_path(client):
    if client == 'codex':
        return Path(os.environ.get('CODEX_HOME',str(Path.home()/'.codex'))) / 'config.toml'
    # Verified using the installed CLI in a disposable HOME with CLAUDE_CONFIG_DIR.
    return Path(os.environ.get('CLAUDE_CONFIG_DIR',str(Path.home()))) / '.claude.json'

def run_cli(command):
    try:
        return subprocess.run(command, capture_output=True, text=True, timeout=30)
    except FileNotFoundError as exc:
        raise InstallError('install ' + command[0] + ' and put it on PATH first') from exc
    except subprocess.TimeoutExpired as exc:
        raise InstallError(command[0] + ' configuration command timed out') from exc

def probe(url, token):
    remote = bridge.Bridge(url, token, timeout=15)
    response = remote.request({'jsonrpc':'2.0', 'id':1, 'method':'initialize', 'params':{'protocolVersion':'2025-03-26', 'capabilities':{}, 'clientInfo':{'name':'nas-search-installer','version':'0.1.0'}}})
    if 'error' in response:
        raise InstallError('MCP initialization failed')
    remote.request({'jsonrpc':'2.0','method':'notifications/initialized'})
    response = remote.request({'jsonrpc':'2.0','id':2,'method':'tools/list'})
    expected = {'search','browse','read_file','index_status'}
    result = response.get('result')
    if not isinstance(result,dict) or not isinstance(result.get('tools'),list):
        raise InstallError('endpoint returned an invalid tool list')
    names = {tool.get('name') for tool in result['tools'] if isinstance(tool,dict)}
    if not expected.issubset(names):
        raise InstallError('endpoint does not expose the four NAS search tools')

def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client', required=True, choices=['codex','claude-code','claude-desktop'])
    parser.add_argument('--url', required=True)
    parser.add_argument('--token-file', type=Path, required=True, help='token-only file or .env containing NAS_TOKEN')
    parser.add_argument('--name', default='nas-search')
    parser.add_argument('--config', type=Path, help='Claude Desktop config path override')
    parser.add_argument('--replace', action='store_true', help='replace an existing entry with this name')
    parser.add_argument('--dry-run', action='store_true', help='show intended action; no endpoint request or writes')
    parser.add_argument('--skip-check', action='store_true', help='skip initialize/list-tools connectivity check')
    args = parser.parse_args(argv)
    try:
        bridge.validate_url(args.url)
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,63}', args.name):
            raise InstallError('name must contain 1 to 64 letters, digits, periods, underscores, or hyphens')
        if args.config and args.client != 'claude-desktop':
            raise InstallError('--config is only supported for Claude Desktop')
        token = bridge.read_token(args.token_file)
        if len(token) < 24:
            raise InstallError('NAS token must contain at least 24 characters')
        destination = Path.home() / '.config/nas-search'
        token_path = destination / ('token' if args.name == 'nas-search' else args.name + '.token')
        bridge_path = destination / 'mcp_bridge.py'
        launch = [sys.executable, str(bridge_path), '--url', args.url, '--token-file', str(token_path)]
        config_path = None
        config = None
        exists = False
        if args.client == 'claude-desktop':
            config_path = args.config or desktop_path()
            if config_path.is_symlink():
                raise InstallError('refusing to replace symlinked desktop config')
            try:
                config = json.loads(config_path.read_text()) if config_path.exists() else {}
            except (ValueError, OSError) as exc:
                raise InstallError('cannot read valid desktop JSON configuration') from exc
            if not isinstance(config,dict) or not isinstance(config.get('mcpServers',{}),dict):
                raise InstallError('desktop config and mcpServers must be JSON objects')
            exists = args.name in config.get('mcpServers',{})
        elif not args.dry_run:
            binary = 'codex' if args.client == 'codex' else 'claude'
            get = [binary,'mcp','get',args.name] + (['--json'] if args.client == 'codex' else [])
            exists = run_cli(get).returncode == 0
        if exists and not args.replace:
            raise InstallError('MCP entry already exists; use --replace to change it')
        if args.dry_run:
            print('Would install ' + args.name + ' for ' + args.client + ' using a protected local token file and stdio bridge.')
            return 0
        if not args.skip_check:
            probe(args.url, token)
        if destination.is_symlink():
            raise InstallError('refusing to use symlinked credential directory')
        destination.mkdir(parents=True, exist_ok=True, mode=0o700)
        destination.chmod(0o700)
        cli_path = cli_config_path(args.client) if args.client != 'claude-desktop' else None
        if cli_path and cli_path.is_symlink():
            raise InstallError('refusing to modify symlinked CLI configuration')
        old_cli = cli_path.read_bytes() if cli_path and cli_path.exists() else None
        if old_cli is not None:
            atomic_write(cli_path.with_name(cli_path.name+'.nas-search.bak'),old_cli)
        if token_path.is_symlink() or bridge_path.is_symlink():
            raise InstallError('refusing to overwrite symlinked credentials or bridge')
        old_token = token_path.read_bytes() if token_path.exists() else None
        old_bridge = bridge_path.read_bytes() if bridge_path.exists() else None
        try:
            atomic_write(token_path, (token+'\n').encode())
            atomic_write(bridge_path, Path(__file__).with_name('mcp_bridge.py').read_bytes())
            if args.client == 'claude-desktop':
                if config_path.exists():
                    atomic_write(config_path.with_name(config_path.name+'.bak'),config_path.read_bytes())
                config.setdefault('mcpServers',{})[args.name] = {'command':launch[0], 'args':launch[1:]}
                atomic_write(config_path,(json.dumps(config,indent=2)+'\n').encode())
            else:
                binary = 'codex' if args.client == 'codex' else 'claude'
                if exists:
                    remove = [binary,'mcp','remove'] + (['--scope','user'] if args.client == 'claude-code' else []) + [args.name]
                    if run_cli(remove).returncode != 0:
                        raise InstallError('could not remove existing MCP entry')
                add = [binary,'mcp','add'] + (['--scope','user'] if args.client == 'claude-code' else []) + [args.name,'--'] + launch
                if run_cli(add).returncode != 0:
                    raise InstallError('CLI could not add MCP entry; previous configuration restored')
        except Exception:
            if cli_path:
                if old_cli is None:
                    cli_path.unlink(missing_ok=True)
                else:
                    atomic_write(cli_path,old_cli)
            for target, previous in [(token_path,old_token),(bridge_path,old_bridge)]:
                if previous is None:
                    target.unlink(missing_ok=True)
                else:
                    atomic_write(target,previous)
            raise
        print('Installed ' + args.name + ' for ' + args.client + '. ' + ('Restart Claude Desktop.' if args.client == 'claude-desktop' else 'Start a new client session.'))
        return 0
    except (InstallError, bridge.BridgeError, OSError) as exc:
        # Avoid printing subprocess output, token contents, or request bodies.
        print('Installer: '+str(exc),file=sys.stderr)
        return 1

if __name__=='__main__':
    raise SystemExit(main())
