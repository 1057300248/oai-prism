# -*- coding: utf-8 -*-
"""Crib-drag：用候选环境词破解各字段的环境串明文（真实+我的），定位桩差距。"""
import json, io

cap = json.load(io.open('instrumented_capture.json', encoding='utf-8'))
real_pairs = [(a, r) for a, r in cap['vm']['btoa']]
mine_pairs = []
for line in io.open('btoalog.txt', encoding='utf-8'):
    parts = line.rstrip(chr(10)).split(chr(9))
    mine_pairs.append((parts[0], parts[-1]))
acc = json.loads(cap['acc'][-1])
mine = json.load(io.open('acc_mine.json', encoding='utf-8'))
rmap, mmap = {}, {}
for a, r in real_pairs: rmap.setdefault(r, a)
for a, r in mine_pairs: mmap.setdefault(r, a)

CRIBS = ['statsig', '300df51147803f41', 'crixet', 'setItem', 'getItem',
         'localStorage', 'length', 'https://', 'prism.openai.com']

def xor(a, b):
    return bytes(x ^ y for x, y in zip(a, b))

def try_decode(cipher):
    cb = cipher.encode('latin1') if isinstance(cipher, str) else cipher
    best = None
    for crib in CRIBS:
        cb_bytes = crib.encode()
        for i in range(0, min(len(cb) - len(cb_bytes), 80)):
            key = xor(cb[i:i+len(cb_bytes)], cb_bytes)
            dec = xor(cb, key + bytes(1))  # key 循环用
            # 用完整 key（key 长度=crib 长度）循环解
            full = bytes(cb[j] ^ key[j % len(key)] for j in range(len(cb)))
            printable = sum(1 for b in full if 32 <= b < 127 or b in (10, 13))
            if printable / len(full) > 0.9:
                score = printable / len(full)
                if best is None or score > best[0]:
                    best = (score, key, full)
    return best

for k in ['39.8', '96.29', '81.72', '90.05', '55.5', '17.03', '93.03']:
    v_real = acc.get(k)
    v_mine = mine.get(k)
    rp = rmap.get(v_real) if isinstance(v_real, str) else None
    mp = mmap.get(v_mine) if isinstance(v_mine, str) else None
    print('=== %s ===' % k)
    for name, plain in (('真实', rp), ('我的', mp)):
        if plain is None or plain == '?':
            print('  %s: (未配对)' % name)
            continue
        b = try_decode(plain)
        if b:
            print('  %s: key=%r 明文=%r' % (name, b[1], b[2].decode('latin1')[:90]))
        else:
            print('  %s: 无法解码 (%d 字节)' % (name, len(plain)))
