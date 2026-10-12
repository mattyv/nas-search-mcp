import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

class ClientTests(unittest.TestCase):
    def setUp(self):
        self.installer = load('installer', 'install-mcp.py')
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.source = self.home / '.env'
        self.token = 'a-very-private-token-which-must-not-be-printed'
        self.source.write_text('NAS_TOKEN=' + self.token + '\nNAS_PHOTOS=false\n')
        self.config = self.home / 'desktop.json'
        self.config.write_text(json.dumps({'other': {'keep': True}, 'mcpServers': {'other-server': {'command': 'keep'}}}))
        self.args = ['--client', 'claude-desktop', '--url', 'http://127.0.0.1:8080/mcp', '--token-file', str(self.source), '--config', str(self.config), '--skip-check']
    def install(self, args=None):
        with patch.dict(os.environ, {'HOME': str(self.home)}), patch('sys.stdout', new_callable=io.StringIO) as out:
            code = self.installer.main(self.args if args is None else args)
            self.assertNotIn(self.token, out.getvalue())
            return code
    def test_desktop_merge_private_credentials_and_backup(self):
        self.assertEqual(self.install(), 0)
        config = json.loads(self.config.read_text())
        self.assertTrue(config['other']['keep'])
        self.assertEqual(config['mcpServers']['other-server']['command'], 'keep')
        entry = config['mcpServers']['nas-search']
        self.assertNotIn(self.token, json.dumps(entry))
        token_file = self.home / '.config/nas-search/token'
        self.assertEqual(token_file.read_text().strip(), self.token)
        self.assertEqual(stat.S_IMODE(token_file.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(token_file.parent.stat().st_mode), 0o700)
        self.assertTrue(self.config.with_name(self.config.name + '.bak').exists())
    def test_conflict_refused_and_replace_explicit(self):
        self.assertEqual(self.install(), 0)
        before = self.config.read_bytes()
        self.assertEqual(self.install(), 1)
        self.assertEqual(before, self.config.read_bytes())
        self.assertEqual(self.install(self.args + ['--replace']), 0)
    def test_dry_run_does_not_write_or_print_secret(self):
        before = self.config.read_bytes()
        self.assertEqual(self.install(self.args + ['--dry-run']), 0)
        self.assertEqual(before, self.config.read_bytes())
        self.assertFalse((self.home / '.config/nas-search').exists())
    def test_cli_commands_contain_no_secret(self):
        commands=[]
        def run(command, **kwargs):
            import subprocess
            commands.append(command)
            return subprocess.CompletedProcess(command, 1 if 'get' in command else 0, stdout='', stderr='')
        for client in ['codex', 'claude-code']:
            commands.clear()
            args=['--client',client,'--url','http://127.0.0.1:8080/mcp','--token-file',str(self.source),'--skip-check']
            with patch.object(self.installer.subprocess, 'run', side_effect=run):
                self.assertEqual(self.install(args),0)
            added=[c for c in commands if 'add' in c][0]
            self.assertIn('--', added)
            self.assertNotIn(self.token, ' '.join(added))
            if client=='claude-code': self.assertEqual(added[2:4],['add','--scope'])

    def test_cli_replace_failure_restores_config_and_credentials(self):
        for client,relative in [('codex','.codex/config.toml'),('claude-code','.claude.json')]:
            with self.subTest(client=client):
                config=self.home/relative
                config.parent.mkdir(parents=True,exist_ok=True)
                before=b'original configuration with unrelated settings'
                config.write_bytes(before)
                credential=self.home/'.config/nas-search/token'
                credential.parent.mkdir(parents=True,exist_ok=True)
                credential.write_text('previous-private-token-value')
                credential.chmod(0o600)
                def run(command,**kwargs):
                    import subprocess
                    if 'get' in command:return subprocess.CompletedProcess(command,0,stdout='',stderr='')
                    config.write_bytes(b'damaged by CLI')
                    return subprocess.CompletedProcess(command,1 if 'add' in command else 0,stdout='',stderr='')
                args=['--client',client,'--url','http://127.0.0.1:8080/mcp','--token-file',str(self.source),'--skip-check','--replace']
                with patch.dict(os.environ,{'CODEX_HOME':str(self.home/'.codex'),'CLAUDE_CONFIG_DIR':str(self.home)}),patch.object(self.installer.subprocess,'run',side_effect=run):
                    self.assertEqual(self.install(args),1)
                self.assertEqual(config.read_bytes(),before)
                self.assertEqual(credential.read_text(),'previous-private-token-value')

    def test_bridge_copy_failure_restores_previous_token(self):
        destination=self.home/'.config/nas-search'
        destination.mkdir(parents=True)
        token_file=destination/'token';token_file.write_text('previous-private-token-value');token_file.chmod(0o600)
        real_write=self.installer.atomic_write
        failed=False
        def atomic_write(path,data,mode=0o600):
            nonlocal failed
            if Path(path).name=='mcp_bridge.py' and not failed:
                failed=True
                raise OSError('simulated bridge copy failure')
            return real_write(path,data,mode)
        before=self.config.read_bytes()
        with patch.object(self.installer,'atomic_write',side_effect=atomic_write):
            self.assertEqual(self.install(),1)
        self.assertEqual(token_file.read_text(),'previous-private-token-value')
        self.assertEqual(self.config.read_bytes(),before)

    def test_linux_existing_default_config_auto_detected(self):
        default = self.home / '.config/Claude/claude_desktop_config.json'
        default.parent.mkdir(parents=True)
        default.write_bytes(self.config.read_bytes())
        args = self.args[:self.args.index('--config')] + ['--skip-check']
        with patch.dict(os.environ, {'HOME':str(self.home), 'XDG_CONFIG_HOME':''}), patch.object(self.installer.sys,'platform','linux'), patch('sys.stdout',new_callable=io.StringIO) as output:
            self.assertEqual(self.installer.main(args),0)
            self.assertIn(str(default),output.getvalue())
            self.assertIn('Restart Claude Desktop',output.getvalue())
            self.assertNotIn(self.token,output.getvalue())
        saved=json.loads(default.read_text())
        self.assertTrue(saved['other']['keep'])
        self.assertEqual(saved['mcpServers']['other-server']['command'],'keep')
        self.assertIn('nas-search',saved['mcpServers'])
        self.assertTrue(default.with_name(default.name+'.bak').exists())

    def test_linux_xdg_existing_config_used(self):
        xdg = self.home / 'custom-config'
        target = xdg / 'Claude/claude_desktop_config.json'
        target.parent.mkdir(parents=True)
        target.write_bytes(self.config.read_bytes())
        args = self.args[:self.args.index('--config')] + ['--skip-check']
        with patch.dict(os.environ, {'HOME':str(self.home), 'XDG_CONFIG_HOME':str(xdg)}), patch.object(self.installer.sys,'platform','linux'):
            self.assertEqual(self.installer.main(args),0)
        self.assertIn('nas-search',json.loads(target.read_text())['mcpServers'])
        self.assertFalse((self.home/'.config/Claude/claude_desktop_config.json').exists())

    def test_linux_missing_auto_config_requires_override(self):
        args = self.args[:self.args.index('--config')] + ['--skip-check']
        with patch.dict(os.environ, {'HOME':str(self.home), 'XDG_CONFIG_HOME':''}), patch.object(self.installer.sys,'platform','linux'), patch('sys.stderr',new_callable=io.StringIO) as error:
            self.assertEqual(self.installer.main(args),1)
            self.assertIn('--config',error.getvalue())
            self.assertNotIn(self.token,error.getvalue())
        self.assertFalse((self.home/'.config/Claude').exists())
        self.assertFalse((self.home/'.config/nas-search').exists())

    def test_linux_auto_detect_replace_safety_and_dry_run(self):
        target=self.home/'.config/Claude/claude_desktop_config.json'
        target.parent.mkdir(parents=True)
        target.write_bytes(self.config.read_bytes())
        args=self.args[:self.args.index('--config')]+['--skip-check']
        with patch.dict(os.environ,{'HOME':str(self.home),'XDG_CONFIG_HOME':''}),patch.object(self.installer.sys,'platform','linux'):
            before=target.read_bytes()
            self.assertEqual(self.installer.main(args+['--dry-run']),0)
            self.assertEqual(target.read_bytes(),before)
            self.assertEqual(self.installer.main(args),0)
            before=target.read_bytes()
            self.assertEqual(self.installer.main(args),1)
            self.assertEqual(target.read_bytes(),before)
            self.assertEqual(self.installer.main(args+['--replace']),0)

    def test_linux_empty_servers_preserves_desktop_preferences(self):
        target=self.home/'.config/Claude/claude_desktop_config.json'
        target.parent.mkdir(parents=True)
        preferences={'theme':'dark','windowBounds':{'width':1000},'mcpServers':{}}
        target.write_text(json.dumps(preferences))
        args=self.args[:self.args.index('--config')]+['--skip-check']
        with patch.dict(os.environ,{'HOME':str(self.home),'XDG_CONFIG_HOME':''}),patch.object(self.installer.sys,'platform','linux'):
            self.assertEqual(self.installer.main(args),0)
        saved=json.loads(target.read_text())
        self.assertEqual(saved['theme'],'dark')
        self.assertEqual(saved['windowBounds'],preferences['windowBounds'])
        self.assertIn('nas-search',saved['mcpServers'])
        self.assertEqual(json.loads(target.with_name(target.name+'.bak').read_text()),preferences)

    def test_chatgpt_desktop_uses_codex_registration_and_backup(self):
        custom=self.home/'custom-codex'
        custom.mkdir()
        config=custom/'config.toml'
        before=b'[unrelated]\nkeep = true\n'
        config.write_bytes(before)
        commands=[]
        def run(command,**kwargs):
            import subprocess
            commands.append(command)
            if 'get' in command:
                return subprocess.CompletedProcess(command,0,stdout='',stderr='')
            if 'add' in command:
                config.write_bytes(before+b'# registered\n')
            return subprocess.CompletedProcess(command,0,stdout='',stderr='')
        args=['--client','chatgpt-desktop','--url','http://127.0.0.1:8080/mcp','--token-file',str(self.source),'--skip-check','--replace']
        with patch.dict(os.environ,{'HOME':str(self.home),'CODEX_HOME':str(custom)}),patch.object(self.installer.subprocess,'run',side_effect=run),patch('sys.stdout',new_callable=io.StringIO) as output:
            self.assertEqual(self.installer.main(args),0)
            self.assertIn('ChatGPT Desktop local connection',output.getvalue())
            self.assertIn('Restart ChatGPT Desktop',output.getvalue())
            self.assertNotIn(self.token,output.getvalue())
        self.assertTrue(all(command[0]=='codex' for command in commands))
        self.assertTrue(any(command[:3]==['codex','mcp','add'] for command in commands))
        self.assertEqual(config.with_name('config.toml.nas-search.bak').read_bytes(),before)
        self.assertTrue(config.read_bytes().startswith(before))
        self.assertFalse((self.home/'.claude.json').exists())

    def test_chatgpt_desktop_dry_run_without_cli(self):
        args=['--client','chatgpt-desktop','--url','http://127.0.0.1:8080/mcp','--token-file',str(self.source),'--dry-run']
        with patch.dict(os.environ,{'HOME':str(self.home)}),patch.object(self.installer.subprocess,'run',side_effect=AssertionError('Dry run must not call CLI')):
            self.assertEqual(self.installer.main(args),0)
        self.assertFalse((self.home/'.config/nas-search').exists())

class BridgeTests(unittest.TestCase):
    def setUp(self):
        self.bridge = load('bridge', 'mcp_bridge.py')
        self.requests=[]
        test=self
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                test.assertEqual(self.headers.get('Authorization'),'Bearer test-token')
                body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                test.requests.append((body,dict(self.headers)))
                if self.path == '/malformed':
                    self.send_response(200);self.end_headers();self.wfile.write(b'{invalid');return
                if self.path == '/redirect':
                    self.send_response(302);self.send_header('Location','/mcp');self.end_headers();return
                if 'id' not in body:
                    self.send_response(202);self.end_headers();return
                result={'ok':True}
                if body['method']=='initialize':
                    result={'protocolVersion':body.get('params',{}).get('protocolVersion','2025-03-26'),'capabilities':{'tools':{}},'serverInfo':{'name':'test','version':'1'}}
                elif body['method']=='tools/call':result={'content':[{'type':'text','text':'ok'}]}
                elif body['method']=='tools/list':result={'tools':[{'name':n,'inputSchema':{'type':'object'}} for n in ['search','browse','read_file','index_status']]}
                response=json.dumps({'jsonrpc':'2.0','id':body['id'],'result':result})
                self.send_response(200);self.send_header('Mcp-Session-Id','session-test');self.send_header('Content-Type','text/event-stream');self.end_headers()
                self.wfile.write(('event: message\ndata: '+response+'\n\n').encode())
            def do_GET(self):
                test.requests.append(({'method':'redirected-get'},dict(self.headers)))
                self.send_response(200);self.end_headers();self.wfile.write(b'{}')
            def log_message(self,*args):pass
        self.server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        self.thread=threading.Thread(target=self.server.serve_forever,daemon=True);self.thread.start()
        self.addCleanup(self.server.server_close);self.addCleanup(self.server.shutdown)
        self.url=f'http://127.0.0.1:{self.server.server_port}/mcp'
    def test_http_jsonrpc_sse_session_and_notifications(self):
        bridge=self.bridge.Bridge(self.url,'test-token')
        result=bridge.request({'jsonrpc':'2.0','id':1,'method':'initialize','params':{}})
        self.assertEqual(result['id'],1)
        self.assertIsNone(bridge.request({'jsonrpc':'2.0','method':'notifications/initialized'}))
        result=bridge.request({'jsonrpc':'2.0','id':2,'method':'tools/list'})
        self.assertEqual(len(result['result']['tools']),5)
        self.assertEqual(self.requests[-1][1].get('Mcp-Session-Id'),'session-test')
        self.assertEqual(self.requests[-1][1].get('Mcp-Protocol-Version'),'2025-03-26')
    def test_official_mcp_sdk_stdio_handshake_and_call(self):
        try:
            import asyncio
            import sys
            from mcp import ClientSession, StdioServerParameters
            from mcp.client.stdio import stdio_client
        except ImportError:
            self.skipTest('optional official MCP SDK not installed; run with project .venv')
        async def check(token_file):
            parameters=StdioServerParameters(command=sys.executable,args=[str(ROOT/'scripts/mcp_bridge.py'),'--url',self.url,'--token-file',str(token_file)])
            async with stdio_client(parameters) as (reader,writer):
                async with ClientSession(reader,writer) as session:
                    await session.initialize()
                    tools=await session.list_tools()
                    self.assertEqual(len(tools.tools),5)
                    result=await session.call_tool('browse',{})
                    self.assertFalse(result.isError)
                    self.assertEqual(result.content[0].text,'ok')
        with tempfile.TemporaryDirectory() as directory:
            token_file=Path(directory)/'token'
            token_file.write_text('test-token');token_file.chmod(0o600)
            asyncio.run(check(token_file))

    def test_malformed_http_response_is_safe_rpc_error(self):
        bridge=self.bridge.Bridge(self.url.replace('/mcp','/malformed'),'test-token')
        incoming=io.StringIO(json.dumps({'jsonrpc':'2.0','id':7,'method':'initialize'})+'\n')
        outgoing=io.StringIO()
        self.bridge.serve_stdio(bridge,incoming,outgoing)
        reply=json.loads(outgoing.getvalue())
        self.assertEqual(reply['id'],7)
        self.assertEqual(reply['error']['code'],-32000)
        self.assertNotIn('test-token',outgoing.getvalue())

    def test_redirect_is_not_followed(self):
        bridge=self.bridge.Bridge(self.url.replace('/mcp','/redirect'),'test-token')
        with self.assertRaises(self.bridge.BridgeError):
            bridge.request({'jsonrpc':'2.0','id':1,'method':'initialize'})
        self.assertEqual(len(self.requests),1)

    def test_stdio_roundtrip_and_safe_invalid_inputs(self):
        bridge=self.bridge.Bridge(self.url,'test-token')
        incoming=io.StringIO('\n'.join([json.dumps({'jsonrpc':'2.0','id':1,'method':'initialize'}),json.dumps({'jsonrpc':'2.0','method':'notifications/initialized'}),'[]','not-json'])+'\n')
        outgoing=io.StringIO()
        self.bridge.serve_stdio(bridge,incoming,outgoing)
        results=[json.loads(line) for line in outgoing.getvalue().splitlines()]
        self.assertEqual(len(results),3)
        self.assertEqual(results[0]['id'],1)
        self.assertEqual(results[1]['error']['code'],-32600)
        self.assertEqual(results[2]['error']['code'],-32700)
        self.assertNotIn('test-token',outgoing.getvalue())

if __name__=='__main__':unittest.main()
