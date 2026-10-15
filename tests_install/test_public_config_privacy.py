from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_public_config_uses_generic_example_sources_and_uid():
    example = (ROOT / '.env.example').read_text()
    compose = (ROOT / 'compose.yaml').read_text()
    assert 'NAS_DOCUMENTS_PATH=/volume1/shared-documents' in example
    assert 'NAS_PHOTOS_PATH=/volume1/shared-photos' in example
    assert 'NAS_UID=1000' in example
    assert '${NAS_DOCUMENTS_LABEL:-Documents}' in compose
    assert '${NAS_UID:-1000}:${NAS_GID:-1000}' in compose
