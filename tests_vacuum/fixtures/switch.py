# Trimmed from the original integration's switch.py for catalogue tests.
SWITCHES: tuple[DreameVacuumSwitchEntityDescription, ...] = (
    DreameVacuumSwitchEntityDescription(
        property_key=DreameVacuumProperty.CHILD_LOCK,
        icon_fn=lambda value, device: "mdi:lock-off" if value == 0 else "mdi:lock",
    ),
    DreameVacuumSwitchEntityDescription(
        property_key=DreameVacuumAutoSwitchProperty.STAIN_AVOIDANCE,
        format_fn=lambda value, device: 2 if value else 1,
    ),
    DreameVacuumSwitchEntityDescription(
        key="carpet_avoidance",
        icon="mdi:rug",
    ),
    DreameVacuumSwitchEntityDescription(
        property_key=DreameVacuumAIProperty.AI_PET_DETECTION,
    ),
)
