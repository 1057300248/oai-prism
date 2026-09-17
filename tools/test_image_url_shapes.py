import requests
import json
import base64
import uuid

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {
    'Cookie': cookie,
    'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)',
    'Content-Type': 'application/json'
}

proj_uuid = '97417d71-8df2-453a-bd60-f4f7ce967f44'
file_uuid = 'eb2f4c30-7d99-4596-a5d5-126617344b03'
sediment_id = 'file_000000005fb481f6b2b2c7b52e11502a'

# 候选的 image_url 格式
candidates = [
    f"https://prism.openai.com/api/project-files/download?projectId={proj_uuid}&fileId={file_uuid}",
    f"https://prism.openai.com/api/project-files/{file_uuid}",
    f"https://prism.openai.com/api/project-files/download?file_id={file_uuid}",
    f"sediment://{sediment_id}",
    f"sediment://{file_uuid}",
    f"file://{file_uuid}",
    f"{sediment_id}",
    f"{file_uuid}",
    f"/api/project-files/download?projectId={proj_uuid}&fileId={file_uuid}",
]

for url_cand in candidates:
    payload = {
        "model": "gpt-5.6-sol",
        "conversationId": None,
        "input": [
            {
                "type": "message",
                "role": "user",
                "content": [
                    {"type": "input_text", "text": "What color is this image?"},
                    {"type": "input_image", "image_url": url_cand, "detail": "auto"}
                ]
            }
        ],
        "metadata": {
            "projectId": proj_uuid,
            "frontend_origin": "https://prism.openai.com",
            "model": "gpt-5.6-sol"
        }
    }
    r = requests.post("https://prism.openai.com/api/llm/response_with_tools_start", headers=headers, json=payload)
    res = r.json()
    err = res.get("response", {}).get("payload", {})
    root_cause = err.get("rootCause") or err.get("message")
    status = res.get("status")
    print(f"Cand: {url_cand[:60]}... -> status: {status}, code: {r.status_code}, err: {root_cause}")
    if status == "started":
        print(f"SUCCESS MATCH: {url_cand}")
        break
