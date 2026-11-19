# Vacuum coverage

What the original Dreame integration (Tasshack/dreame-vacuum, pinned commit) offers, and what your assistants can do through the NAS. Every command and setting in the original is listed here, so nothing is missed silently.

**Status key:** ✅ available · 🔜 planned (batch named) · ⛔ left out, with the reason

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
| Reset consumable (`reset_consumable`) | 🔜 B | "I replaced the filter, reset it" |
| Follow path (`follow_path`) | 🔜 D | "Patrol hallway, kitchen, front door" |
| Request map (`request_map`) | 🔜 C | |
| Select map (`select_map`) | 🔜 C | "Switch to the upstairs map" |
| Rename map (`rename_map`) | 🔜 C | |
| Delete map (`delete_map`) | 🔜 C | |
| Backup map (`backup_map`) | 🔜 C | |
| Restore map (`restore_map`) | 🔜 C | |
| Save / discard / replace temporary map | 🔜 C | |
| Rename room (`rename_segment`) | 🔜 C | |
| Set room type (`set_segment_type`) | 🔜 C | |
| Cleaning order (`set_cleaning_sequence`) | 🔜 C | |
| Hidden rooms (`set_hidden_segments`) | 🔜 C | |
| Merge rooms (`merge_segments`) | 🔜 C | |
| Split room (`split_segments`) | 🔜 C | |
| Per-room custom cleaning (`set_custom_cleaning`) | ✅ partly | Room suction, wetness, passes and mode work today; the rest in 🔜 C |
| Custom carpet cleaning (`set_custom_carpet_cleaning`) | 🔜 C | |
| Floor material (`set_floor_material`) | 🔜 C | |
| Mop type (`set_mop_type`) | 🔜 C | |
| Ignore obstacle (`set_obstacle_ignore`) | 🔜 D | "Stop avoiding that shoe" |
| No-go and no-mop zones (`set_restricted_zone`) | 🔜 D | |
| Virtual walls (`set_walls`) | 🔜 D | |
| Thresholds (`set_threshold`, `set_virtual_threshold`) | 🔜 D | |
| Carpet areas and types (`set_carpet_area`, `set_carpet_type`) | 🔜 D | |
| Furniture, curtains, low-lying areas | 🔜 D | |
| Predefined points (`set_predefined_points`) | 🔜 D | |
| Router position (`set_router_position`) | 🔜 D | |
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
| All other on/off settings (about 70, such as carpet boost, AI pet detection, hot washing, resume cleaning) | 🔜 B |
| All other choice settings (about 30, such as mop pressure, water temperature, cleaning route, drying time) | 🔜 B |
| Number and time settings (drying time, off-peak charging hours, camera light, and so on) | 🔜 B |
| Other buttons: clear warning, drain or empty water tank, base self-clean and self-repair, dust-bag drying, start mapping, fast mapping, re-clean | 🔜 B |
| Live video, on-demand photo | ⛔ Not in the original either |

Batch B adds settings generically: the vacuum reports which settings your model has, with current values and allowed choices, and the assistant can change any of them. The verified safety rules accept only a setting the vacuum listed, with a value it allows.
