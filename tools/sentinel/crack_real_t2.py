# -*- coding: utf-8 -*-
"""v2：约束求解——明文以 {" 开头、以 } 结尾、整体 json.loads 通过。"""
import base64, io, json, string, itertools

b = json.load(io.open(r'sentinel_bundle.json', encoding='utf-8'))
val = base64.b64decode(json.loads(b['token'])['t'])
print('真实 t: %d 字节' % len(val))

ALLOWED = set((string.printable).encode())

for klen in range(2, 13):
    cols = []
    feasible = True
    for j in range(klen):
        col = [val[i] for i in range(j, len(val), klen)]
        cands = [k for k in range(256) if all((c ^ k) in ALLOWED for c in col)]
        if not cands:
            feasible = False
            break
        cols.append(cands)
    if not feasible:
        continue
    # 约束：明文[0]=='{'，明文[1]=='"'
    if klen >= 2:
        cols[0] = [k for k in cols[0] if (val[0] ^ k) == ord('{')]
        cols[1] = [k for k in cols[1] if (val[1] ^ k) == ord('"')]
    total = 1
    for c in cols:
        total *= len(c)
    if total == 0 or total > 200000:
        print('klen=%d: 组合 %d，跳过' % (klen, total))
        continue
    print('klen=%d: 候选组合 %d' % (klen, total))
    found = False
    for combo in itertools.product(*cols):
        plain = bytes(c ^ combo[i % klen] for i, c in enumerate(val))
        if plain[-1] != ord('}'):
            continue
        try:
            s = plain.decode('utf-8')
            obj = json.loads(s)
            print('✓ 密钥: %r' % bytes(combo))
            print('明文前 300:', s[:300])
            io.open(r'real_accumulator.json', 'w', encoding='utf-8').write(
                json.dumps(obj, ensure_ascii=False, indent=1))
            print('已保存 real_accumulator.json，字段数:', len(obj))
            found = True
            break
        except Exception:
            continue
    if found:
        break
