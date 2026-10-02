# -*- coding: utf-8 -*-
"""检查：按 Go Sprintf(%q) 语义重组的 token 字符串是否与捕获的逐字节一致。"""
import json, io, os

T = os.environ.get('TEMP', r'C:\Users\13080\AppData\Local\Temp')
cap = json.load(io.open(os.path.join(T, 'instrumented_capture.json'), encoding='utf-8'))
real_tok_str = cap['apiReq']['sentinelToken']
rt = json.loads(real_tok_str)


def goq(s):
    out = '"'
    for ch in s:
        if ch == '\\':
            out += '\\\\'
        elif ch == '"':
            out += '\\"'
        else:
            out += ch
    return out + '"'


mine = '{"p":%s,"t":%s,"c":%s,"flow":"prism_inference"}' % (goq(rt['p']), goq(rt['t']), goq(rt['c']))
print('捕获 token : %d 字符' % len(real_tok_str))
print('重组 token : %d 字符' % len(mine))
print('逐字节一致:', mine == real_tok_str)
if mine != real_tok_str:
    for i, (a, b) in enumerate(zip(real_tok_str, mine)):
        if a != b:
            print('首差异@%d: real=%r' % (i, real_tok_str[max(0, i - 30):i + 30]))
            print('           mine=%r' % mine[max(0, i - 30):i + 30])
            break
    else:
        n = min(len(real_tok_str), len(mine))
        print('前 %d 相同; real尾=%r' % (n, real_tok_str[n:n + 60]))
        print('           mine尾=%r' % mine[n:n + 60])
