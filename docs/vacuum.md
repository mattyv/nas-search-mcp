# Robot vacuum

See [vacuum coverage](vacuum-coverage.md) for everything the original integration offers and what is available here.

Ask Claude, Codex, or ChatGPT to check on or run your Dreame robot vacuum, from home or anywhere your NAS connector already works. "Is the vacuum charged?", "Clean the kitchen and hallway twice on turbo", and "Send it back to the dock" all work.

This is off by default. It uses the open-source [Dreame vacuum library by Tasshack](https://github.com/Tasshack/dreame-vacuum) (MIT licence), pinned to a reviewed version. Home Assistant is not needed.

## What you get

Looking (never moves the vacuum):

- `vacuum_status`: activity, battery, docked or not, errors, suction, current room, and progress of the current job.
- `vacuum_rooms`: the rooms on the saved map with their IDs, where each room sits, and the map's edges.
- `vacuum_details`: how worn the brushes, filter, mop pads and other parts are; the base station (water tanks, dust bag, detergent, washing, drying); and current settings.
- `vacuum_history`: the most recent cleaning runs, with time, minutes, area, and whether they finished.
- `vacuum_map`: a picture of the map with rooms, the vacuum and its dock.

Doing (only when you ask):

- `vacuum_control`: start, pause, stop, dock, locate (play a sound); clean chosen rooms, up to five rectangles on the map, or one spot, each with 1-3 passes and an optional suction level; empty the dustbin; wash, dry, or stop drying the mops.
- `vacuum_control` `go_to`: drive to a point on the map (for example the middle of a room) and stop there, without cleaning.
- `vacuum_drive`: one small remote-control step (turn and roll), like the app's joystick. Each call is a short nudge, a few seconds apart, and the vacuum's own bump and cliff sensors still apply.
- `vacuum_shortcuts` and `vacuum_shortcut`: list, run, rename or delete the cleaning shortcuts saved in the Dreamehome app.
- `vacuum_settings`: the everyday settings by friendly name: cleaning mode, suction, mop wetness, volume, do-not-disturb, child lock, carpet handling, and per-room mode, suction, wetness and passes.
- `vacuum_options` and `vacuum_set_option`: every other setting your model has, as in the app (about 100: carpet boost, AI detection, hot washing, mop pressure, water temperature, cleaning route, drying time, off-peak charging and more), with current values and allowed choices.
- `vacuum_buttons` and `vacuum_press`: one-press actions: reset a part's wear counter after replacing it, clear a warning, drain or empty the water tank, base self-clean or self-repair, dust-bag drying, start mapping, re-clean missed areas, back up the map.

Maps and rooms (only when you ask):

- `vacuum_maps` and `vacuum_manage_map`: list maps and their backups; switch, rename, delete, back up, restore, refresh, and save, discard or keep a freshly scanned map.
- `vacuum_manage_rooms`: rename rooms, set a room's type, merge two rooms, split a room along a line, set the cleaning order, hide rooms.
- `vacuum_room_options` and `vacuum_set_room_option`: each room's own settings (suction, water, mode, passes, routes, mop pressure and temperature, mop type, floor material).
- `vacuum_map_elements` and `vacuum_edit_map`: see and change no-go and no-mop zones, virtual walls, thresholds, ramps, curtains, low-lying areas, carpet areas, saved points and the router position.
- `vacuum_patrol`: drive along a path of up to 20 points without cleaning.

Camera (on by default; set `NAS_VACUUM_CAMERA=false` in `.env` to hide these three):

- `vacuum_obstacles`: things the vacuum recognised and avoided on its current map (socks, cables, shoes, pet mess), with where they are and whether it took a photo.
- `vacuum_obstacle_photo`: shows one of those photos. View only; nothing is saved.
- `vacuum_ignore_obstacle`: stop avoiding one of them (for example a rug edge it mistakes for a cable), or avoid it again.

The vacuum takes these photos automatically while cleaning. It cannot yet take a photo on request, and there is no live video: the library does not support either. So "go to the hallway and tell me what you see" works only as far as driving there.

The assistant cannot change schedules, update firmware, place furniture, edit structural walls and doors, or set carpet types; see [coverage](vacuum-coverage.md) for why. Everything it aims, from cleaning zones to no-go zones and patrol points, must lie inside the saved map, and only rooms, maps, shortcuts, settings and buttons the vacuum reported are accepted, with allowed values. A feature your model lacks is refused with the vacuum's own message.

Try: "Run the after-dinner shortcut", "Add a no-go zone around the dog bowl", "Turn on carpet boost", "I replaced the filter", "Mop the bathroom with strong pressure", "Drive the vacuum to the kitchen", "What did the vacuum avoid today? Show me", "How worn are the vacuum's brushes?", "Show me the vacuum map", "Clean the area in front of the sofa", "Mop the kitchen only, wet", "Turn on do-not-disturb from 22:00 to 08:00".

## Turn it on

You need SSH access to the NAS (DSM: Control Panel → Terminal & SNMP → Enable SSH service). Find the install folder, which holds the private `.env`:

```sh
sudo docker inspect nas-search-nas-search-1 --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}'
```

The examples below use `/volume1/@nas-search-upgrader`; replace it with the folder that command printed.

1. Save the password you use in the vendor app in its own file. Typing it at the prompt keeps it out of your shell history:

   ```sh
   cd /volume1/@nas-search-upgrader
   sudo bash -c 'umask 077; IFS= read -r -s -p "Vacuum app password: " pw; echo; printf "%s\n" "$pw" > dreame_password'
   ```

2. Add these lines to the `.env` in the same folder (`sudo vi .env`), using the account you sign in to the vendor app with:

   ```
   NAS_VACUUM_ENABLED=true
   DREAME_ACCOUNT_TYPE=dreame
   DREAME_USERNAME=you@example.com
   DREAME_COUNTRY=eu
   ```

   - `DREAME_ACCOUNT_TYPE`: `dreame` for the Dreamehome app, `mova` for the MOVAhome app, or `mi` for Xiaomi Home.
   - `DREAME_COUNTRY`: the server region of your account (`eu`, `us`, `sg`, `cn`, `ru`, `kr`). It is where the account was created, not where you live now. Accounts from Australia and most of Asia use `sg`.
   - Do not put the password in `.env`; the installer refuses it there.
3. Upgrade through MCP (ask your assistant to run `upgrade_from_main`), or run the installer on the NAS.
4. Ask your assistant: "What's the vacuum doing?"

The installer checks the settings before changing anything, starts a small `nas-vacuum` container, and waits for it to report healthy. If it does not start, the installer rolls back to the previous release. A vacuum that is offline, or a sign-in the vendor rejects, does not roll anything back: `vacuum_status` reports it instead, and the service retries every few minutes.

To turn it off, set `NAS_VACUUM_ENABLED=false` and upgrade again. The installer stops the container.

To change the password later, repeat step 1 and upgrade again.

### More than one vacuum

If the account has several vacuums, the service log names each one with its ID. Add `DREAME_DEVICE_ID=<id>` to `.env` and upgrade again. One service controls one vacuum.

## Remote access

Nothing extra to set up. The vacuum tools ride on the same authenticated MCP connection as search, so they work wherever your connector already does: on your LAN, over your personal Tailscale, or through the OAuth connector. The vacuum service publishes no ports and never sees your files; it talks to the main service only through a private socket on the NAS.

## Saved places

Name spots on the map once, then use the names: "go to the front door", "spot-clean under the couch".

- Drive the vacuum to the spot, then say "save this spot as Front door". Or give a point from the map.
- "What places have I saved?" lists them; "forget Couch" removes one.
- Up to 30 places. They live on the NAS (in the index folder), not in the Dreame app, and stay put across upgrades. If you rebuild the map, re-save them, since old points may land somewhere else.

## Phone notifications

Get a push on your phone when the vacuum finishes, gets stuck, or a part (filter, brush, mop pad) drops below 10% life.

1. Install the free **ntfy** app (iPhone or Android).
2. Make up a long random topic name, for example run `openssl rand -hex 12` on the NAS and put `roberto-` in front.
3. In the app, tap **+** and subscribe to that topic on `ntfy.sh`.
4. Add `NAS_VACUUM_NTFY_TOPIC=<your topic>` to `.env` and upgrade.

The NAS checks the vacuum every 30 seconds and sends each event once. Anyone who knows the topic name can read the notices, so keep it long and private. Notices go through the public ntfy.sh service; to keep them in-house, run your own ntfy server and set `NAS_VACUUM_NTFY_URL`.

## Limits

- **Needs the vendor cloud.** Sign-in and commands go through Dreame's (or Xiaomi's) servers, as the official app does. If their service is down, so is this.
- **Xiaomi accounts with extra verification.** If Xiaomi asks for a captcha or a 2FA code on sign-in, the headless service cannot complete it. Use a Dreamehome account if your vacuum supports it.
- **Status lags a little.** The library polls the vacuum; expect up to about a minute of delay.
- **Rooms need a saved map.** Until the vacuum has mapped your home and saved the map, `vacuum_rooms` is empty and room, zone and spot cleaning are unavailable. Whole-home `start` still works.
- **Zones and spots are aimed by coordinates.** The assistant works them out from room positions and the map picture, so describe the area clearly ("the third of the living room nearest the window"). Check the result with `vacuum_map` the first few times.
- **Mop wetness is approximate.** Models with a fine wetness scale get dry, moist and wet mapped to roughly 20%, 50% and 85% of their range.
- **Schedules stay in the app.** The library cannot create schedules.
- **History depends on the vendor cloud** and may be empty until the vacuum has finished a run since the service started.
- **Memory.** The library uses about 170 MB just to load, and drawing the map picture adds more; the service is capped at 384 MB (`NAS_VACUUM_MEMORY`, 256m-1024m). Real use on a DS220+ has not been measured yet. If the container restarts with out-of-memory errors in Container Manager, raise the cap to `512m`.
- **Password storage.** The vendor password lives only in the `dreame_password` file. The installer makes it readable by the service's user alone and mounts it into the vacuum container only, so it does not appear in `.env`, in container settings, or in `docker inspect`. Anyone with full admin access to the NAS can still read it. Use a password you use nowhere else, so the worst case is someone controlling your vacuum. To change it, rewrite the file (step 1) and upgrade again.
- **Untested models.** It supports what the pinned library supports. A model the library does not recognise shows as "no supported vacuum found" in the log.

## Troubleshooting

Ask your assistant for the vacuum status. When the vacuum is unavailable, the answer ends with the reason in brackets, such as a failed sign-in or a vacuum that did not respond. For more detail, open Container Manager, select `nas-search-nas-vacuum-1`, and read the log:

| Log says | Fix |
|---|---|
| `rejected the sign-in` / `rejected the password` | The service stops trying, because Dreame locks the account after five wrong passwords. Fix the username, region or `dreame_password` file (see below), then restart the vacuum service. |
| `could not reach the Dreame cloud` | Network or Dreame outage; it retries on its own. |
| `several vacuums found` | Add `DREAME_DEVICE_ID` with one of the listed IDs. |
| `no supported vacuum found` | Check you used the account that owns the vacuum, and the right account type. |
| `connection failed, retrying` | Temporary network or vendor issue; it retries on its own. |
| `vacuum did not respond` | Signed in, but the vacuum is off, asleep without Wi-Fi, or out of range. Check it in the Dreamehome app. |

### Find your account's region

Dreame keeps each account on one regional server, which is not always where you live. This tries each region with your saved details and prints which one signs in. It never shows the password. Every try may count as a failed sign-in on that region's server, and Dreame locks an account after five, so run it once, and if every region fails, check the password in the app before running it again.

```sh
cd /volume1/@nas-search-upgrader
sudo docker run --rm --entrypoint /opt/vacuum-venv/bin/python \
  -v "$PWD/dreame_password:/pw:ro" \
  -e U="$(sudo sed -n 's/^DREAME_USERNAME=//p' .env | tr -d "'\"")" \
  "$(cat current-image)" -I -W ignore -c '
import os, sys, logging; logging.disable(logging.CRITICAL); sys.path.insert(0, "/opt/dreame")
from dreame import DreameVacuumProtocol
pw = open("/pw").read().rstrip("\r\n"); u = os.environ["U"]
for c in ["sg", "eu", "us", "cn", "ru", "kr"]:
    p = DreameVacuumProtocol(username=u, password=pw, country=c, prefer_cloud=True, account_type="dreame")
    p.cloud.login(); print(c, "SIGNED IN" if p.cloud.logged_in else "no")
'
```

### Restart only the vacuum service

After changing `DREAME_` settings or the password file:

```sh
cd /volume1/@nas-search-upgrader
sudo docker compose --profile vacuum --env-file .env --project-name nas-search --project-directory . -f compose.yaml up -d --no-build --force-recreate nas-vacuum
```

Search and the rest of the NAS keep running.
