# Claude web and mobile connector

This connector gives Anthropic's cloud authenticated, read-only access to the folders you select. The selected scope is the existing MCP scope: `Matthew`, `Code`, and `Photos`, including sensitive personal documents. Authorization permits Claude to read those documents; OAuth does not prevent an authorized client from receiving their contents.

The controlled NAS deployment and public endpoint checks passed. Claude web login and search work, and the connector appears in Claude mobile. Mobile tool calls and NAS Inspector remain unverified.

## Configure access and login

Keep the project `.env` owned by root and mode `600`. Never paste its contents, passwords, hashes, tokens, or client secrets into chat or commit them. `.env` and `.env.*` are ignored; `.env.example` contains only empty secret placeholders. Single-quote bcrypt hashes in `.env` so Docker Compose preserves dollar signs. Use a unique password of 12–72 bytes and bcrypt cost 12; a longer phrase is safer. The project `hash-password` command reads the password from standard input and produces a cost-12 bcrypt hash. Invoke it only through a setup process that prompts without echo, captures its output, and writes the hash directly to the root-owned `.env`; do not run it with output sent to the terminal. Do not place the password in shell arguments, shell history, or environment variables.

Review these settings locally:

| Setting | Value and effect |
| --- | --- |
| `NAS_ALLOWED_FOLDERS` | Empty denies all document access. Your selected scope is `Matthew,Code,Photos`, relative to `/files`. |
| `NAS_DENIED_FOLDERS` | Empty excludes nothing. Comma-separated relative folders exclude their descendants. |
| `NAS_MATTHEW_PATH` | Confirm `/volume1/Matthew/Documents`; mounted read-only at `/files/Matthew`. |
| `NAS_CODE_PATH` | Confirm `/volume1/Matthew/Code`; mounted read-only at `/files/Code`. |
| `NAS_PHOTOS_PATH` | Your existing pilot uses `/volume1/docker/nas-search/pilot-photos`; confirm it before using. The generic sample defaults to `/volume1/homes/matt/Photos`. |
| `NAS_OAUTH_ISSUER` | Public HTTPS origin, for example `https://NAS.TAILNET.ts.net`, without `/mcp`. Empty disables the OAuth listener. |
| `NAS_OAUTH_CLIENT_ID` | `nas-claude`, a pre-registered public client using PKCE. |
| `NAS_OAUTH_PASSWORD_HASH` | Single-quoted bcrypt hash of your login password; never the plaintext password. |
| `NAS_OAUTH_CLIENT_SECRET_HASH` | Optional bcrypt hash for a confidential client. Leave empty for the public client; no secret is required. |
| `NAS_OAUTH_LOCAL_REDIRECTS` | Exact loopback callback URLs for Inspector testing only; empty in production. |

OAuth state is stored in `/data/oauth.sqlite3`, on the existing writable index volume. Access and refresh token values are stored hashed. Access tokens expire after 10 minutes; refresh tokens expire after seven days. Keep that volume private and writable only by the project user and administrators. Original source mounts remain read-only.

Compose binds `8080` to both host loopback and the configured NAS LAN IP (`NAS_LAN_IP`, default `192.168.1.4`) to preserve existing private access. It binds the OAuth port `8082` only to host loopback. Port `8080` retains private bearer-token MCP access. Port `8082` serves OAuth and authenticated MCP at `/mcp`. Container listeners use all container interfaces so loopback publishing works; native listeners default to loopback. Funnel must target `8082`, never the private listener.

## Deploy and test before public exposure

The first NAS installer attempt failed verification and rolled back. On 2026-10-14, a corrected glibc-linked binary (SHA-256 prefix `f2cdf4…`) passed an offline Docker build and isolated `vec0` startup smoke test, then the controlled project-only deployment passed. Private health returned `200`; anonymous `http://127.0.0.1:8082/mcp` returned `401` with resource metadata. Docker published `8082` only on `127.0.0.1` and kept `8080` on loopback and `192.168.1.4`. The temporary sudo rule was removed.

For future recovery, read the NAS home `AGENTS.md` and `NORD-EXIT-HANDOFF.md` first. Preserve the work Tailscale account, VPN containers, exit-node settings, LAN access, and unrelated services. The live, root-owned mode-`700` project is `/volume1/nas-search-connector`, outside a DSM shared folder. Keep this checkout as the recovery source if a DSM update removes it. The temporary passwordless sudo rule and matt-owned staging copy were removed after successful deployment. The fixed helper remains in the secure project, but future redeployment needs a fresh, reviewed image and configuration. The controlled deploy operation restarts only `nas-search`, checks private health and the anonymous OAuth `401`, and restores the previous project service if verification fails. It does not enable Funnel or change Tailscale. Confirm `restart: unless-stopped` during routine maintenance. Ask before any restart outside this project or any push to git. NAS Inspector login/search remains unverified.

Local Dafny, Go, and Python checks and the local Inspector synthetic-fixture flow are recorded in D021. NAS Inspector login/search remains unverified. If testing it, use the official [MCP Inspector](https://modelcontextprotocol.io/docs/tools/inspector) against `http://127.0.0.1:8082/mcp`, configure `nas-claude`, allow only its exact local callback, complete password and PKCE login, list tools, and search a harmless document. Remove the callback exception afterward. Verify traversal, escaping symlinks, excluded folders, rate limits, and response caps.

## Funnel state and changes

The **host** Tailscale is 1.104.1, upgraded from 1.58.2 using the official signed package after DNS publication initially failed. The same tailnet identity (UserID `7023865582684939`) and private `8080` Serve route remain intact. Funnel is currently on and forwards public port `443` to `127.0.0.1:8082`. The VPN container's Tailscale is a separate instance. Any future Funnel or ACL change requires explicit user approval. In the Tailscale admin console, check **DNS → MagicDNS** and **HTTPS Certificates → Enable HTTPS**. Under **Access controls**, add only this NAS to Funnel's `nodeAttrs` target, preserving the rest of the policy:

```json
{"target": ["100.112.12.118"], "attr": ["funnel"]}
```

Do not use the default `autogroup:member` target, which grants every member device permission to create a Funnel. Wait for explicit user approval before changing the policy or Funnel state. See [Tailscale Funnel requirements](https://tailscale.com/docs/features/tailscale-funnel) and [policy targets](https://tailscale.com/docs/reference/targets-and-selectors).

After explicit approval, run on the NAS host:

```sh
tailscale funnel --bg --https=443 8082
```

`443` is the public HTTPS port; `8082` is the local target. Tailscale also permits public ports `8443` and `10000`. Confirm existing private Serve mappings still work. Stop and diagnose any command error before proceeding.

The verified public hostname is `mattssynology.tail0fe6aa.ts.net`, with DNS answers `103.84.155.153` and `103.84.155.217`. From a public relay, `/mcp` returned `401` with `WWW-Authenticate`, and OAuth metadata returned `200`. `/`, `/webman/`, and `/healthz` returned `404`; public port `8080` timed out. These checks establish the observed public responses, not Claude compatibility. Funnel is currently on; ask before changing its state or changing ACLs.

## Add Claude

In Claude web, open **Customize → Connectors → Add → Custom → Web**. Choose **Sign in now**, then **Use your own OAuth client**. Enter the name `nas-claude` and URL `https://mattssynology.tail0fe6aa.ts.net/mcp`; leave the client secret blank for the public PKCE client. Complete the password login and consent. The production callback is exactly `https://claude.ai/api/mcp/auth_callback`. Claude web login and search succeeded on 2026-10-14, and the connector appears in Claude mobile. A mobile tool call remains untested.

Then open Claude mobile on the same account, confirm the connector appears, and try a harmless search. A configured connector is not considered verified until the OAuth flow and search succeed. Follow the current [Claude connector authentication documentation](https://claude.com/docs/connectors/building/authentication) and [MCP authorization specification](https://modelcontextprotocol.io/specification/latest/basic/authorization) when diagnosing compatibility.

## Stop access

As root, run one command from the candidate project directory:

```sh
cd /volume1/nas-search-connector && ./scripts/kill-connector.sh
```

It stops only `nas-search` first, then revokes OAuth tokens through a one-shot container using the same database without publishing ports. Stopping first prevents an in-flight login or refresh from issuing tokens after revocation. If stopping fails, the connector may still be reachable and revocation is not attempted. If revocation fails, the service remains stopped: do not restart until revocation succeeds. It does not change Tailscale or other containers.

To remove Funnel, review existing private Serve mappings first, then run after authorization:

```sh
tailscale funnel reset
```

Reset can remove existing Serve/Funnel configuration; inspect and restore the private mapping if affected. Never reset the separate VPN container's configuration or switch Tailscale accounts as part of this task.
