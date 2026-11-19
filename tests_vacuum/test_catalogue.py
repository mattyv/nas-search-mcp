import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('vacuum_catalogue', ROOT / 'vacuum/catalogue.py')
catalogue = importlib.util.module_from_spec(spec)
spec.loader.exec_module(catalogue)
FIXTURES = Path(__file__).resolve().parent / 'fixtures'


def entries():
    return {e['name']: e for e in catalogue.load(FIXTURES)}


def test_reads_each_settings_table_and_skips_room_level_ones():
    found = entries()
    assert set(found) == {'child_lock', 'stain_avoidance', 'carpet_avoidance', 'ai_pet_detection',
                          'mop_pressure', 'battery_charge_level', 'mop_cleaning_remainder', 'self_clean_time',
                          'off_peak_charging_start'}
    assert 'ignored_segment_select' not in found


def test_records_kind_family_and_property():
    found = entries()
    assert found['child_lock'] == {'name': 'child_lock', 'kind': 'switch', 'family': 'property', 'prop': 'CHILD_LOCK'}
    assert found['stain_avoidance']['family'] == 'auto_switch'
    assert found['ai_pet_detection']['family'] == 'ai'
    assert found['carpet_avoidance'] == {'name': 'carpet_avoidance', 'kind': 'switch', 'family': None, 'prop': None}


def test_records_literal_options_and_number_ranges():
    found = entries()
    assert found['battery_charge_level']['options'] == ['80%', '90%', '100%']
    assert 'options' not in found['mop_pressure']
    assert found['mop_cleaning_remainder']['range'] == [0, 180, 15]
    assert found['self_clean_time']['range'] == [None, None, 5]


def test_missing_folder_gives_empty_catalogue(tmp_path):
    assert catalogue.load(tmp_path) == []


def test_reads_buttons_with_their_library_call():
    found = {b['name']: b for b in catalogue.load_buttons(FIXTURES)}
    assert found['reset_main_brush'] == {'name': 'reset_main_brush', 'action': 'RESET_MAIN_BRUSH', 'call': None}
    assert found['clear_warning'] == {'name': 'clear_warning', 'action': 'CLEAR_WARNING', 'call': ['clear_warning', []]}
    assert found['start_mapping'] == {'name': 'start_mapping', 'action': None, 'call': ['start_mapping', []]}
    assert found['empty_water_tank']['call'] == ['start_draining', [True]]
    assert 'shortcut' not in found
