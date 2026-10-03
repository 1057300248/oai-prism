"""Pinned real Codex client + real gateway + fixed upstream plans.

This validates transport and local execution, NOT model intelligence. No live
model/API credentials are used; the only writable project is a disposable repo.
"""
from __future__ import annotations
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.request

BASE = 'http://127.0.0.1:18790/v1'
KEY = 'codex-fixture-key'

def get(path: str) -> bytes:
    request = urllib.request.Request(BASE + path, headers={'Authorization': 'Bearer ' + KEY})
    with urllib.request.urlopen(request, timeout=3) as response:
        return response.read()

def run(command: list[str], **kwargs) -> subprocess.CompletedProcess:
    result = subprocess.run(command, text=True, capture_output=True, timeout=90, **kwargs)
    if result.returncode:
        raise AssertionError(f'Command failed ({result.returncode}): {command}\n{result.stdout[-6000:]}\n{result.stderr[-6000:]}')
    return result

def main() -> None:
    cli = os.environ['CODEX_CLI']
    binary = os.environ['CODEX_FIXTURE_BIN']
    evidence = Path(os.environ.get('CODEX_EVIDENCE_DIR', 'codex-evidence')).resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    version = run([cli, '--version']).stdout.strip()
    assert '0.160.0' in version, version
    (evidence / 'version.txt').write_text(version)
    parent = Path(os.environ.get('RUNNER_TEMP', str(Path.home())))
    with tempfile.TemporaryDirectory(prefix='oaiprism-codex-', dir=parent) as temporary:
        base = Path(temporary)
        work, home = base / 'work', base / 'codex-home'
        work.mkdir(); home.mkdir()
        run(['git', 'init', '-q', str(work)])
        run(['git', '-C', str(work), 'config', 'user.name', 'Codex Fixture'])
        run(['git', '-C', str(work), 'config', 'user.email', 'fixture@example.invalid'])
        good = 'def add(a, b):\n    return a + b\n'
        bad = 'def add(a, b):\n    return a - b\n'
        (work / 'calc.py').write_text(good)
        (work / 'AGENTS.md').write_text('Only modify calc.py. Do not edit the tests. Run python3 -m unittest -v after a fix. Do not request elevated permissions.\n')
        (work / 'test_calc.py').write_text('import unittest\nfrom calc import add\nclass Addition(unittest.TestCase):\n    def test_add(self):\n        self.assertEqual(add(2, 3), 5)\n')
        run(['git', '-C', str(work), 'add', '.']); run(['git', '-C', str(work), 'commit', '-qm', 'fixture baseline'])
        (work / 'calc.py').write_text(bad)
        report = evidence / 'gateway-turns.jsonl'
        if report.exists(): report.unlink()
        # Do not pass GitHub/model credentials to the fixture or tool subprocess.
        env = {k: v for k, v in os.environ.items() if k in {'PATH', 'HOME', 'LANG', 'LC_ALL', 'TMPDIR', 'USER', 'SHELL'}}
        env.update(CODEX_HOME=str(home), FIXTURE_KEY=KEY, CODEX_FIXTURE_WORKDIR=str(work), CODEX_FIXTURE_REPORT=str(report))
        with (evidence / 'fixture.log').open('w') as logfile:
            process = subprocess.Popen([binary], env=env, stdout=logfile, stderr=subprocess.STDOUT)
            try:
                for _ in range(100):
                    if process.poll() is not None: raise AssertionError('Fixture exited before readiness')
                    try:
                        data = get('/codex/models'); break
                    except Exception: time.sleep(.1)
                else: raise AssertionError('Fixture did not start')
                catalog = json.loads(data)
                assert {m['slug'] for m in catalog['models']} == {'codex-fixture', 'codex-review'}
                catalog_path = home / 'models.json'; catalog_path.write_bytes(data)
                (evidence / 'model-catalog.json').write_bytes(data)
                config = '\n'.join([
                    'model_provider = "fixture"', 'model = "codex-fixture"',
                    'model_reasoning_effort = "medium"', 'web_search = "disabled"',
                    'approval_policy = "never"', # cannot grant an escalation in CI
                    'model_catalog_json = ' + json.dumps(str(catalog_path)),
                    '[model_providers.fixture]', 'name = "fixture"',
                    'base_url = "' + BASE + '"', 'wire_api = "responses"', 'env_key = "FIXTURE_KEY"',
                ]) + '\n'
                (home / 'config.toml').write_text(config)
                cmd = [cli, 'exec', '--json', '--ephemeral', '--sandbox', 'workspace-write', '-C', str(work),
                       'Read calc.py, fix add using apply_patch, then run the unit tests. Follow AGENTS.md.']
                result = run(cmd, env=env)
                (evidence / 'edit-events.jsonl').write_text(result.stdout)
                (evidence / 'edit-stderr.txt').write_text(result.stderr)
                assert (work / 'calc.py').read_text() == good, 'CLI did not apply the patch locally'
                run(['python3', '-m', 'unittest', '-v'], cwd=work, env=env)
                changed = run(['git', '-C', str(work), 'status', '--porcelain']).stdout
                # A fixed file matches baseline; only bytecode may be untracked.
                assert 'test_calc.py' not in changed and 'AGENTS.md' not in changed, changed
                turns = [json.loads(line) for line in report.read_text().splitlines()]
                edits = [t for t in turns if t['model'] == 'codex-fixture']
                assert [t['prior_outputs'] for t in edits] == [0, 1, 2, 3], edits
                assert edits[1]['returned_calls'][0]['name'] == 'apply_patch'
                last = json.dumps(edits[-1]['received_outputs'][-1])
                assert 'OK' in last and 'Process exited with code 0' in last, last
                # Local review against a deliberately reintroduced bad diff.
                shutil.rmtree(work / '__pycache__', ignore_errors=True)
                (work / 'calc.py').write_text(bad)
                before = {p.name: p.read_bytes() for p in work.iterdir() if p.is_file()}
                review = run([cli, 'exec', '--json', '--ephemeral', '--sandbox', 'read-only', '-C', str(work),
                              '-c', 'model="codex-review"', 'review', '--uncommitted'], env=env)
                (evidence / 'review-events.jsonl').write_text(review.stdout)
                (evidence / 'review-stderr.txt').write_text(review.stderr)
                assert 'Addition must not subtract' in review.stdout, review.stdout
                after = {p.name: p.read_bytes() for p in work.iterdir() if p.is_file()}
                assert before == after, 'Read-only review modified the worktree'
                turns = [json.loads(line) for line in report.read_text().splitlines()]
                reviews = [t for t in turns if t['model'] == 'codex-review']
                assert [t['prior_outputs'] for t in reviews] == [0, 1], reviews
                summary = {'codex_version': version, 'upstream': 'deterministic localhost fixture',
                           'read_file': True, 'custom_apply_patch': True, 'client_unit_tests': True,
                           'review_diff': True, 'review_worktree_unchanged': True, 'live_model_validated': False}
                (evidence / 'summary.json').write_text(json.dumps(summary, indent=2))
                print(json.dumps(summary, indent=2))
            finally:
                process.terminate()
                try: process.wait(timeout=5)
                except subprocess.TimeoutExpired: process.kill(); process.wait()

if __name__ == '__main__':
    main()
