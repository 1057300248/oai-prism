import requests
import re
import os

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {'Cookie': cookie, 'User-Agent': 'Mozilla/5.0'}
html = requests.get('https://prism.openai.com', headers=headers).text
js_urls = re.findall(r'src="([^"]+\.js)"', html)
print('Found JS scripts:', len(js_urls))
for u in js_urls:
    if not u.startswith('http'):
        u = 'https://prism.openai.com' + u
    try:
        content = requests.get(u, headers=headers).text
        if 'project-files' in content:
            print('Matched script:', u)
            for m in re.finditer(r'project-files[^\'"` ]{0,150}', content):
                print('  project-files:', m.group(0))
            for m in re.finditer(r'input_image[^\'"` ]{0,150}', content):
                print('  input_image:', m.group(0))
    except Exception as e:
        print('Error:', e)
