# Robot vacuum

Ask Claude, Codex, or ChatGPT to check on or run your Dreame robot vacuum, from home or anywhere your NAS connector already works. "Is the vacuum charged?", "Clean the kitchen and hallway twice on turbo", and "Send it back to the dock" all work.

This is off by default. It uses the open-source [Dreame vacuum library by Tasshack](https://github.com/Tasshack/dreame-vacuum) (MIT licence), pinned to a reviewed version. Home Assistant is not needed.

## What you get

- `vacuum_status`: activity, battery, docked or not, errors, suction, current room, and progress of the current job.
- `vacuum_rooms`: the rooms on the vacuum's saved map, with the IDs the assistant uses to clean them.
- `vacuum_control`: start, pause, stop, dock, locate (play a sound), or clean chosen rooms with 1-3 passes and an optional suction level.

The assistant can do nothing else: no map edits, settings changes, schedules, firmware updates, or camera access. Room cleaning only accepts rooms the vacuum itself reported.

## Turn it on

1. Add these lines to the private `.env` in the install folder, using the account you sign in to the vendor app with:

   ```
   NAS_VACUUM_ENABLED=true
   DREAME_ACCOUNT_TYPE=dreame
   DREAME_USERNAME=you@example.com
   DREAME_PASSWORD='your-app-password'
   DREAME_COUNTRY=eu
   ```

   - `DREAME_ACCOUNT_TYPE`: `dreame` for the Dreamehome app, `mova` for the MOVAhome app, or `mi` for Xiaomi Home.
   - `DREAME_COUNTRY`: the server region shown in the app (for example `eu`, `us`, `sg`, `cn`).
   - Single-quote the password if it contains `$` or spaces.
2. Upgrade through MCP (ask your assistant to run `upgrade_from_main`), or run the installer on the NAS.
3. Ask your assistant: "What's the vacuum doing?"

The installer checks the settings before changing anything, starts a small `nas-vacuum` container, and waits for it to report healthy. If it does not start, the installer rolls back to the previous release. A vacuum that is offline, or a sign-in the vendor rejects, does not roll anything back: `vacuum_status` reports it instead, and the service retries every few minutes.

To turn it off, set `NAS_VACUUM_ENABLED=false` and upgrade again. The installer stops the container.

### More than one vacuum

If the account has several vacuums, the service log names each one with its ID. Add `DREAME_DEVICE_ID=<id>` to `.env` and upgrade again. One service controls one vacuum.

## Remote access

Nothing extra to set up. The vacuum tools ride on the same authenticated MCP connection as search, so they work wherever your connector already does: on your LAN, over your personal Tailscale, or through the OAuth connector. The vacuum service publishes no ports and never sees your files; it talks to the main service only through a private socket on the NAS.

## Limits

- **Needs the vendor cloud.** Sign-in and commands go through Dreame's (or Xiaomi's) servers, as the official app does. If their service is down, so is this.
- **Xiaomi accounts with extra verification.** If Xiaomi asks for a captcha or a 2FA code on sign-in, the headless service cannot complete it. Use a Dreamehome account if your vacuum supports it.
- **Status lags a little.** The library polls the vacuum; expect up to about a minute of delay.
- **Rooms need a saved map.** Until the vacuum has mapped your home and saved the map, `vacuum_rooms` is empty and room cleaning is unavailable. Whole-home `start` still works.
- **Memory.** The library uses about 170 MB just to load; the service is capped at 384 MB (`NAS_VACUUM_MEMORY`, 256m-1024m). Real use on a DS220+ has not been measured yet. If the container restarts with out-of-memory errors in Container Manager, raise the cap to `512m`.
- **Credentials.** Your vendor password sits in the private `.env` (root-only, never in source control), like the other NAS secrets.
- **Untested models.** It supports what the pinned library supports. A model the library does not recognise shows as "no supported vacuum found" in the log.

## Troubleshooting

Open Container Manager, select `nas-search-nas-vacuum-1`, and read the log:

| Log says | Fix |
|---|---|
| `login failed` | Check username, password, account type, and region in `.env`. |
| `several vacuums found` | Add `DREAME_DEVICE_ID` with one of the listed IDs. |
| `no supported vacuum found` | Check you used the account that owns the vacuum, and the right account type. |
| `connection failed, retrying` | Temporary network or vendor issue; it retries on its own. |
