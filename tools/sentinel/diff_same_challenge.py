# -*- coding: utf-8 -*-
"""同挑战逐槽明文 diff（crib 恢复密钥版，零钩子依赖）。

每槽值 = btoa(XOR(明文, K))，K 为该挑战的槽密钥（双方相同）。
解法分层：
  1. b64 解码得密文；若密文本身可打印 → 无 XOR 槽（明文直存）
  2. 用语义 crib 前缀（{"x": / https:// / TypeError / ["Google / {"availWidth" / statsig）
     恢复 K，校验周期性（K 应为 4~5 字符的 "NN.NN" 型）
  3. 兜底：trace 的 SET "NN.NN" 字面量候选
选出的 K 同时解双方密文，逐槽 diff。

用法：python diff_same_challenge.py
"""
import json, io, os, re, base64

T = os.environ.get('TEMP', r'C:\Users\13080\AppData\Local\Temp')
real = json.load(io.open(os.path.join(T, 'real_acc_same.json'), encoding='utf-8'))
mine = json.load(io.open(os.path.join(T, 'my_acc_same.json'), encoding='utf-8'))

# 兜底候选：trace 中 SET 的 NN.NN 字面量
try:
    _trace = io.open(os.path.join(T, 'full_trace.txt'), encoding='utf-8', errors='replace').read()
    CANDS = sorted(set(re.findall(r'= "(\d{1,3}\.\d{1,3})"', _trace)))
except OSError:
    CANDS = []

CRIBS = ['{"x":', '{"availWidth"', 'https://', 'TypeError', '["Google', 'statsig.', '{"x"', '{"']


def b64dec(s):
    pad = s + '=' * ((4 - len(s) % 4) % 4)
    try:
        return base64.b64decode(pad).decode('latin1')
    except Exception:
        return None


def xor(s, k):
    return ''.join(chr(ord(c) ^ ord(k[i % len(k)])) for i, c in enumerate(s))


def printable_ratio(p):
    return sum(1 for ch in p if 32 <= ord(ch) < 127) / len(p) if p else 0.0


def recover_key(cipher):
    """分层恢复槽密钥：返回 (明文, 密钥说明, 置信)。"""
    # 1) 无 XOR：密文本身可打印
    if printable_ratio(cipher) > 0.95:
        return cipher, '无XOR', 1.0
    # 2) crib 恢复
    for crib in CRIBS:
        if len(cipher) < len(crib):
            continue
        k = ''.join(chr(ord(cipher[i]) ^ ord(crib[i])) for i in range(len(crib)))
        # K 应为 4~5 字符周期（"NN.NN" 型）
        for period in (4, 5):
            if len(k) < period:
                continue
            key = k[:period]
            if all(k[i] == key[i % period] for i in range(len(k))):
                plain = xor(cipher, key)
                if printable_ratio(plain) > 0.9:
                    return plain, 'crib:%s(key=%s)' % (crib.strip('"'), key), 1.0
    # 3) trace 候选
    best = None
    for c in CANDS:
        p = xor(cipher, c)
        r = printable_ratio(p)
        if best is None or r > best[2]:
            best = (p, 'cand:%s' % c, r)
    if best and best[2] > 0.9:
        return best
    return None, '', 0.0


n_ok = n_cmp = 0
for k in real:
    rv, mv = real[k], mine.get(k)
    if isinstance(rv, float) or isinstance(mv, float):
        ok = isinstance(rv, float) and isinstance(mv, float) and rv == mv
        n_ok += ok
        print('%s %-8s RAW-FLOAT real=%r mine=%r' % ('✓' if ok else '✗', k, rv, mv))
        continue
    if mv is None:
        print('✗ %-8s 我方缺槽' % k)
        continue
    rc, mc = b64dec(rv), b64dec(mv)
    if rc is None or mc is None:
        print('✗ %-8s b64 解码失败' % k)
        continue
    rp, rk, rc_conf = recover_key(rc)
    mp, mk, mc_conf = recover_key(mc)
    if rp is None or mp is None:
        print('✗ %-8s 密钥未恢复 (real=%s/%.2f mine=%s/%.2f)' % (k, rk, rc_conf, mk, mc_conf))
        continue
    # 双方各自恢复的密钥若不同但明文都可打印 —— 直接比明文（同挑战 K 必同，密钥不同=有边恢复错）
    n_cmp += 1
    ok = rp == mp
    n_ok += ok
    print('%s %-8s [%s|%s] len %d vs %d' % ('✓' if ok else '✗', k, rk, mk, len(rp), len(mp)))
    print('     real: %r' % rp[:88])
    if not ok:
        print('     mine: %r' % mp[:88])
        for i, (a, b) in enumerate(zip(rp, mp)):
            if a != b:
                print('     首差异@%d real=%r mine=%r' % (i, rp[max(0, i-16):i+24], mp[max(0, i-16):i+24]))
                break
        else:
            s2 = min(len(rp), len(mp))
            print('     前 %d 相同; real尾=%r mine尾=%r' % (s2, rp[s2:][:40], mp[s2:][:40]))
print()
print('同挑战明文级一致: %d/%d (可判定 %d)' % (n_ok, len(real), n_cmp))
