"""A real MCP SDK must reach owner consent from an empty OAuth client store."""
import asyncio
from html.parser import HTMLParser
from pathlib import Path
import select
import shutil
import subprocess
from urllib.parse import parse_qs, urlparse

import pytest

ROOT = Path(__file__).resolve().parents[1]


class SessionInput(HTMLParser):
    session = None

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == 'input' and attrs.get('name') == 'session':
            self.session = attrs.get('value')


def test_fresh_mcp_sdk_registers_and_completes_owner_consent(tmp_path):
    httpx = pytest.importorskip('httpx')
    pytest.importorskip('mcp.client.auth')
    from mcp.client.auth import OAuthClientProvider
    from mcp.shared.auth import OAuthClientMetadata

    assert shutil.which('go'), 'Go is required for the OAuth interoperability fixture'
    executable = tmp_path / 'oauth-fixture'
    subprocess.run(['go', 'build', '-tags', 'sqlite_fts5', '-o', str(executable),
                    './tests_install/fixtures/oauth_probe_server.go'], cwd=ROOT,
                   check=True, capture_output=True, timeout=120)
    process = subprocess.Popen([str(executable), str(tmp_path)], stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, text=True)
    try:
        assert select.select([process.stdout], [], [], 15)[0], 'OAuth fixture did not start'
        issuer = process.stdout.readline().strip()
        assert issuer.startswith('http://127.0.0.1:')

        class Store:
            tokens = None
            client = None

            async def get_tokens(self): return self.tokens
            async def get_client_info(self): return self.client
            async def set_tokens(self, tokens): self.tokens = tokens
            async def set_client_info(self, client): self.client = client

        async def run():
            store = Store()
            callback_result = None
            consent_count = 0
            async with httpx.AsyncClient(follow_redirects=False, timeout=5) as browser:
                async def redirect(url):
                    nonlocal callback_result, consent_count
                    # Registration alone must not authorize file access.
                    assert (await browser.get(issuer + '/mcp')).status_code == 401
                    page = await browser.get(url)
                    assert page.status_code == 200, 'Fresh client cannot reach owner login'
                    parser = SessionInput()
                    parser.feed(page.text)
                    assert parser.session
                    response = await browser.post(issuer + '/authorize', data={
                        'session': parser.session, 'password': 'fixture-password-not-a-real-credential',
                        'consent': 'allow'}, headers={'Origin': issuer})
                    assert response.status_code == 303, 'Owner consent failed or deadlocked'
                    query = parse_qs(urlparse(response.headers['location']).query)
                    callback_result = (query['code'][0], query['state'][0])
                    consent_count += 1

                async def callback():
                    assert callback_result is not None
                    return callback_result

                auth = OAuthClientProvider(issuer + '/mcp', OAuthClientMetadata(
                    redirect_uris=['http://127.0.0.1:19191/callback'],
                    client_name='Fresh MCP SDK interoperability test',
                    token_endpoint_auth_method='none',
                    grant_types=['authorization_code', 'refresh_token'],
                    response_types=['code']), store, redirect, callback, timeout=5)
                async with httpx.AsyncClient(auth=auth, timeout=5) as client:
                    reply = await client.get(issuer + '/mcp')
                    assert reply.status_code == 200
                    assert reply.json() == {'authenticated': True}
                    assert (await client.get(issuer + '/mcp')).status_code == 200
                assert consent_count == 1
                assert store.client and store.client.client_id != 'existing-fixture-client'
                assert store.tokens and store.tokens.scope == 'files:read'
                assert store.tokens.refresh_token
        asyncio.run(run())
    finally:
        process.terminate()
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
