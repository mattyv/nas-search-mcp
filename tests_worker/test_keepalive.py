import io
import json

from test_worker import worker


def test_reuses_one_model_and_releases_before_switch(monkeypatch):
    import weakref
    live = []
    loaded = []
    class Model:
        def __init__(self, **kwargs):
            assert not any(ref() is not None for ref in live)
            live.append(weakref.ref(self))
            loaded.append(kwargs['model_name'])
            self.dimension = 512 if 'clip' in loaded[-1] else 384
        def embed(self, values, **kwargs):
            return [[0.1] * self.dimension for _ in values]
    monkeypatch.setattr(worker, 'load_embedding_classes', lambda: (Model, Model))
    cache = worker.ModelCache()
    for op in ['embed', 'embed', 'image_query', 'image_query', 'embed']:
        worker.handle({'operation': op, 'texts': ['hello']}, cache)
    assert loaded == ['BAAI/bge-small-en-v1.5', 'Qdrant/clip-ViT-B-32-text', 'BAAI/bge-small-en-v1.5']


def test_server_handles_multiple_requests_and_recovers_from_error(monkeypatch):
    requests = [{'operation': 'bogus'}, {'operation': 'embed', 'texts': ['hello']}, {'operation': 'embed', 'texts': ['again']}]
    calls = []
    class Model:
        def __init__(self, **kwargs): calls.append(kwargs)
        def embed(self, values, **kwargs):
            print('model diagnostic')
            return [[0.1] * 384]
    monkeypatch.setattr(worker, 'load_embedding_classes', lambda: (Model, Model))
    monkeypatch.setattr(worker.sys, 'stdin', io.StringIO('\n'.join(map(json.dumps, requests)) + '\n'))
    output = io.StringIO()
    monkeypatch.setattr(worker.sys, 'stdout', output)
    assert worker.serve() == 0
    responses = [json.loads(line) for line in output.getvalue().splitlines()]
    assert 'error' in responses[0]
    assert [response['dimension'] for response in responses[1:]] == [384, 384]
    assert len(calls) == 1


def test_embedding_batch_size_follows_input_capped_at_16(monkeypatch):
    sizes = []
    class Model:
        def __init__(self, **kwargs): pass
        def embed(self, values, **kwargs):
            sizes.append(kwargs['batch_size'])
            return [[0.1] * 384 for _ in values]
    monkeypatch.setattr(worker, 'load_embedding_classes', lambda: (Model, Model))
    cache = worker.ModelCache()
    for count in (1, 5, 16, 32):
        worker.handle({'operation': 'embed', 'texts': ['x'] * count}, cache)
    assert sizes == [1, 5, 16, 16]


def test_server_reports_rss_on_success_and_error(monkeypatch):
    requests = [{'operation': 'bogus'}, {'operation': 'embed', 'texts': ['hello']}]
    class Model:
        def __init__(self, **kwargs): pass
        def embed(self, values, **kwargs): return [[0.1] * 384]
    monkeypatch.setattr(worker, 'load_embedding_classes', lambda: (Model, Model))
    monkeypatch.setattr(worker.sys, 'stdin', io.StringIO('\n'.join(map(json.dumps, requests)) + '\n'))
    output = io.StringIO()
    monkeypatch.setattr(worker.sys, 'stdout', output)
    worker.serve()
    responses = [json.loads(line) for line in output.getvalue().splitlines()]
    assert all(isinstance(r['rss_kib'], int) and r['rss_kib'] > 0 for r in responses)
