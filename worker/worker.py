#!/usr/bin/env python3
"""One-request extraction/model boundary. Caller authorizes paths before invocation."""
import contextlib
import json
import os
from pathlib import Path
import sys

TEXT_EXTENSIONS = {'.txt', '.md', '.rst', '.csv', '.tsv', '.json', '.jsonl', '.xml', '.yaml', '.yml', '.toml', '.ini', '.cfg', '.log', '.py', '.go', '.rs', '.java', '.js', '.jsx', '.ts', '.tsx', '.c', '.h', '.cpp', '.hpp', '.cs', '.sh', '.bash', '.sql', '.dfy', '.css', '.scss', '.vue', '.svelte', '.rb', '.php', '.swift', '.kt', '.kts', '.r', '.tex'}
IMAGE_EXTENSIONS = {'.jpg', '.jpeg', '.png', '.webp', '.gif', '.bmp', '.tif', '.tiff'}


def extract(request):
    path = Path(request['path'])
    max_bytes = int(request.get('max_bytes', 32 * 1024 * 1024))
    max_chars = int(request.get('max_chars', 200000))
    if max_bytes <= 0 or max_chars <= 0:
        raise ValueError('max_bytes and max_chars must be positive')
    if path.stat().st_size > max_bytes:
        raise ValueError('file exceeds max_bytes')
    result = {'kind': 'document', 'sections': [], 'metadata': {}, 'truncated': False}
    remaining = max_chars
    def add(location, text):
        nonlocal remaining
        text = str(text)
        if len(text) > remaining:
            result['truncated'] = True
        bounded = text[:remaining]
        if bounded:
            result['sections'].append({'location': location, 'text': bounded})
        remaining -= len(bounded)
        return remaining > 0
    suffix = path.suffix.lower()
    if suffix in TEXT_EXTENSIONS:
        # Limit before decoding; four bytes per Unicode code point plus one sentinel.
        with path.open('rb') as stream:
            raw = stream.read(min(max_bytes, max_chars * 4 + 1))
        add('text', raw.decode('utf-8', errors='replace'))
    elif suffix in {'.html', '.htm'}:
        from html.parser import HTMLParser
        class Parser(HTMLParser):
            def __init__(self):
                super().__init__()
                self.hidden = 0
            def handle_starttag(self, tag, attrs):
                if tag in {'script', 'style'}:
                    self.hidden += 1
            def handle_endtag(self, tag):
                if tag in {'script', 'style'}:
                    self.hidden = max(0, self.hidden - 1)
            def handle_data(self, data):
                if not self.hidden:
                    add('html', data)
        parser = Parser()
        with path.open('r', encoding='utf-8', errors='replace') as stream:
            while remaining:
                piece = stream.read(4096)
                if not piece:
                    break
                parser.feed(piece)
        if remaining == 0:
            result['truncated'] = True
    elif suffix == '.pdf':
        from pypdf import PdfReader
        reader = PdfReader(path)
        result['metadata']['pages'] = len(reader.pages)
        result['metadata']['needs_ocr'] = False
        for number, page in enumerate(reader.pages, 1):
            text = page.extract_text() or ''
            if not text.strip():
                result['metadata']['needs_ocr'] = True
            if not add(f'page:{number}', text):
                result['truncated'] |= number < len(reader.pages)
                break
    elif suffix == '.docx':
        from docx import Document
        doc = Document(path)
        for index, block in enumerate(doc.iter_inner_content(), 1):
            text = block.text if hasattr(block, 'text') else '\n'.join('\t'.join(cell.text for cell in row.cells) for row in block.rows)
            if not add(f'block:{index}', text):
                break
    elif suffix in {'.xlsx', '.xlsm'}:
        from openpyxl import load_workbook
        book = load_workbook(path, read_only=True, data_only=True, keep_links=False)
        try:
            for sheet in book:
                for number, row in enumerate(sheet.iter_rows(values_only=True), 1):
                    if not add(f'sheet:{sheet.title}:row:{number}', '\t'.join('' if value is None else str(value) for value in row)):
                        break
                if not remaining:
                    break
        finally:
            book.close()
    elif suffix == '.pptx':
        from pptx import Presentation
        for number, slide in enumerate(Presentation(path).slides, 1):
            for shape in slide.shapes:
                if getattr(shape, 'has_text_frame', False):
                    add(f'slide:{number}', shape.text)
                elif getattr(shape, 'has_table', False):
                    for row in shape.table.rows:
                        add(f'slide:{number}', '\t'.join(cell.text for cell in row.cells))
                if not remaining:
                    break
            if not remaining:
                break
    elif suffix in IMAGE_EXTENSIONS:
        from PIL import Image, ExifTags
        Image.MAX_IMAGE_PIXELS = 25_000_000
        with Image.open(path) as image:
            if image.width * image.height > Image.MAX_IMAGE_PIXELS:
                raise ValueError('image exceeds pixel limit')
            result['kind'] = 'photo'
            result['metadata'].update(width=image.width, height=image.height, format=image.format)
            exif = image.getexif()
            for key, value in exif.items():
                name = ExifTags.TAGS.get(key, str(key))
                if name in {'DateTime', 'DateTimeOriginal', 'Make', 'Model', 'Software', 'ImageDescription'}:
                    result['metadata'][name] = str(value)[:1024]
    else:
        raise ValueError(f'unsupported file type: {suffix or "no extension"}')
    return result


def load_embedding_classes():
    from fastembed import TextEmbedding, ImageEmbedding
    return TextEmbedding, ImageEmbedding


def handle(request):
    operation = request.get('operation', request.get('op'))
    if operation == 'extract':
        return extract(request)
    if operation not in {'embed', 'image_embed', 'image_query'}:
        raise ValueError('unsupported operation')
    values = [request['path']] if operation == 'image_embed' else request['texts']
    if not isinstance(values, list) or not values or len(values) > 32 or any(not isinstance(value, str) for value in values):
        raise ValueError('embedding input must contain 1 to 32 strings')
    if operation != 'image_embed' and sum(len(value) for value in values) > 200000:
        raise ValueError('embedding input exceeds character limit')
    if operation == 'image_embed':
        # Check decoding dimensions and input size before loading heavyweight models.
        extract({'path': request['path'], 'max_bytes': request.get('max_bytes', 32 * 1024 * 1024), 'max_chars': 1})
    TextEmbedding, ImageEmbedding = load_embedding_classes()
    model_name = {'embed': 'BAAI/bge-small-en-v1.5', 'image_embed': 'Qdrant/clip-ViT-B-32-vision', 'image_query': 'Qdrant/clip-ViT-B-32-text'}[operation]
    cls = ImageEmbedding if operation == 'image_embed' else TextEmbedding
    model = cls(model_name=model_name, threads=1, providers=['CPUExecutionProvider'], cache_dir=str(Path(os.environ.get('NAS_DATA', '/data')) / 'models'))
    vectors = [vector.tolist() if hasattr(vector, 'tolist') else list(vector) for vector in model.embed(values, batch_size=1)]
    dimension = 384 if operation == 'embed' else 512
    if len(vectors) != len(values) or any(len(vector) != dimension for vector in vectors):
        raise ValueError('unexpected embedding dimensions')
    return {'vectors': vectors, 'dimension': dimension, 'model': model_name}


def main():
    try:
        request = json.load(sys.stdin)
        with contextlib.redirect_stdout(sys.stderr):
            result = handle(request)
        print(json.dumps(result, allow_nan=False))
    except Exception as error:
        print(json.dumps({'error': str(error)}))
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
