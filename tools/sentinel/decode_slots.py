# -*- coding: utf-8 -*-
"""自解码：把我的 VM 产出的 acc 每个槽解回明文（无需真实对照）。

原理：槽值 = btoa(XOR(明文, K))，K 是 trace 里 SET 出现过的 "NN.NN" 字面量。
对每槽尝试全部候选密钥，取可打印率最高者。

用法：cd %TEMP% && python decode_slots.py [acc文件] [btoalog文件]
"""
import json, io, os, sys

T = os.environ.get('TEMP', r'C:\Users\13080\AppData\Local\Temp')
acc_path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(T, 'acc_mine.json')
log_path = sys.argv[2] if len(sys.argv) > 2 else os.path.join(T, 'btoalog.json')

mine = json.load(io.open(acc_path, encoding='utf-8'))
bj = json.load(io.open(log_path, encoding='utf-8'))
mmap = {}
for a, r in bj:
    mmap.setdefault(r, a)

# 候选密钥来自 trace 的 SET 日志（slot_keys.txt，由 grep 生成）
cands = [l.strip() for l in io.open(os.path.join(T, 'slot_keys.txt'), encoding='utf-8') if l.strip()]


def x(s, k):
    return ''.join(chr(ord(c) ^ ord(k[i % len(k)])) for i, c in enumerate(s))


for k, v in mine.items():
    if isinstance(v, float):
        print('%-8s RAW-FLOAT %r' % (k, v))
        continue
    inp = mmap.get(v)
    if inp is None:
        print('%-8s 无btoa日志 len=%d %r' % (k, len(v), v[:40]))
        continue
    best = None
    for c in cands:
        p = x(inp, c)
        pr = sum(1 for ch in p if 32 <= ord(ch) < 127)
        if best is None or pr > best[1]:
            best = (p, pr, c)
    p, pr, c = best
    tag = 'OK ' if pr >= len(p) * 0.9 else '?? '
    print('%s%-8s key=%-8s len=%-4d %r' % (tag, k, c, len(p), p[:72]))
