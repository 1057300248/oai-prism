import requests
import json
import base64
import uuid
import re

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {
    'Cookie': cookie,
    'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)',
}

proj_id = '8b90ffa3-1ffc-4450-a957-e0af8183f2ee'
sb_url = 'https://prism.openai.com/s/sandboxes/proxy/'

log_txt = open(r'C:\Users\13080\.gemini\antigravity\brain\649ac808-7a2a-4422-b668-2f509433fb0b\.system_generated\tasks\task-882.log').read()
sb_token = re.search(r'PRISM_SANDBOX_TOKEN=([^\s]+)', log_txt).group(1).strip()

# 1. 上传一个 1x1 纯红 PNG
red_png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==')
file_id = str(uuid.uuid4())
r_up = requests.post('https://prism.openai.com/api/project-files/upload', headers=headers, 
                     data={'path': 'red.png', 'projectId': proj_id, 'fileId': file_id},
                     files={'file': ('red.png', red_png, 'image/png')})
print('Upload:', r_up.status_code, r_up.text)
up_info = r_up.json()
file_uuid = up_info.get('fileUuid') or up_info.get('id')
sediment_id = up_info.get('sedimentFileId')

candidates = [
    f'https://prism.openai.com/api/project-files/download?projectId={proj_id}&fileId={file_uuid}',
    f'https://prism.openai.com/api/project-files/{file_uuid}',
    f'https://prism.openai.com/api/project-files/download?file_id={file_uuid}',
    f'sediment://{sediment_id}',
    f'sediment://{file_uuid}',
    f'file://{file_uuid}',
    f'{sediment_id}',
    f'{file_uuid}',
]

for cand in candidates:
    headers_llm = dict(headers)
    headers_llm['Content-Type'] = 'application/json'
    payload = {
        'model': 'gpt-5.6-sol',
        'conversationId': None,
        'input': [
            {
                'type': 'message',
                'role': 'user',
                'content': [
                    {'type': 'input_text', 'text': '这幅图片的颜色是什么？请直接用中文回答这一个颜色的名字。'},
                    {'type': 'input_image', 'image_url': cand, 'detail': 'auto'}
                ]
            }
        ],
        'metadata': {
            'projectId': proj_id,
            'sandbox_url': sb_url,
            'sandbox_token': sb_token,
            'frontend_origin': 'https://prism.openai.com',
            'model': 'gpt-5.6-sol'
        }
    }
    r = requests.post('https://prism.openai.com/api/llm/response_with_tools_start', headers=headers_llm, json=payload)
    res = r.json()
    status = res.get('status')
    err_obj = res.get('response', {}).get('payload', {})
    err_msg = err_obj.get('rootCause') or err_obj.get('message') or res.get('message')
    print(f'Cand [{cand[:60]}] -> status: {status}, err: {err_msg}')
    if status == 'started':
        print('FOUND VALID URL CANDIDATE! url =', cand)
        print('request_id =', res.get('request_id'))
        req_id = res.get('request_id')
        ts = res.get('turn_state')
        for poll in range(10):
            st_r = requests.post('https://prism.openai.com/api/llm/response_with_tools_status', headers=headers_llm, json={'request_id': req_id, 'turn_state': ts, 'wait_ms': 5000})
            st_res = st_r.json()
            print('Poll round', poll, 'status:', st_res.get('status'))
            if st_res.get('turn_state'):
                ts = st_res['turn_state']
            if st_res.get('status') == 'completed':
                out_payload = st_res.get('response', {}).get('payload', {})
                print('COMPLETED OUTPUT:', json.dumps(out_payload, ensure_ascii=False))
                break
        break
