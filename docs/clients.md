# Connect your MCP clients

The service uses Streamable HTTP at `/mcp` with a static bearer token. For the LAN pilot the URL is `http://192.168.1.4:8080/mcp`. Substitute a reachable Tailscale/HTTPS address when configured. The server, index, and models remain on the NAS; the desktop bridge forwards requests and can save downloaded originals on its own computer.

## Install the MCP connection

Fully quit Claude Desktop before changing its configuration, then reopen it after installation. If the NAS tools disappear later, check whether the `nas-search` entry is still present in the config file printed by the installer. Rerunning the installer restores a missing entry; use `--replace` only when replacing an existing entry.

Run from the project checkout on your client computer (Python 3.10 or newer):

```sh
python3 scripts/install-mcp.py --client codex \
  --url http://192.168.1.4:8080/mcp \
  --token-file build/nas-deployment/.env
```

Use `--client claude-code` for Claude CLI or `--client claude-desktop` for Claude Desktop. On Linux, the installer uses an existing `Claude/claude_desktop_config.json` under `XDG_CONFIG_HOME`, or `~/.config` when unset. If that file does not exist, specify your client's path with `--config`; the installer does not create a guessed Linux config. Automatic desktop paths also cover macOS and Windows. The `build/nas-deployment/.env` path is a private local deployment file; it is not included in the repository. Substitute your own NAS `.env` copy or a token-only file. Keep it outside source control and omit the token value from commands.

The installer checks the MCP handshake and tools before writing settings. It copies a dependency-free stdio bridge and token into `~/.config/nas-search`, protects them with owner-only permissions, and uses the CLI's supported registration command or merges desktop JSON. Existing entries with the same name are refused unless `--replace` is supplied. Backups are retained: `.nas-search.bak` for CLI configuration and `.bak` for desktop JSON. Failed configuration changes restore the prior configuration and credential files. `--dry-run` makes no changes or network requests; `--skip-check` is only for deliberately offline setup.

Start a new CLI session or fully restart the desktop app. Config installation and SDK tool calls have passed; conversations inside the client GUIs still need testing. The installed bridge supports this project's stateless HTTP server; it is not a general bridge for server-initiated requests or resumable event streams. You do not need Node.js or a shell-exported token with this installer. Manual native HTTP alternatives follow.

## Read complete documents and retrieve originals

`read_document` returns extracted text and page/section locations. Continue with its `next_offset` until `complete=true`; offsets count Unicode characters. This bypasses the search index's 200,000-character extraction cap, but source files still have a 32 MiB safety limit. English OCR is used for PDF pages without embedded text when `NAS_OCR=true`. Check OCR metadata; an unreadable scan can still contain missing text. Long scanned documents may repeat OCR of earlier pages during pagination, so reading can be slow.

With the installed stdio bridge, ask your assistant: **“Download this NAS file to /tmp/nas-melody.pdf using download_file.”** Supply the exact NAS `path` and an absolute local `output` path. The destination folder must already exist. The tool saves the complete original and returns its local path and byte count; it refuses to overwrite existing files. Downloads retain the 32 MiB source limit and source-version checks.

The file is saved on the computer running the bridge. A bridge running on a remote host saves there. Native HTTP connections do not offer `download_file`; use the helper below or reconnect through the installed bridge. A saved file does not automatically become a chat attachment.

After updating this checkout, rerun the client installation command with `--replace`, then start a new CLI session or fully quit and reopen the desktop app to load the new tool.

`fetch_file` returns original bytes in base64 batches, without conversion. For continuation calls, supply the first response's `version` as `expected_version`. A client must reassemble the bytes; receiving them does not establish that its model can interpret PDF/image attachments. To download the entire original to your computer:

```sh
python3 scripts/fetch-file.py --url http://192.168.1.4:8080/mcp \
  --token-file build/nas-deployment/.env \
  --path 'Matthew/Berklee/Week 10/Melody.pdf' --output /tmp/nas-melody.pdf
```

The helper uses authenticated MCP and writes a private local file. Existing output files are preserved unless `--replace` is explicit. PDFs are not converted to Markdown, and original NAS files stay read-only.

## Manual native HTTP setup

Use these alternatives if you prefer native HTTP to the bundled bridge.

### Claude CLI / Claude Code

Set `NAS_SEARCH_TOKEN` to the same token as the NAS `.env`, then run:

```sh
claude mcp add --transport http --scope user nas-search \
  http://192.168.1.4:8080/mcp \
  --header "Authorization: Bearer ${NAS_SEARCH_TOKEN}"
claude mcp list
```

Restart Claude Code and check `/mcp`. This command stores the configured credential in your client configuration; protect that file and avoid exposing the token in screenshots or shell history. These flags follow [official Claude Code documentation](https://code.claude.com/docs/en/mcp).

### Codex CLI

Add this table to your existing `~/.codex/config.toml`; keep existing entries:

```toml
[mcp_servers.nas_search]
url = "http://192.168.1.4:8080/mcp"
bearer_token_env_var = "NAS_SEARCH_TOKEN"
startup_timeout_sec = 30
tool_timeout_sec = 600
```

Export `NAS_SEARCH_TOKEN` in the environment that launches Codex, then restart it and run `codex mcp list`. The token value belongs in your environment, not in this TOML snippet. This follows [official OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

## ChatGPT Desktop and ChatGPT cloud

For ChatGPT Desktop's local Codex connection, run the client installer with `--client chatgpt-desktop`. This is an explicit alias for installing into the shared Codex configuration through `codex mcp`; the Codex CLI must be available. It uses the protected token file and stdio bridge, so no shell token export is needed. Existing entries require `--replace` to change them.

[Official OpenAI documentation](https://learn.chatgpt.com/docs/extend/mcp) confirms that ChatGPT Desktop, Codex CLI, and the IDE extension share MCP configuration for the same Codex host. Restart the connection from Settings → MCP servers, start a new local Codex chat, and use `/mcp` to check availability. Desktop conversation use remains unverified here. Cloud Work chats do not load this local configuration, even when opened in the desktop app; see [developer settings](https://learn.chatgpt.com/docs/developer-settings).

ChatGPT's **cloud** custom-MCP connection cannot directly reach a LAN address or your private Tailscale address. OpenAI documents [Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels), which could run beside the server on the NAS and make an outbound connection. It requires a Platform tunnel identity, runtime API key, and relevant organization/workspace permissions. A publicly reachable HTTPS service is another route. Neither is configured in this pilot, and this server currently provides static bearer authentication rather than an OAuth authorization server.

Do not expose port 8080 to the public internet to bypass that limitation. First prove LAN/client access; verify the tunnel/authentication path and account support as a separate integration.

## First requests

Ask your client to:

1. Call `index_status` and report scan progress and errors.
2. Search for a known phrase in a pilot document and show its source path/location.
3. Browse `Matthew/` and read a known small text/code file.
4. After photo indexing is enabled, search with `photos=true` for a description of a known image.

Initial indexing shares its worker with semantic search and document extraction, so those calls can wait. Status checks and original-file retrieval avoid the worker queue. A health check confirms only that HTTP is running. Search results come from a periodically updated index; validate important answers against the source file.

Before reporting a named document missing, call `find_files`. This searches live filenames and folder names, independently of OCR and indexing. Use a known folder as `prefix`, then pass the returned exact path to `read_document` or `fetch_file`. All whitespace-separated query terms must appear in the path, ignoring case. Check warnings and `truncated`: a partial search does not prove absence. Symlinks, hidden files, and excluded folders are omitted; matching by name does not guarantee the content can be extracted.

For a PDF with unreadable extracted glyphs, call `read_document` with `force_ocr: true`, and use the same setting with every `next_offset`. OCR must be enabled on the NAS. Check `metadata.text_quality_suspect`, `suspect_pages`, and the OCR fields before treating extracted text as reliable. The suspect flag describes the original text layer, even when OCR replaces it. Sheet music needs visual interpretation to understand the notation; OCR may recover only printed words.
