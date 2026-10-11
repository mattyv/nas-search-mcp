# Connect your MCP clients

The service uses Streamable HTTP at `/mcp` with a static bearer token. For the LAN pilot the URL is `http://192.0.2.10:8080/mcp`. Substitute a reachable Tailscale/HTTPS address when configured. The server, index, and models remain on the NAS; a desktop bridge only forwards requests.

## Install the MCP connection

Run from the project checkout on your client computer (Python 3.10 or newer):

```sh
python3 scripts/install-mcp.py --client codex \
  --url http://192.0.2.10:8080/mcp \
  --token-file build/nas-deployment/.env
```

Use `--client claude-code` for Claude CLI or `--client claude-desktop` for Claude Desktop. For the existing Linux desktop installation, also pass `--config "$HOME/.config/Claude/claude_desktop_config.json"`; automatic desktop paths cover macOS and Windows. The token source may instead be a token-only file. Never paste its value into a command.

The installer checks the MCP handshake and tools before writing settings. It copies a dependency-free stdio bridge and token into `~/.config/nas-search`, protects them with owner-only permissions, and uses the CLI's supported registration command or merges desktop JSON. Existing entries with the same name are refused unless `--replace` is supplied. Backups are retained: `.nas-search.bak` for CLI configuration and `.bak` for desktop JSON. Failed configuration changes restore the prior configuration and credential files. `--dry-run` makes no changes or network requests; `--skip-check` is only for deliberately offline setup.

Start a new CLI session or fully restart the desktop app. The installed bridge supports this project's stateless HTTP server; it is not a general bridge for server-initiated requests or resumable event streams. You do not need Node.js or a shell-exported token with this installer. Manual native HTTP and third-party bridge alternatives remain below.

## Read complete documents and retrieve originals

`read_document` returns extracted text and page/section locations. Continue with its `next_offset` until `complete=true`; offsets count Unicode characters. This bypasses the search index's 200,000-character extraction cap, but source files still have a 32 MiB safety limit. English OCR is used for PDF pages without embedded text when `NAS_OCR=true`. Check OCR metadata; an unreadable scan can still contain missing text. Long scanned documents may repeat OCR of earlier pages during pagination, so reading can be slow.

`fetch_file` returns original bytes in base64 batches, without conversion. For continuation calls, supply the first response's `version` as `expected_version`. A client must reassemble the bytes; receiving them does not establish that its model can interpret PDF/image attachments. To download the entire original to your computer:

```sh
python3 scripts/fetch-file.py --url http://192.0.2.10:8080/mcp \
  --token-file build/nas-deployment/.env \
  --path 'Documents/Melody.pdf' --output /tmp/nas-melody.pdf
```

The helper uses authenticated MCP and writes a private local file. Existing output files are preserved unless `--replace` is explicit. PDFs are not converted to Markdown, and original NAS files stay read-only.

## Claude CLI / Claude Code

Set `NAS_SEARCH_TOKEN` to the same token as the NAS `.env`, then run:

```sh
claude mcp add --transport http --scope user nas-search \
  http://192.0.2.10:8080/mcp \
  --header "Authorization: Bearer ${NAS_SEARCH_TOKEN}"
claude mcp list
```

Restart Claude Code and check `/mcp`. This command stores the configured credential in your client configuration; protect that file and avoid exposing the token in screenshots or shell history. These flags follow [official Claude Code documentation](https://code.claude.com/docs/en/mcp).

## Codex CLI

Add this table to your existing `~/.codex/config.toml`; keep existing entries:

```toml
[mcp_servers.nas_search]
url = "http://192.0.2.10:8080/mcp"
bearer_token_env_var = "NAS_SEARCH_TOKEN"
startup_timeout_sec = 30
tool_timeout_sec = 600
```

Export `NAS_SEARCH_TOKEN` in the environment that launches Codex, then restart it and run `codex mcp list`. The token value belongs in your environment, not in this TOML snippet. This follows [official OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

## Claude Desktop

Use the [`mcp-remote` stdio-to-HTTP bridge](https://github.com/punkpeye/mcp-remote) if your desktop client does not offer direct HTTP servers with static headers. Install Node.js on the client computer; the NAS does not need it.

Open Claude Desktop **Settings → Developer → Edit Config**, merge the following entry into `mcpServers`, and replace the token placeholder. Use an absolute path to `npx` if the desktop app cannot find it:

```json
{
  "mcpServers": {
    "nas-search": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote", "http://192.0.2.10:8080/mcp",
        "--allow-http", "--header", "Authorization:${NAS_AUTH_HEADER}"
      ],
      "env": {"NAS_AUTH_HEADER": "Bearer REPLACE_WITH_YOUR_TOKEN"}
    }
  }
}
```

The header format uses the bridge's documented workaround for spaces in desktop argument handling. `--allow-http` is for trusted private-network HTTP only. Keep the config private and restart Claude Desktop fully. This bridge configuration has not yet been tested against this project in Claude Desktop.

## ChatGPT Desktop and ChatGPT cloud

The current [OpenAI client documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli) says ChatGPT Desktop, Codex CLI, and the IDE extension share the Codex MCP configuration. Where your installed desktop build supports that configuration, try the Codex entry above and ensure the desktop process receives `NAS_SEARCH_TOKEN`. This route remains untested for this project; desktop version/account capabilities must be checked.

ChatGPT's **cloud** custom-MCP connection cannot directly reach a LAN address or your private Tailscale address. OpenAI documents [Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels), which could run beside the server on the NAS and make an outbound connection. It requires a Platform tunnel identity, runtime API key, and relevant organization/workspace permissions. A publicly reachable HTTPS service is another route. Neither is configured in this pilot, and this server currently provides static bearer authentication rather than an OAuth authorization server.

Do not expose port 8080 to the public internet to bypass that limitation. First prove LAN/client access; verify the tunnel/authentication path and account support as a separate integration.

## First requests

Ask your client to:

1. Call `index_status` and report scan progress and errors.
2. Search for a known phrase in a pilot document and show its source path/location.
3. Browse `Documents/` and read a known small text/code file.
4. After photo indexing is enabled, search with `photos=true` for a description of a known image.

A successful health check alone does not validate these operations. Search results come from a periodically updated index; validate important answers against the source file.
