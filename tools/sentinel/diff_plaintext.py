# -*- coding: utf-8 -*-
"""明文级逐槽 diff：真实 acc vs 我的 acc（各自用己方 btoa 日志解出明文后对比）。

用法：python diff_plaintext.py
依赖：Temp/instrumented_capture.json（真实捕获）、Temp/acc_mine.json + Temp/btoalog.txt（我的 VM 产出）。
输出：逐槽 ✓/✗ + 首差异位置上下文，末尾给明文级一致数。
"""
import json, io, os

T = os.environ.get('TEMP', r'C:\Users\13080\AppData\Local\Temp')

cap = json.load(io.open(os.path.join(T, 'instrumented_capture.json'), encoding='utf-8'))
real = json.load(io.open(os.path.join(T, 'real_acc_same.json'), encoding='utf-8'))
mine = json.load(io.open(os.path.join(T, 'acc_mine.json'), encoding='utf-8'))

# 真实浏览器 btoa 日志：输出(base64) -> 输入(明文)
bmap = {}
for arg, ret in cap['vm']['btoa']:
    bmap.setdefault(ret, arg)
# 我的 VM btoa 日志（btoalog.json：[[明文, 输出], ...] —— XOR 明文是二进制，必须结构化）
mmap = {}
_bj = json.load(io.open(os.path.join(T, 'btoalog.json'), encoding='utf-8'))
for arg, ret in _bj:
    mmap.setdefault(ret, arg)

n_ok = 0
for k in real:
    rv, mv = real[k], mine.get(k)
    ok = False
    if isinstance(rv, float) or isinstance(mv, float):
        ok = isinstance(rv, float) and isinstance(mv, float)
        print('%s %-7s 随机槽 (real=%r mine=%r)' % ('✓' if ok else '✗', k, rv, mv))
    else:
        rp = bmap.get(rv)
        mp = mmap.get(mv)
        if rp is None or mp is None:
            print('✗ %-7s 配对失败 real_in_bmap=%s mine_in_mmap=%s' % (k, rp is not None, mp is not None))
        else:
            ok = rp == mp
            mark = '✓' if ok else '✗'
            print('%s %-7s len %d vs %d' % (mark, k, len(rp), len(mp)))
            if not ok:
                for i, (a, b) in enumerate(zip(rp, mp)):
                    if a != b:
                        print('     首差异@%d:' % i)
                        print('     real: %r' % rp[max(0, i - 24):i + 30])
                        print('     mine: %r' % mp[max(0, i - 24):i + 30])
                        break
                else:
                    shorter = min(len(rp), len(mp))
                    print('     前 %d 字符相同，长度差：' % shorter)
                    print('     real尾: %r' % rp[shorter:][:40])
                    print('     mine尾: %r' % mp[shorter:][:40])
    n_ok += ok
print()
print('明文级一致: %d/%d' % (n_ok, len(real)))
