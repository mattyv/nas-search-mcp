import importlib.util
import json
import subprocess
import sys
from pathlib import Path

import pytest
from PIL import Image
from docx import Document
from pypdf import PdfWriter

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('extraction_worker', ROOT / 'worker/worker.py')
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


def extract(path, **kw):
    return worker.handle({'operation': 'extract', 'path': str(path), 'max_bytes': 100000, 'max_chars': 1000, **kw})


def test_text_bounds(tmp_path):
    p = tmp_path / 'code.py'
    p.write_text('hello world')
    result = extract(p, max_chars=5)
    assert result['sections'] == [{'location': 'text', 'text': 'hello'}]
    assert result['truncated'] is True
    assert result['kind'] == 'document'
    with pytest.raises(ValueError, match='max_bytes'):
        extract(p, max_bytes=2)


def test_docx(tmp_path):
    p = tmp_path / 'test.docx'
    doc = Document()
    doc.add_paragraph('Alpha')
    doc.add_table(rows=1, cols=1).cell(0, 0).text = 'Beta'
    doc.save(p)
    assert 'Alpha' in str(extract(p)['sections'])
    assert 'Beta' in str(extract(p)['sections'])


def test_pdf_page_location(tmp_path):
    p = tmp_path / 'test.pdf'
    writer = PdfWriter()
    writer.add_blank_page(width=100, height=100)
    writer.write(p)
    result = extract(p)
    assert result['kind'] == 'document'
    assert result['metadata']['pages'] == 1
    assert result['metadata']['needs_ocr'] is True


def test_photo_and_unsupported(tmp_path):
    p = tmp_path / 'test.png'
    Image.new('RGB', (5, 7)).save(p)
    result = extract(p)
    assert result['kind'] == 'photo'
    assert result['metadata']['width'] == 5
    assert result['sections'] == []
    p = tmp_path / 'backup.zip'
    p.write_bytes(b'x')
    with pytest.raises(ValueError, match='unsupported'):
        extract(p)


def test_embedding_models_and_dimensions(monkeypatch, tmp_path):
    image_path = tmp_path / 'model.jpg'
    Image.new('RGB', (5, 7)).save(image_path)
    calls = []
    class FakeModel:
        def __init__(self, **kwargs):
            calls.append(kwargs)
            self.dimension = 512 if 'clip' in kwargs['model_name'] else 384
        def embed(self, values, **kwargs):
            calls.append((list(values), kwargs))
            return [[0.1] * self.dimension]
    monkeypatch.setattr(worker, 'load_embedding_classes', lambda: (FakeModel, FakeModel))
    for operation, payload, dimension in [('embed', {'texts': ['hello']}, 384), ('image_query', {'texts': ['cat']}, 512), ('image_embed', {'path': str(image_path)}, 512)]:
        result = worker.handle({'operation': operation, **payload})
        assert len(result['vectors'][0]) == dimension
    assert all(c['threads'] == 1 for c in calls if isinstance(c, dict))
    assert all(c['providers'] == ['CPUExecutionProvider'] for c in calls if isinstance(c, dict))


def test_json_protocol_error():
    result = subprocess.run([sys.executable, str(ROOT / 'worker/worker.py')], input=json.dumps({'operation': 'bogus'}), capture_output=True, text=True)
    assert result.returncode == 1
    assert 'error' in json.loads(result.stdout)


def test_spreadsheet_and_presentation(tmp_path):
    from openpyxl import Workbook
    from pptx import Presentation
    from pptx.util import Inches
    p = tmp_path / 'book.xlsx'
    book = Workbook()
    book.active.append(['Heading', 42])
    book.save(p)
    sections = extract(p)['sections']
    assert sections == [{'location': 'sheet:Sheet:row:1', 'text': 'Heading\t42'}]
    p = tmp_path / 'slides.pptx'
    deck = Presentation()
    slide = deck.slides.add_slide(deck.slide_layouts[6])
    slide.shapes.add_textbox(Inches(1), Inches(1), Inches(3), Inches(1)).text = 'Slide content'
    deck.save(p)
    assert extract(p)['sections'] == [{'location': 'slide:1', 'text': 'Slide content'}]


def test_html_hides_scripts_and_byte_limit(tmp_path):
    p = tmp_path / 'page.html'
    p.write_text('<p>Visible</p><script>secret()</script><style>hidden</style>')
    result = extract(p)
    assert result['sections'] == [{'location': 'html', 'text': 'Visible'}]
    with pytest.raises(ValueError, match='positive'):
        extract(p, max_chars=0)


def test_embedding_input_limit(monkeypatch):
    monkeypatch.setattr(worker, 'load_embedding_classes', lambda: (_ for _ in ()).throw(AssertionError('models must not load for invalid request')))
    with pytest.raises(ValueError, match='1 to 32'):
        worker.handle({'operation': 'embed', 'texts': []})
    with pytest.raises(ValueError, match='character limit'):
        worker.handle({'operation': 'embed', 'texts': ['x' * 200001]})
