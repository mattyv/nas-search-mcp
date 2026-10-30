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

The editor listens on the NAS itself (`127.0.0.1:8083`). Share it with your own devices through Tailscale. Do not use `tailscale funnel`; that would put it on the public internet. The editor refuses any request that arrives through Funnel.

1. In the Tailscale admin console, turn on MagicDNS and HTTPS certificates for your tailnet. `tailscale serve --https=8444` fails until both are on.
2. On the NAS, run:

   ```sh
   sudo /var/packages/Tailscale/target/bin/tailscale serve --bg --https=8444 http://127.0.0.1:8083
   ```

3. Find your NAS name and tailnet name with `tailscale status`. Use your own Tailscale login for `NAS_EDITOR_OWNER`: run `tailscale status` on one of your own devices and copy the login shown there. `tailscale whois NAS_TAILSCALE_IP` shows the NAS's owner, which is you only if the NAS is on your personal account. Logins with non-ASCII characters are not supported yet.
4. Set `NAS_EDITOR_ORIGIN=https://NAS_NAME.TAILNET.ts.net:8444` and `NAS_EDITOR_OWNER=<your Tailscale login>` in `.env`, then upgrade.
5. Open that address on your phone.
6. Add it to your home screen:
   - Safari: Share, then Add to Home Screen.
   - Chrome: menu, then Add to home screen.

A Mac that is connected to a different tailnet, such as a work one, cannot reach the editor until you switch it back to your personal tailnet. Do not change the work connection to reach it. Use your phone instead.

## Tell Claude to use it

Put this line in Claude's personal preferences or in a project's instructions:

> At the start of a conversation about an ongoing project, call context_open for that project before answering. When I ask you to remember or record something, use journal_append.

## Forgetting

Assistants cannot erase notes. When you ask one to forget something, it gives you a link to that note in the editor. Open the link and delete the note there. The memory service treats every caller on the assistant side as an assistant, so no assistant can forget, and there is no approval page.

Assistants also cannot replace a note you confirmed, and cannot write checkpoint entries with `journal_append`.

## Scopes

A scope names a group of notes, such as `personal` or `work/acme`. The assistant can read and write only the scopes listed in `NAS_MEMORY_SCOPES` (default `personal`). Use lowercase letters, digits, `_` and `-`, with `/` between parts, and commas between scopes: `personal,work/acme`.

Anything under `work/...` is work material. Keep it out of `NAS_JEV_SCOPES` so it never leaves the NAS.

## Passkeys

Passkeys protect the sign-in page that connects an assistant to your NAS and approves actions in your browser. Set `NAS_OAUTH_PASSKEYS` in `.env`:

- `enroll`: the default. Sign-in offers to add a passkey until you have one, then always asks for it.
- `required`: every sign-in needs a passkey.
- `off`: password only. This is the default when `NAS_OAUTH_ISSUER` is an IP address, because browsers do not offer passkeys for IP addresses.

What `enroll` does. Until the first passkey exists, every sign-in (the Claude or ChatGPT connector and browser approvals) shows an "Add a passkey (recommended)" page with a Skip button. Once one passkey exists, every sign-in asks for a passkey.

**Register a passkey right after you upgrade.** Until the first one exists, anyone who has your password can register a passkey. Open `<issuer>/passkeys`, where `<issuer>` is your `NAS_OAUTH_ISSUER` address, and add yours.

Register a passkey:

- Mac: use iCloud Keychain when the browser offers it.
- Ubuntu: use Chrome, a hardware security key, or scan the QR code with your phone.

Manage passkeys at the same address.

Check that a new passkey works: open `<issuer>/passkeys` in a private window, enter your password and use the passkey. Its "Last used" time updates. Registering alone shows "never".

Add a second passkey that does not depend on the first. A Mac passkey syncs through iCloud Keychain to your iPhone, so losing your Apple account loses both. Add one from Chrome on another computer, or a hardware security key, so you cannot lock yourself out.

Add a passkey on a device that has none, without your first passkey:

1. On the NAS, run:

   ```sh
   sudo docker exec nas-search-nas-search-1 /app/nas-search passkey-invite
   ```

   It prints an invite code that works once, for 15 minutes. A new code replaces the old one.
2. On the new device, open `<issuer>/passkeys` and enter your password.
3. At the passkey step, open "New device without a passkey?", enter the code and register the device's passkey.

The invite still needs your password, and it only adds a passkey: it never signs you in without one.

If you lose every passkey, run this on the NAS to clear them:

```sh
sudo docker exec nas-search-nas-search-1 /app/nas-search reset-passkeys
```

If you roll back to an older release, sign-in returns to password only.

## Optional: screen notes with the Jev classifier

Jev is a hosted classifier from TypeSafe that scores notes. It is optional, and notes work without it.

1. Get an API key from TypeSafe.
2. Add `NAS_JEV_API_KEY=<key>` to `.env`. The installer never prints it.
3. Choose which scopes it may see with `NAS_JEV_SCOPES` (default `personal`). Set `NAS_JEV_SCOPES=none` to send nothing to TypeSafe.

Only notes in `NAS_JEV_SCOPES` go to TypeSafe. Notes you write yourself in the editor are never sent.

Two thresholds, scored from 0 to 1000, control the result. A score at or above `NAS_MEMORY_ACCEPT_AT` (default 900) is accepted. A score at or below `NAS_MEMORY_REJECT_AT` (default 100) is rejected. Scores in between, and entries the classifier never sees, wait in the editor's Review screen for you to decide. Keep `0 <= reject < accept <= 1000`. A reject threshold of `0` is allowed.

Other settings: `NAS_JEV_URL` (default `https://api.typesafe.ai`), `NAS_JEV_MODEL` (default `jev-latest`), `NAS_EDITOR_PORT` (default 8083, range 1024 to 65535).

## Back up your memory

The memory database is the only copy of your journal, checkpoints and decisions; unlike the search index, it cannot be rebuilt from your files. Back up a consistent copy, not the live file, which a sync can catch mid-write.

1. In DSM, open Control Panel → Task Scheduler → Create → Scheduled Task → User-defined script.
2. Run it as `root`, daily, at least an hour before your offsite backup.
3. Use this script:

   ```sh
   docker exec nas-search-nas-memory-1 /app/nas-search memory-backup
   ```

Test it once by hand over SSH; it prints the new copy's path inside the container:

```sh
sudo docker exec nas-search-nas-memory-1 /app/nas-search memory-backup
```

The scheduled task runs as `root`, so it needs no `sudo`. Each run writes a copy to the `backups` folder inside your memory folder (for example `/volume1/docker/nas-search/memory/backups`) and keeps the newest 7. Point your offsite backup, such as Glacier Backup, at that `backups` folder; it appears after the first run. To keep a different number of copies, add `-e NAS_MEMORY_BACKUP_KEEP=14` after `docker exec` (1 to 90).

To restore, stop the memory container, replace `memory.sqlite3` with a backup copy, delete any `memory.sqlite3-wal` and `memory.sqlite3-shm` files beside it, and start the container again.

## Limits

- Claude does not capture your chats on its own. It writes a note only when you ask.
- Memory search matches keywords. It has no embeddings yet, so it will miss notes that use different words.
- Speed and memory use on the DS220+ are not yet measured.
- The editor's offline features and the passkey pages have not been tried in real browsers yet. Expect rough edges.
- The classifier needs internet access from the NAS. Everything else stays local.
