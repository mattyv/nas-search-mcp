SELECTS: tuple[DreameVacuumSelectEntityDescription, ...] = (
    DreameVacuumSelectEntityDescription(
        property_key=DreameVacuumProperty.MOP_PRESSURE,
        value_int_fn=lambda value, entity: DreameVacuumMopPressure[value.upper()].value,
    ),
    DreameVacuumSelectEntityDescription(
        property_key=DreameVacuumProperty.BATTERY_CHARGE_LEVEL,
        options=lambda entity: ["80%", "90%", "100%"],
        value_int_fn=lambda value, entity: int(value[:-1]),
    ),
)
SEGMENT_SELECTS: tuple[DreameVacuumSelectEntityDescription, ...] = (
    DreameVacuumSelectEntityDescription(key="ignored_segment_select"),
    DreameVacuumSelectEntityDescription(key=DreameVacuumProperty.MOP_PRESSURE.name.lower()),
    DreameVacuumSelectEntityDescription(key="cleaning_times", options=lambda entity: [f"{i}x" for i in range(1, 4)]),
    DreameVacuumSelectEntityDescription(key="mop_type", options=lambda entity: ["a", "b", "c"]),
    DreameVacuumSelectEntityDescription(key="order"),
    DreameVacuumSelectEntityDescription(key="name"),
)
