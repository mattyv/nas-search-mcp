NUMBERS: tuple[DreameVacuumNumberEntityDescription, ...] = (
    DreameVacuumNumberEntityDescription(
        property_key=DreameVacuumProperty.MOP_CLEANING_REMAINDER,
        native_min_value=0,
        native_max_value=180,
        native_step=15,
    ),
    DreameVacuumNumberEntityDescription(
        key="self_clean_time",
        min_value_fn=lambda device: device.status.self_clean_time_min,
        max_value_fn=lambda device: device.status.self_clean_time_max,
        native_step=5,
    ),
)
SEGMENT_NUMBERS: tuple[DreameVacuumNumberEntityDescription, ...] = (
    DreameVacuumNumberEntityDescription(key=DreameVacuumProperty.WETNESS_LEVEL.name.lower(), native_min_value=1),
)
