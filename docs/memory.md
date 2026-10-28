# Shared memory

Shared memory gives Claude, Codex, and your other assistants one place to keep notes about your ongoing projects. Tell one assistant to remember something, and another can read it later. The notes live in a small database on your NAS.

Memory is off by default. Nothing in this guide changes how search works.

## What you get

- `journal_append` saves a note when you ask an assistant to remember or record something.
- `context_open` loads the notes for a project at the start of a conversation.
- A web editor, which only you can open, lets you read, fix, and delete notes.

## Turn it on

1. Pick a folder for the database on the NAS, outside your documents, code, photos, index, and upgrade folders. The installer creates it.
2. Add these lines to the private `.env` in the install folder:

   ```
   NAS_MEMORY_ENABLED=true
   NAS_MEMORY_PATH=/volume1/app-data/nas-search/memory
   NAS_EDITOR_OWNER=you@example.com
   NAS_EDITOR_ORIGIN=https://NAS_NAME.TAILNET.ts.net:8444
   ```

   Set `NAS_EDITOR_OWNER` to your Tailscale login. The next section shows how to find it and the origin.
3. Upgrade through MCP (ask your assistant to run `upgrade_from_main`), or run the installer on the NAS.

The installer checks every setting before it changes anything. It creates the folders, starts a small `nas-memory` container (128 MB limit), and waits for it to report healthy. If the check fails, it rolls back to the previous release. To turn memory off, set `NAS_MEMORY_ENABLED=false` and upgrade again. The installer stops the container and leaves your notes in place.

## Open the editor from your phone and computer

The editor listens on the NAS itself (`127.0.0.1:8083`). Share it with your own devices through Tailscale. Do not use `tailscale funnel`; that would put it on the public internet.

1. On the NAS, run:

   ```sh
   sudo /var/packages/Tailscale/target/bin/tailscale serve --bg --https=8444 http://127.0.0.1:8083
   ```

2. Find your NAS name and tailnet name with `tailscale status`. Your login appears with `tailscale whois NAS_TAILSCALE_IP`.
3. Set `NAS_EDITOR_ORIGIN=https://NAS_NAME.TAILNET.ts.net:8444` and `NAS_EDITOR_OWNER=<your Tailscale login>` in `.env`, then upgrade.
4. Open that address on your phone.
5. Add it to your home screen:
   - Safari: Share, then Add to Home Screen.
   - Chrome: menu, then Add to home screen.

A Mac that is connected to a different tailnet, such as a work one, cannot reach the editor until you switch it back to your personal tailnet. Do not change the work connection to reach it. Use your phone instead.

## Tell Claude to use it

Put this line in Claude's personal preferences or in a project's instructions:

> At the start of a conversation about an ongoing project, call context_open for that project before answering. When I ask you to remember or record something, use journal_append.

## Scopes

A scope names a group of notes, such as `personal` or `work/acme`. The assistant can read and write only the scopes listed in `NAS_MEMORY_SCOPES` (default `personal`). Use lowercase letters, digits, `_` and `-`, with `/` between parts, and commas between scopes: `personal,work/acme`.

Anything under `work/...` is work material. Keep it out of `NAS_JEV_SCOPES` so it never leaves the NAS.

## Passkeys

Passkeys protect the sign-in page that connects an assistant to your NAS. Set `NAS_OAUTH_PASSKEYS` in `.env`:

- `off`: password only.
- `enroll` (default): password works, and you can add a passkey.
- `required`: sign-in needs a passkey.

Register a passkey:

- Mac: use iCloud Keychain when the browser offers it.
- Ubuntu: use Chrome, a hardware security key, or scan the QR code with your phone.

Manage passkeys at `<issuer>/passkeys`, where `<issuer>` is your `NAS_OAUTH_ISSUER` address.

If you lose every passkey while in `required` mode, run this on the NAS to clear them:

```sh
sudo docker exec nas-search-nas-search-1 /app/nas-search reset-passkeys
```

## Optional: screen notes with the Jev classifier

Jev is a hosted classifier from TypeSafe that scores notes. It is optional, and notes work without it.

1. Get an API key from TypeSafe.
2. Add `NAS_JEV_API_KEY=<key>` to `.env`. The installer never prints it.
3. Choose which scopes it may see with `NAS_JEV_SCOPES` (default `personal`).

Only notes in `NAS_JEV_SCOPES` go to TypeSafe. Notes you write yourself in the editor are never sent.

Two thresholds, scored from 0 to 1000, control the result. A score at or above `NAS_MEMORY_ACCEPT_AT` (default 900) is accepted. A score at or below `NAS_MEMORY_REJECT_AT` (default 100) is rejected. Scores in between, and entries the classifier never sees, wait in the editor's Review screen for you to decide. Keep `0 <= reject < accept <= 1000`.

Other settings: `NAS_JEV_URL` (default `https://api.typesafe.ai`), `NAS_JEV_MODEL` (default `jev-latest`), `NAS_EDITOR_PORT` (default 8083, range 1024 to 65535).

## Limits

- Claude does not capture your chats on its own. It writes a note only when you ask.
- Memory search matches keywords. It has no embeddings yet, so it will miss notes that use different words.
- Speed and memory use on the DS220+ are not yet measured.
- The classifier needs internet access from the NAS. Everything else stays local.
