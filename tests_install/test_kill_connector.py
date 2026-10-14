import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class KillConnectorTests(unittest.TestCase):
    def run_kill(self, revoke_status=0, stop_status=0):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docker = root / 'docker'
            docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$CALLS"\ncase "$*" in\n  *revoke-oauth*) exit "$REVOKE_STATUS";;\n  *) exit "$STOP_STATUS";;\nesac\n')
            docker.chmod(0o755)
            calls = root / 'calls'
            env = dict(os.environ, PATH=f'{root}:{os.environ["PATH"]}', CALLS=str(calls), REVOKE_STATUS=str(revoke_status), STOP_STATUS=str(stop_status))
            script = Path(__file__).resolve().parents[1] / 'scripts/kill-connector.sh'
            result = subprocess.run(['sh', str(script)], env=env, capture_output=True, text=True)
            return result, calls.read_text().splitlines() if calls.exists() else []

    def test_stop_before_offline_revoke_prevents_inflight_token_issuance(self):
        result, calls = self.run_kill()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertTrue(calls[0].endswith('stop nas-search'))
        self.assertTrue(calls[1].endswith('run --rm --no-deps nas-search revoke-oauth'))
        self.assertIn('Tokens revoked', result.stdout)

    def test_stop_even_when_revocation_fails(self):
        result, calls = self.run_kill(revoke_status=3)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 2)
        self.assertTrue(calls[0].endswith('stop nas-search'))
        self.assertIn('revocation failed', result.stderr)
        self.assertNotIn('Tokens revoked', result.stdout)

    def test_stop_failure_reported(self):
        result, calls = self.run_kill(stop_status=4)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 1)
        self.assertIn('stop failed', result.stderr)
        self.assertNotIn('Tokens revoked', result.stdout)
