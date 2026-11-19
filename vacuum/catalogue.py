"""Read the original integration's settings tables without importing Home Assistant.

The pinned Dreame integration describes every vacuum-wide setting in its
switch, select, number and time platform files. Parsing them with ``ast`` keeps
the NAS's settings list exactly in step with the pinned library version.
"""

import ast
from pathlib import Path

TABLES = {'switch': 'SWITCHES', 'select': 'SELECTS', 'number': 'NUMBERS', 'time': 'TIMES'}
ROOM_TABLES = {'select': 'SEGMENT_SELECTS', 'number': 'SEGMENT_NUMBERS'}
FAMILIES = {
    'DreameVacuumProperty': 'property',
    'DreameVacuumAutoSwitchProperty': 'auto_switch',
    'DreameVacuumAIProperty': 'ai',
    'DreameVacuumStrAIProperty': 'ai',
}


def _literal_int(node):
    try:
        value = ast.literal_eval(node)
    except (ValueError, SyntaxError):
        return None
    return value if isinstance(value, int) and not isinstance(value, bool) else None


def _literal_options(node):
    """Options written as `lambda entity: ["a", "b"]`; anything computed is read from the vacuum instead."""
    if isinstance(node, ast.Lambda):
        try:
            value = ast.literal_eval(node.body)
        except (ValueError, SyntaxError):
            return None
        if isinstance(value, list) and all(isinstance(v, str) for v in value):
            return value
    return None


def _entry(kind, call):
    keywords = {k.arg: k.value for k in call.keywords}
    family = prop = name = None
    key = keywords.get('property_key')
    if isinstance(key, ast.Attribute) and isinstance(key.value, ast.Name) and key.value.id in FAMILIES:
        family, prop = FAMILIES[key.value.id], key.attr
        name = prop.lower()
    if 'key' in keywords:
        node = keywords['key']
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            name = node.value
        elif (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr == 'lower'
              and isinstance(node.func.value, ast.Attribute) and node.func.value.attr == 'name'
              and isinstance(node.func.value.value, ast.Attribute)):
            # key=DreameVacuumProperty.X.name.lower()
            name = node.func.value.value.attr.lower()
    if not name:
        return None
    entry = {'name': name, 'kind': kind, 'family': family, 'prop': prop}
    if kind == 'select' and 'options' in keywords:
        options = _literal_options(keywords['options'])
        if options:
            entry['options'] = options
    if kind == 'number':
        entry['range'] = [_literal_int(keywords['native_min_value']) if 'native_min_value' in keywords else None,
                          _literal_int(keywords['native_max_value']) if 'native_max_value' in keywords else None,
                          _literal_int(keywords['native_step']) if 'native_step' in keywords else 1]
    return entry


def load(folder, rooms=False):
    """Return the vacuum-wide (or, with rooms=True, per-room) settings in the platform files."""
    entries, seen = [], set()
    for kind, table in (ROOM_TABLES if rooms else TABLES).items():
        path = Path(folder) / f'{kind}.py'
        if not path.is_file():
            continue
        tree = ast.parse(path.read_text(encoding='utf-8'))
        for node in tree.body:
            target = node.target if isinstance(node, ast.AnnAssign) else (
                node.targets[0] if isinstance(node, ast.Assign) and len(node.targets) == 1 else None)
            if getattr(target, 'id', None) != table or not isinstance(node.value, ast.Tuple):
                continue
            for call in node.value.elts:
                if not isinstance(call, ast.Call):
                    continue
                entry = _entry(kind, call)
                if entry and entry['name'] not in seen:
                    seen.add(entry['name'])
                    entries.append(entry)
    return entries


def _device_call(node):
    """`lambda device: device.method(literal, ...)` -> [method, [literals]]; anything else -> None."""
    if not (isinstance(node, ast.Lambda) and isinstance(node.body, ast.Call)):
        return None
    func = node.body.func
    if not (isinstance(func, ast.Attribute) and isinstance(func.value, ast.Name) and func.value.id == 'device'):
        return None
    try:
        args = [ast.literal_eval(arg) for arg in node.body.args]
    except (ValueError, SyntaxError):
        return None
    return [func.attr, args]


def load_buttons(folder):
    """Return the original's buttons: a library action, a device method call, or both."""
    path = Path(folder) / 'button.py'
    if not path.is_file():
        return []
    buttons, seen = [], set()
    for node in ast.parse(path.read_text(encoding='utf-8')).body:
        target = node.target if isinstance(node, ast.AnnAssign) else (
            node.targets[0] if isinstance(node, ast.Assign) and len(node.targets) == 1 else None)
        if getattr(target, 'id', None) != 'BUTTONS' or not isinstance(node.value, ast.Tuple):
            continue
        for call in node.value.elts:
            if not isinstance(call, ast.Call):
                continue
            keywords = {k.arg: k.value for k in call.keywords}
            action = keywords.get('action_key')
            action = action.attr if isinstance(action, ast.Attribute) else None
            device_call = _device_call(keywords['action_fn']) if 'action_fn' in keywords else None
            key = keywords.get('key')
            name = key.value if isinstance(key, ast.Constant) and isinstance(key.value, str) else (
                action.lower() if action else None)
            if not name or name in seen or (action is None and device_call is None):
                continue
            seen.add(name)
            buttons.append({'name': name, 'action': action, 'call': device_call})
    return buttons
