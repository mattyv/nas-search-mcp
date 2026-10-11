#!/usr/bin/env python3
"""Reproducible serial CPU worker smoke benchmark; Linux RSS is measured per child."""
import argparse
from datetime import datetime, timezone
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import time

# Each operation gets a fresh interpreter. RUSAGE_SELF therefore excludes earlier
# operations and avoids cumulative RUSAGE_CHILDREN peak attribution mistakes.
CHILD = r'''
import contextlib, importlib.util, json, resource, sys
spec = importlib.util.spec_from_file_location('nas_worker', sys.argv[1])
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)
try:
    request = json.load(sys.stdin)
    with contextlib.redirect_stdout(sys.stderr):
        result = worker.handle(request)
    vectors = result.get('vectors', [])
    result.pop('vectors', None)
    result['vector_count'] = len(vectors)
    result['vector_dimensions'] = [len(v) for v in vectors]
    result['peak_rss_kib'] = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    print(json.dumps(result, allow_nan=False))
except Exception as error:
    print(json.dumps({'error': str(error), 'peak_rss_kib': resource.getrusage(resource.RUSAGE_SELF).ru_maxrss}))
    sys.exit(1)
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data', required=True, help='Data directory containing models/; models download if absent')
    parser.add_argument('--output', required=True, help='JSON result path')
    parser.add_argument('--label', default='development', help='Machine role: use nas only when actually run there')
    args = parser.parse_args()
    if platform.system() != 'Linux':
        parser.error('This script reports Linux ru_maxrss units; run on Linux')
    project = Path(__file__).resolve().parents[1]
    cpu = 'unknown'
    for line in Path('/proc/cpuinfo').read_text().splitlines():
        if line.startswith('model name'):
            cpu = line.split(':', 1)[1].strip()
            break
    versions = {}
    for package in ['fastembed', 'onnxruntime', 'numpy', 'Pillow']:
        try:
            versions[package] = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError:
            versions[package] = 'not installed'
    report = {'recorded_at_utc': datetime.now(timezone.utc).isoformat(), 'machine_role': args.label,
              'is_nas_measurement': args.label == 'nas', 'platform': {'system': platform.system(), 'kernel': platform.release(), 'architecture': platform.machine(), 'cpu': cpu, 'python': platform.python_version()},
              'dependencies': versions, 'method': 'One fresh subprocess per operation, serial execution, CPUExecutionProvider, threads=1, batch_size=1. Elapsed includes interpreter/model loading. Peak RSS from each subprocess RUSAGE_SELF, Linux KiB. Single samples, not throughput estimates.',
              'cache': 'Existing model cache if provided; downloads allowed when absent. Cache paths omitted.', 'operations': []}
    environment = dict(os.environ, NAS_DATA=str(Path(args.data).resolve()), OMP_NUM_THREADS='1', OPENBLAS_NUM_THREADS='1', TOKENIZERS_PARALLELISM='false')
    with tempfile.TemporaryDirectory(prefix='nas-benchmark-') as temp:
        from PIL import Image
        image = Path(temp) / 'red-square.png'
        Image.new('RGB', (64, 64), (255, 0, 0)).save(image)
        requests = [{'operation': 'embed', 'texts': ['A red square photo and a sample document.']}, {'operation': 'image_embed', 'path': str(image)}, {'operation': 'image_query', 'texts': ['red square']}]
        for request in requests:
            start = time.monotonic()
            process = subprocess.run([sys.executable, '-c', CHILD, str(project / 'worker/worker.py')], input=json.dumps(request), text=True, capture_output=True, env=environment, timeout=600)
            elapsed = time.monotonic() - start
            result = json.loads(process.stdout)
            report['operations'].append({'operation': request['operation'], 'exit_code': process.returncode, 'elapsed_seconds': round(elapsed, 4), 'result': result})
            print(f"{request['operation']}: exit {process.returncode}, elapsed {elapsed:.2f}s, peak RSS {result.get('peak_rss_kib')} KiB", flush=True)
    destination = Path(args.output)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(report, indent=2) + '\n')
    return int(any(op['exit_code'] for op in report['operations']))


if __name__ == '__main__':
    sys.exit(main())
