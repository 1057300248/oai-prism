import requests
import re
import concurrent.futures

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {'Cookie': cookie, 'User-Agent': 'Mozilla/5.0'}
html = requests.get('https://prism.openai.com', headers=headers).text
matches = re.findall(r'/_next/static/[^"\' ]+\.js', html)
print('Total JS files:', len(matches))

def check_file(path):
    url = 'https://prism.openai.com' + path
    try:
        r = requests.get(url, headers=headers, timeout=10)
        t = r.text
        results = []
        if 'storage' in t.lower() or 'image' in t.lower():
            for m in re.finditer(r'https?://[a-zA-Z0-9_\-\.\/]+storage[a-zA-Z0-9_\-\.\/]+', t, re.IGNORECASE):
                results.append(m.group(0))
            for m in re.finditer(r'/api/[a-zA-Z0-9_\-\.\/]*image[a-zA-Z0-9_\-\.\/]*', t, re.IGNORECASE):
                results.append(m.group(0))
            for m in re.finditer(r'/api/[a-zA-Z0-9_\-\.\/]*upload[a-zA-Z0-9_\-\.\/]*', t, re.IGNORECASE):
                results.append(m.group(0))
        return path, results
    except Exception as e:
        return path, []

with concurrent.futures.ThreadPoolExecutor(max_workers=10) as ex:
    for path, res in ex.map(check_file, matches):
        if res:
            print(path, set(res))
