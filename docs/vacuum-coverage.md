# Vacuum coverage

What the original Dreame integration (Tasshack/dreame-vacuum, pinned commit) offers, and what your assistants can do through the NAS. Every command and setting in the original is listed here, so nothing is missed silently.

**Status key:** ✅ available · ⛔ left out, with the reason

## Commands (the original's 45 services)

| Original | Status | How to ask |
|---|---|---|
| Clean rooms (`clean_segment`) | ✅ | "Clean the kitchen twice on turbo" |
| Clean zones (`clean_zone`) | ✅ | "Clean the area in front of the couch" |
| Clean spot (`clean_spot`) | ✅ | "Spot-clean by the front door" |
| Go to point (`goto`) | ✅ | "Drive to the secondary bedroom" |
| Remote control step (`remote_control_move_step`) | ✅ | "Nudge forward a bit, turn left" |
| Start shortcut (`start_shortcut`) | ✅ | "Run the after-dinner shortcut" |
| Rename shortcut (`rename_shortcut`) | ✅ | "Rename that shortcut to Evening tidy" |
| Delete shortcut (`delete_shortcut`) | ✅ | "Delete the old bathroom shortcut" |
| Reset consumable (`reset_consumable`) | ✅ | "I replaced the filter, reset it" |
| Follow path (`follow_path`) | ✅ | "Patrol hallway, kitchen, front door" |
| Request map (`request_map`) | ✅ | |
| Select map (`select_map`) | ✅ | "Switch to the upstairs map" |
| Rename map (`rename_map`) | ✅ | |
| Delete map (`delete_map`) | ✅ | |
| Backup map (`backup_map`) | ✅ | |
| Restore map (`restore_map`) | ✅ | |
| Save / discard / replace temporary map | ✅ | |
| Rename room (`rename_segment`) | ✅ | |
| Set room type (`set_segment_type`) | ✅ | |
| Cleaning order (`set_cleaning_sequence`) | ✅ | |
| Hidden rooms (`set_hidden_segments`) | ✅ | |
| Merge rooms (`merge_segments`) | ✅ | |
| Split room (`split_segments`) | ✅ | |
| Per-room custom cleaning (`set_custom_cleaning`) | ✅ | "Mop the kitchen with strong pressure" (every per-room setting) |
| Custom carpet cleaning (`set_custom_carpet_cleaning`) | ⛔ | Uses undocumented carpet object and cleaning codes; set it in the app |
| Floor material (`set_floor_material`) | ✅ | |
| Mop type (`set_mop_type`) | ✅ | |
| Ignore obstacle (`set_obstacle_ignore`) | ✅ | "Stop avoiding that shoe" |
| No-go and no-mop zones (`set_restricted_zone`) | ✅ | |
| Virtual walls (`set_walls`) | ✅ | |
| Thresholds (`set_threshold`, `set_virtual_threshold`) | ✅ | |
| Carpet areas (`set_carpet_area`) | ✅ | "Mark a carpet in the living room" |
| Carpet types (`set_carpet_type`) | ⛔ | Uses undocumented carpet object and type codes; set it in the app |
| Curtains, low-lying areas | ✅ | |
| Furniture placement (`set_furniture`) | ⛔ | Each item takes 11 undocumented numbers; guessing could corrupt the map. Use the app |
| Structural walls and doors (`set_walls`) | ⛔ | Same reason; these rebuild the map. Use the app |
| Predefined points (`set_predefined_points`) | ✅ | |
| Router position (`set_router_position`) | ✅ | |
| Install voice pack (`install_voice_pack`) | ⛔ | Needs a voice file hosted on the network for the vacuum to download; no such host here |
| Restore map from file (`restore_map_from_file`) | ⛔ | Reads a file on the Home Assistant computer; there is none |
| Raw property / action (`set_property`, `call_action`) | ⛔ | Sends any vendor command unchecked, bypassing every safety rule |

## Settings and buttons (the original's entities)

| Original | Status |
|---|---|
| Status, battery, errors, current room, job progress | ✅ |
| Consumable wear (brushes, filter, mop pads, and so on) | ✅ |
| Base station state, cleaning history, map picture | ✅ |
| Obstacle list and photos | ✅ (hide with `NAS_VACUUM_CAMERA=false`) |
| Cleaning mode, suction, mop wetness, volume, do-not-disturb, child lock, carpet avoidance and recognition | ✅ |
| Base station: empty dustbin, wash mops, dry mops, stop drying | ✅ |
| Every on/off, choice, number and time setting (118, read from the original's own tables) | ✅ |
| Every per-room setting (14) | ✅ |
| Every button (31), including wear-counter resets | ✅ |
| Live video, on-demand photo | ⛔ Not in the original either |

Settings, room settings and buttons are read from the pinned integration's own tables, shipped in the NAS image, so this list follows the library version. The vacuum reports which of them your model has; the verified safety rules accept only those, with allowed values.
