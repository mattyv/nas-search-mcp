# Claude web and mobile connector

This optional connector lets Anthropic's cloud access the folders authorized for this MCP service. OAuth does not prevent an authorized client from receiving the contents of those files. If MCP upgrades are enabled, an authenticated client can request an upgrade approval link. The owner must then complete fresh browser OAuth sign-in and explicitly approve the upgrade.

Use a dedicated OAuth listener for the public connector. Keep the private bearer-token listener on a trusted LAN or private network. Do not publish the private listener through Funnel or expose it directly to the internet.

## Configure the service

Keep `.env` owned by an administrator and readable only by that account. Never paste its contents, passwords, hashes, tokens, or client secrets into chat or commit them. Generate a unique password and a bcrypt hash using the project's password setup process; do not pass plaintext in shell arguments, shell history, or environment variables.

Set the OAuth values in the private `.env`:

| Setting | Value |
|---|---|
| `NAS_ALLOWED_FOLDERS` | Explicit client-visible folder names, such as `Documents,Code,Photos`; an empty value denies file access. |
| `NAS_OAUTH_ISSUER` | Public HTTPS origin, such as `https://NAS_PUBLIC_HOST`, without `/mcp`. |
| `NAS_OAUTH_CLIENT_ID` | The registered public client ID. |
| `NAS_OAUTH_PASSWORD_HASH` | Bcrypt hash of the owner sign-in password; never the plaintext password. |
| `NAS_OAUTH_CLIENT_SECRET_HASH` | Optional bcrypt hash for a confidential client; leave empty for a public PKCE client. |
| `NAS_DOWNLOAD_URL` | Optional private origin for file links. Empty uses the OAuth issuer when configured. |

Verify the physical source directories before deployment. Mount them read-only and expose only the intended client-visible names. The OAuth listener should bind to host loopback and be published only through the HTTPS route. Keep private bearer access on its intended private listener.

## Publish the OAuth listener

Before enabling public access, confirm the service's issuer metadata is correct, anonymous MCP requests receive an authentication challenge, authenticated requests are scoped to approved folders, and public routes do not reveal management pages or health endpoints. Test from a network outside the NAS LAN.

If using Tailscale Funnel, check your tailnet's HTTPS and Funnel policies first. Limit Funnel permission to the NAS device that hosts the service. Preserve other Tailscale devices, accounts, exit-node settings, and local-LAN access. Get explicit owner approval before changing Funnel state or tailnet ACLs.

Configure Funnel to forward HTTPS traffic to the OAuth listener's loopback port. Keep the private bearer listener unpublished. Verify the public `/mcp` endpoint presents the expected OAuth challenge and that metadata uses the same public origin. Do not publish a real endpoint or tailnet address in repository documentation.

In Claude web, add a custom connector using the public MCP URL `https://NAS_PUBLIC_HOST/mcp`. Select the registered OAuth client, leave the secret blank for a public PKCE client, and complete sign-in and consent. Use the exact callback URI shown by Claude's current connector UI. Confirm access with a harmless search. Configure mobile separately and verify its tool calls before relying on it.

## Open files from Claude

When `NAS_OAUTH_ISSUER` is set, Claude can create tap-to-open links under the same public origin. A public file link requires fresh browser OAuth sign-in, including the NAS password. Successful sign-in unlocks only that link in that browser for up to ten minutes. The link also expires after its configured lifetime. Never enter the password in Claude chat. Links and passwords must not be logged.

Set `NAS_DOWNLOAD_URL` to a private address to keep file links off the public route. Private links use their unguessable URL as the credential, so do not forward them. Changing the file or removing its folder from `NAS_ALLOWED_FOLDERS` invalidates the link. Links retain the configured file-size limit.

## Revoke access

Remove the custom connector from the client, remove its OAuth grant using the service's revocation procedure, and disable the public route if no public clients should connect. Revoking OAuth grants invalidates browser access to public file links; the links also expire within their configured lifetime. Before resetting Funnel, inspect and preserve any unrelated private Serve mappings.

Authentication limits and lockouts apply per expiring login session. Start a new browser sign-in after a locked session. These limits reduce application-level abuse but cannot guarantee availability during sustained network or resource attacks.
