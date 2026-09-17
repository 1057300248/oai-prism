import requests

cookie = open('secrets/cookies.txt', encoding='utf-8').read().strip()
headers = {'Cookie': cookie, 'User-Agent': 'Mozilla/5.0'}
url = 'https://prism.openai.com/_next/static/chunks/11850yk199q3a.js'
t = requests.get(url, headers=headers).text
needle = 'input_image'
pos = 0
while True:
    idx = t.find(needle, pos)
    if idx == -1:
        break
    snippet = t[max(0, idx-200):min(len(t), idx+400)]
    if 'image_url' in snippet:
        print('--- FOUND SNIPPET ---')
        print(snippet)
        print('---------------------')
    pos = idx + len(needle)
