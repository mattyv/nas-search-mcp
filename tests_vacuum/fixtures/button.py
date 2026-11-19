BUTTONS: tuple[ButtonEntityDescription, ...] = (
    DreameVacuumButtonEntityDescription(
        action_key=DreameVacuumAction.RESET_MAIN_BRUSH,
        exists_fn=lambda description, device: True,
    ),
    DreameVacuumButtonEntityDescription(
        action_key=DreameVacuumAction.CLEAR_WARNING,
        action_fn=lambda device: device.clear_warning(),
    ),
    DreameVacuumButtonEntityDescription(
        key="start_mapping",
        action_fn=lambda device: device.start_mapping(),
    ),
    DreameVacuumButtonEntityDescription(
        key="empty_water_tank",
        action_fn=lambda device: device.start_draining(True),
    ),
    DreameVacuumButtonEntityDescription(
        key="shortcut",
    ),
)
