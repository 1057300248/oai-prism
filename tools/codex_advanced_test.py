"""Actual pinned Codex and official SDK against a deterministic local gateway.
All model outputs are fixtures. These tests do NOT measure model intelligence.
"""
from __future__ import annotations
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import time
import urllib.request
import zlib

from openai import OpenAI, NotFoundError
from codex_contract_test import check_sandbox

BASE = 'http://127.0.0.1:18792/v1'
KEY = 'advanced-fixture-key'


def png_bytes() -> bytes:
    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data) & 0xffffffff)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 2, 2, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(b'\x00' + b'\xff\x00\x00' * 2 + b'\x00' + b'\x00\xff\x00' * 2)) + chunk(b'IEND', b'')


def main() -> None:
    cli = os.environ['CODEX_CLI']
    binary = os.environ['CODEX_ADVANCED_BIN']
    evidence = Path(os.environ.get('CODEX_ADVANCED_EVIDENCE', 'advanced-evidence')).resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    version = subprocess.check_output([cli, '--version'], text=True).strip()
    assert '0.160.0' in version, version
    (evidence / 'version.txt').write_text(version)
    failures: list[str] = []
    results: dict[str, object] = {'version': version, 'live_model_validated': False}
    with tempfile.TemporaryDirectory(prefix='oaiprism-advanced-', dir=os.environ.get('RUNNER_TEMP', str(Path.home()))) as directory:
        root = Path(directory)
        work, home = root / 'work', root / 'codex-home'
        work.mkdir(); home.mkdir()
        subprocess.run(['git', 'init', '-q', str(work)], check=True)
        (work / 'AGENTS.md').write_text('This is an isolated protocol fixture. Use only the requested tools. Do not request elevated permissions or edit this file.\n')
        image = work / 'image.png'
        image.write_bytes(png_bytes())
        report = evidence / 'gateway-turns.jsonl'
        report.unlink(missing_ok=True)
        env = {k: v for k, v in os.environ.items() if k in {'PATH', 'HOME', 'LANG', 'LC_ALL', 'TMPDIR', 'USER', 'SHELL'}}
        env.update(CODEX_HOME=str(home), FIXTURE_KEY=KEY, CODEX_FIXTURE_WORKDIR=str(work), CODEX_FIXTURE_REPORT=str(report))
        with (evidence / 'fixture.log').open('w') as log:
            process = subprocess.Popen([binary], env=env, stdout=log, stderr=subprocess.STDOUT)
            try:
                for _ in range(100):
                    if process.poll() is not None:
                        raise AssertionError('Advanced fixture exited early')
                    try:
                        request = urllib.request.Request(BASE + '/codex/models', headers={'Authorization': 'Bearer ' + KEY, 'X-Fixture-Tenant': 'advanced-cli'})
                        with urllib.request.urlopen(request, timeout=2) as response:
                            data = response.read()
                        break
                    except OSError:
                        time.sleep(.1)
                else:
                    raise AssertionError('Advanced fixture did not become ready')
                catalog = json.loads(data)
                assert all('image' in m['input_modalities'] for m in catalog['models']), catalog
                (home / 'models.json').write_bytes(data)
                config = '\n'.join([
                    'model_provider = "fixture"', 'model = "advanced-image"', 'model_reasoning_effort = "medium"',
                    'model_catalog_json = ' + json.dumps(str(home / 'models.json')),
                    'web_search = "disabled"', 'approval_policy = "never"',
                    '[model_providers.fixture]', 'name = "fixture"', 'base_url = "' + BASE + '"',
                    'wire_api = "responses"', 'env_key = "FIXTURE_KEY"',
                    'http_headers = { "X-Fixture-Tenant" = "advanced-cli" }',
                    'request_max_retries = 0', 'stream_max_retries = 0',
                    '[features]', 'multi_agent = true',
                    '[agents]', 'max_threads = 3', 'max_depth = 1',
                    '[mcp_servers.fixture]', 'command = "python3"',
                    'args = ' + json.dumps([str(Path(__file__).with_name('mcp_fixture.py').resolve()), str(image)]),
                    'startup_timeout_sec = 10', 'tool_timeout_sec = 10',
                ]) + '\n'
                (home / 'config.toml').write_text(config)
                check_sandbox(cli, work, root, env, evidence)
                before = {p.name: p.read_bytes() for p in work.iterdir() if p.is_file()}
                scenarios = [
                    ('image', 'advanced-image', ['-i', str(image)], 'Inspect the attached fixture image without tools.', 'IMAGE_BYTES_RECEIVED'),
                    ('view', 'advanced-view', [], 'Call view_image on image.png and report receipt without editing files.', 'VIEW_IMAGE_RECEIVED'),
                    ('mcp', 'advanced-mcp', [], 'Call the fixture_echo MCP tool with text MCP_ROUND_TRIP; report the returned image and text.', 'MCP_IMAGE_AND_TEXT_RECEIVED'),
                    ('stdin', 'advanced-stdin', [], 'Start the interactive fixture process, send hello-fixture via write_stdin, then report its completion.', 'INTERACTIVE_PROCESS_COMPLETED'),
                    ('subagent', 'advanced-parallel', [], 'Explicitly spawn one child agent for the fixture check, wait for its result, then report completion.', 'SUBAGENT_ROUND_TRIP_COMPLETED'),
                ]
                for name, model, flags, prompt, expected in scenarios:
                    command = [cli, 'exec', '--json', '--ephemeral', '--sandbox', 'workspace-write', '-C', str(work), '-c', 'model=' + json.dumps(model), *flags, prompt]
                    try:
                        completed = subprocess.run(command, env=env, capture_output=True, text=True, timeout=100)
                        (evidence / (name + '-events.jsonl')).write_text(completed.stdout)
                        (evidence / (name + '-stderr.txt')).write_text(completed.stderr)
                        if completed.returncode != 0 or expected not in completed.stdout:
                            raise AssertionError(f'exit={completed.returncode}\n{completed.stdout[-5000:]}\n{completed.stderr[-3000:]}')
                        turns = [json.loads(line) for line in report.read_text().splitlines()]
                        selected = [t for t in turns if t['model'] == model]
                        assert selected and any(t.get('text') == expected for t in selected), selected
                        if name in ('image', 'view', 'mcp'):
                            assert any(t['image_hashes'] for t in selected), selected
                        if name == 'image':
                            assert hashlib.sha256(image.read_bytes()).hexdigest() in selected[0]['image_hashes'], 'CLI altered/lost the small PNG unexpectedly'
                        results[name] = True
                    except Exception as exc:
                        failures.append(name + ': ' + str(exc))
                        results[name] = False
                after = {p.name: p.read_bytes() for p in work.iterdir() if p.is_file()}
                assert before == after, 'Advanced read-only tasks changed source files'
                try:
                    with OpenAI(api_key=KEY, base_url=BASE, default_headers={'X-Fixture-Tenant': 'sdk-owner'}, max_retries=0, timeout=15) as client:
                        uploaded = client.files.create(file=('notes.md', b'SDK_FILE_CONTENT_513', 'text/markdown'), purpose='user_data', expires_after={'anchor': 'created_at', 'seconds': 3600})
                        assert client.files.retrieve(uploaded.id).filename == 'notes.md'
                        assert client.files.content(uploaded.id).content == b'SDK_FILE_CONTENT_513'
                        assert uploaded.id in [f.id for f in client.files.list(limit=10).data]
                        response = client.responses.create(model='advanced-files', input=[{'role': 'user', 'content': [{'type': 'input_file', 'file_id': uploaded.id}]}], store=True)
                        assert response.output_text == 'FILE_CONTENT_RECEIVED'
                        with client.with_options(default_headers={'X-Fixture-Tenant': 'other-owner'}) as other:
                            try: other.files.retrieve(uploaded.id)
                            except NotFoundError: pass
                            else: raise AssertionError('Cross-tenant SDK file access')
                        uploaded_image = client.files.create(file=('small.png', image.read_bytes(), 'image/png'), purpose='user_data')
                        visual = client.responses.create(model='advanced-image', input=[{'role': 'user', 'content': [{'type': 'input_image', 'file_id': uploaded_image.id}]}], store=False)
                        assert visual.output_text == 'IMAGE_BYTES_RECEIVED'
                        assert client.files.delete(uploaded.id).deleted
                        try: client.responses.create(model='advanced-files', input='continue', previous_response_id=response.id, store=False)
                        except NotFoundError: pass
                        else: raise AssertionError('Deleted file continued from snapshot')
                        client.files.delete(uploaded_image.id)
                        results['files_sdk'] = True
                except Exception as exc:
                    failures.append('files_sdk: ' + str(exc))
                    results['files_sdk'] = False
            finally:
                process.terminate()
                try: process.wait(timeout=5)
                except subprocess.TimeoutExpired: process.kill(); process.wait()
    (evidence / 'summary.json').write_text(json.dumps(results, indent=2))
    (evidence / 'failures.txt').write_text('\n\n'.join(failures))
    print(json.dumps(results, indent=2))
    if failures:
        raise AssertionError('\n\n'.join(failures))


if __name__ == '__main__':
    main()
