# -*- coding: utf-8 -*-
"""全链路核对：捕获 → 解密 → VM → 逐字段明文对比（每步显式校验）。"""
import base64, io, json, subprocess, os

T = r'C:\Users\13080\AppData\Local\Temp'

# 1) 最新捕获
cap = json.load(io.open(os.path.join(T, 'instrumented_capture.json'), encoding='utf-8'))
p_req = json.loads(cap['sentinelReq']['body'])['p']
resp = json.loads(cap['sentinelResp']['body'])
dx = resp['turnstile']['dx']
real_acc = json.loads(cap['acc'][-1])
print('[1] 捕获: dx %d 字符 | 真实 acc %d 字段 keys=%s' % (len(dx), len(real_acc), list(real_acc.keys())[:4]))

# 2) 解密 dx
k = p_req.encode()
raw = base64.b64decode(dx)
plain = bytes(c ^ k[i % len(k)] for i, c in enumerate(raw))
l1 = json.loads(plain)
json.dump(l1, io.open(os.path.join(T, 'dx_instructions.json'), 'w', encoding='utf-8'), ensure_ascii=False)
print('[2] dx 解密: %d 条指令' % len(l1))

# 3) 随机序列
io.open(os.path.join(T, 'real_rand.txt'), 'w').write('\n'.join(repr(x) for x in cap['vm']['rand']))

# 4) 跑我的 VM
r = subprocess.run([os.path.join(T, 'sentinelvm', 'vm.exe')], capture_output=True, text=True, errors='replace')
print('[3] VM:', r.stdout.strip().splitlines()[-2] if r.stdout else r.stderr[:120])
mine = json.load(io.open(os.path.join(T, 'acc_mine.json'), encoding='utf-8'))
print('[4] 我的 acc: %d 字段 keys=%s' % (len(mine), list(mine.keys())[:4]))
print('[5] 键序一致:', list(mine.keys()) == list(real_acc.keys()))

# 5) 逐字段明文对比（btoa 钩子配对）
vm = cap['vm']
bmap = {}
for arg, ret in vm['btoa']:
    bmap.setdefault(ret, arg)
def show(x):
    if x == '?': return '?'
    try: return repr(x.decode('latin1')[:36])
    except Exception: return str(x)[:36]
print()
n_ok = 0
for k2, v in real_acc.items():
    mv = mine.get(k2)
    rp = bmap.get(v, None)
    mp = bmap.get(mv, None) if mv is not None else None
    ok = rp is not None and mp is not None and rp == mp
    n_ok += ok
    print('%s %-8s 真实(%2d): %-38s | 我(%2d): %s' % (
        '✓' if ok else '✗', k2,
        len(rp) if rp else -1, show(rp) if rp else '?',
        len(mp) if mp else -1, show(mp) if mp else '?'))
print('\n完全一致: %d/%d' % (n_ok, len(real_acc)))
json.dump(mine, io.open(os.path.join(T, 'my_acc_same.json'), 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
json.dump(real_acc, io.open(os.path.join(T, 'real_acc_same.json'), 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
