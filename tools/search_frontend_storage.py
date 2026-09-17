import requests
import re
import sys

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {'Cookie': cookie, 'User-Agent': 'Mozilla/5.0'}
html = requests.get('https://prism.openai.com', headers=headers).text
matches = re.findall(r'/_next/static/[^"]+\.js', html)
print('Static JS count:', len(matches), flush=True)

found_urls = set()
for path in matches:
    url = 'https://prism.openai.com' + path
    try:
        r = requests.get(url, headers=headers)
        txt = r.text
        if 'storage' in txt.lower() or 'storageurl' in txt.lower() or 'prism-storage' in txt.lower() or 'sediment' in txt.lower():
            for m in re.finditer(r'["\'](https?://[^"\']+|/[^"\']+)["\']', txt):
                val = m.group(1)
                if any(k in val.lower() for k in ['storage', 'upload', 'files', 'image', 'asset', 'download']):
                    if val not in found_urls and len(val) < 120:
                        found_urls.add(val)
                        print('Found URL pattern:', val, flush=True)
    except Exception as e:
        print('Err:', e, flush=True)
