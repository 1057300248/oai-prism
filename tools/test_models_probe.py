import requests
import json

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {
    'Cookie': cookie,
    'User-Agent': 'Mozilla/5.0',
    'Content-Type': 'application/json'
}

candidates = ['gpt-6-astra', 'gpt-6', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5']
for m in candidates:
    payload = {
        'model': m,
        'conversationId': None,
        'input': [{'type': 'message', 'role': 'user', 'content': [{'type': 'input_text', 'text': 'hi'}]}],
        'metadata': {'model': m, 'frontend_origin': 'https://prism.openai.com'}
    }
    r = requests.post('https://prism.openai.com/api/llm/response_with_tools_start', headers=headers, json=payload)
    res = r.json()
    status = res.get('status')
    err = res.get('response', {}).get('payload', {})
    err_msg = err.get('rootCause') or err.get('message') or res.get('message')
    print(f'Model [{m}] -> HTTP {r.status_code}, status: {status}, err: {err_msg}')
