# -*- coding: utf-8 -*-
"""全链路核对：捕获 → 解密 → VM → 逐字段明文对比（每步显式校验）。"""
import base64, io, json, subprocess, os, sys

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

# 3) 随机序列（对齐 VM 消费偏移：捕获序列前段是 SDK 层消耗的，如 PoW 种子。
#    找到 random_2 槽（raw float 且在序列中能定位）的索引 i2，VM 的两次
#    Math.random（random_1 → random_2 连续调用）从 i2-1 开始回放）
vm_rand = cap['vm']['rand']
i2 = None
for k2, v2 in real_acc.items():
    if isinstance(v2, float):
        for i3, r3 in enumerate(vm_rand):
            if abs(r3 - v2) < 1e-15:
                i2 = i3
                break
    if i2 is not None:
        break
start = max(0, (i2 - 1)) if i2 is not None else 0
print('[3] 随机回放: random_2 定位于序列[%d]，回放起点 %d（共 %d 值）' % (i2, start, len(vm_rand) - start))
io.open(os.path.join(T, 'real_rand.txt'), 'w').write('\n'.join(repr(x) for x in vm_rand[start:]))

# 4) 跑我的 VM（--trace 供密钥提取；注入随机回放 → Math.random 派生槽与真实逐字节可比）
import subprocess as _sp
r = _sp.run([os.path.join(T, 'sentinelvm', 'vm.exe'), '--trace'],
            capture_output=True, text=True, errors='replace',
            env={**os.environ, 'SENTINEL_RAND': os.path.join(T, 'real_rand.txt')})
print('[3] VM:', r.stdout.strip().splitlines()[-2] if r.stdout else r.stderr[:120])
mine = json.load(io.open(os.path.join(T, 'acc_mine.json'), encoding='utf-8'))
print('[4] 我的 acc: %d 字段 keys=%s' % (len(mine), list(mine.keys())[:4]))
print('[5] 键序一致:', list(mine.keys()) == list(real_acc.keys()))

# 5) 逐槽明文 diff —— 委托 diff_same_challenge.py（crib 恢复密钥版，零钩子依赖）
print()
r2 = subprocess.run([sys.executable, os.path.join(os.path.dirname(os.path.abspath(__file__)), 'diff_same_challenge.py')])
sys.exit(r2.returncode)
json.dump(mine, io.open(os.path.join(T, 'my_acc_same.json'), 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
json.dump(real_acc, io.open(os.path.join(T, 'real_acc_same.json'), 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
