import requests
import re

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {'Cookie': cookie, 'User-Agent': 'Mozilla/5.0'}
url = 'https://prism.openai.com/_next/static/chunks/11850yk199q3a.js'
t = requests.get(url, headers=headers).text

for m in re.finditer(r'[a-zA-Z0-9_\.]+\.appendPromptAttachmentToMessage', t):
    idx = m.start()
    print('Pattern 1:', t[max(0, idx-100):min(len(t), idx+200)])

for m in re.finditer(r'appendPromptAttachmentToMessage\(', t):
    idx = m.start()
    print('Pattern 2:', t[max(0, idx-100):min(len(t), idx+200)])
