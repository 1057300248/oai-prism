# -*- coding: utf-8 -*-
"""穷举真实 token 的 t 密钥 → 解出真实 accJSON 明文 → 与我的 VM accJSON 逐字节对比。

验证链：t = btoa(JSON(acc) ⊕ K)，K ∈ "NN.NN"（1e4 空间）。
- 若解出的真实 accJSON 与我的 accJSON 一致 → 我的 VM+组装链完全正确，
  403 的唯一剩余变量 = K_final 的可推导性
- 若不一致 → 给出首差异位置
"""
import json, io, os, base64

T = os.environ.get('TEMP', r'C:\Users\13080\AppData\Local\Temp')
cap = json.load(io.open(os.path.join(T, 'instrumented_capture.json'), encoding='utf-8'))
tok = json.loads(cap['apiReq']['sentinelToken'])
real_t = tok['t']
print('真实 t: %d 字符 (b64)' % len(real_t))

pad = real_t + '=' * ((4 - len(real_t) % 4) % 4)
cipher = base64.b64decode(pad).decode('latin1')
print('密文: %d 字符, 首字节 %r' % (len(cipher), cipher[:4]))


def xor(s, k):
    return ''.join(chr(ord(c) ^ ord(k[i % len(k)])) for i, c in enumerate(s))


# 穷举 "NN.NN"（1e4）与 "N.NN"/"NN.N" 变体兜底 —— 必须解析为合法 JSON 且字段数 >= 5
found = None
for i in range(100):
    for j in range(100):
        for k in ('%d.%02d' % (i, j), '%d.%d' % (i, j), '%02d.%d' % (i, j)):
            p = xor(cipher, k)
            if not p.startswith('{"'):
                continue
            try:
                obj = json.loads(p)
            except Exception:
                continue
            if isinstance(obj, dict) and len(obj) >= 5:
                found = (k, p)
                break
        if found:
            break
    if found:
        break

if not found:
    print('✗ 未穷举出密钥（K 可能不是 NN.NN 型）')
    raise SystemExit(0)
k, real_plain = found
print('密钥 K = %r' % k)
print('真实 accJSON: %d 字符' % len(real_plain))
try:
    real_acc = json.loads(real_plain)
    print('  合法 JSON ✓ %d 字段: %s' % (len(real_acc), list(real_acc.keys())))
except Exception as e:
    print('  ✗ JSON 解析失败:', e)
    raise SystemExit(0)

# 与我的 VM accJSON 对比
mine = json.load(io.open(os.path.join(T, 'acc_mine.json'), encoding='utf-8'))
mine_json = json.dumps(mine, ensure_ascii=False, separators=(',', ':'))
print()
print('键序一致:', list(real_acc.keys()) == list(mine.keys()))
if list(real_acc.keys()) == list(mine.keys()):
    n_diff = 0
    for kk in real_acc:
        rv, mv = real_acc[kk], mine.get(kk)
        same = rv == mv
        if not same:
            n_diff += 1
            print('  ✗ %s real=%r' % (kk, str(rv)[:60]))
            print('         mine=%r' % str(mv)[:60])
    if n_diff == 0:
        print('  所有字段值一致 ✓')
# 原始字符串级对比（含空白/转义差异）
print()
if real_plain == mine_json:
    print('★★★ 真实 accJSON 与我的输出逐字节一致 ★★★')
else:
    # 找第一个差异
    for i, (a, b) in enumerate(zip(real_plain, mine_json)):
        if a != b:
            print('原始串首差异@%d:' % i)
            print('  real: %r' % real_plain[max(0, i-30):i+40])
            print('  mine: %r' % mine_json[max(0, i-30):i+40])
            break
    else:
        print('长度差: real %d vs mine %d' % (len(real_plain), len(mine_json)))
        s2 = min(len(real_plain), len(mine_json))
        print('  前 %d 相同; real尾=%r' % (s2, real_plain[s2:s2+60]))
        print('              mine尾=%r' % mine_json[s2:s2+60])
