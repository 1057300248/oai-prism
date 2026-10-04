"""Deterministic local MCP test server. No network, commands or credentials."""
from __future__ import annotations
import base64
import json
from pathlib import Path
import sys


def main() -> None:
    image = Path(sys.argv[1]).read_bytes()
    if len(image) > 1024 * 1024:
        raise ValueError('Fixture image too large')
    encoded = base64.b64encode(image).decode()
    while True:
        line = sys.stdin.buffer.readline(1024 * 1024 + 1)
        if not line:
            return
        if len(line) > 1024 * 1024:
            raise ValueError('Oversized MCP fixture request')
        request = json.loads(line)
        if 'id' not in request:
            continue
        method = request.get('method')
        params = request.get('params', {})
        result = None
        error = None
        if method == 'initialize':
            result = {'protocolVersion': params['protocolVersion'], 'capabilities': {'tools': {}},
                      'serverInfo': {'name': 'oaiprism-protocol-fixture', 'version': '1.0'}}
        elif method == 'tools/list':
            result = {'tools': [{'name': 'fixture_echo', 'description': 'Return fixed text and a fixture PNG without side effects.',
                                'inputSchema': {'type': 'object', 'properties': {'text': {'type': 'string'}},
                                                'required': ['text'], 'additionalProperties': False},
                                'annotations': {'readOnlyHint': True, 'destructiveHint': False}}]}
        elif method == 'tools/call' and params.get('name') == 'fixture_echo':
            text = params.get('arguments', {}).get('text')
            if not isinstance(text, str) or len(text) > 2048:
                error = {'code': -32602, 'message': 'Expected bounded text'}
            else:
                result = {'content': [{'type': 'text', 'text': text},
                                      {'type': 'image', 'data': encoded, 'mimeType': 'image/png'}], 'isError': False}
        elif method == 'ping':
            result = {}
        elif method in ('resources/list', 'resources/templates/list'):
            result = {'resources': []} if method == 'resources/list' else {'resourceTemplates': []}
        else:
            error = {'code': -32601, 'message': 'Unsupported fixture method'}
        reply = {'jsonrpc': '2.0', 'id': request['id']}
        reply['error' if error else 'result'] = error or result
        print(json.dumps(reply, separators=(',', ':')), flush=True)


if __name__ == '__main__':
    main()
