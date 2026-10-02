# -*- coding: utf-8 -*-
"""精密对齐：同一挑战下，真实 VM vs 我的 Go VM 的累积对象逐字段 diff。"""
import base64, io, json, string, subprocess, itertools

cap = json.load(io.open('precision_capture.json', encoding='utf-8'))

# 1) 真实数据
req_body = json.loads(cap['sentinelReq']['body'])
p_req = req_body['p']
resp = json.loads(cap['sentinelResp']['body'])
dx = resp['turnstile']['dx']
seed = resp['proofofwork']['seed']
diff = resp['proofofwork']['difficulty']
c_tok = resp['token']
real_tok = json.loads(cap['apiReq']['sentinelToken'])
print('真实 token 字段:', list(real_tok.keys()), '| p len=%d t len=%d' % (len(real_tok['p']), len(real_tok['t'])))
print('挑战: seed=%s difficulty=%s' % (seed, diff))

# 2) 解密 dx（密钥 = p_req）
dx_raw = base64.b64decode(dx)
k = p_req.encode()
plain = bytes(c ^ k[i % len(k)] for i, c in enumerate(dx_raw))
l1 = json.loads(plain)
print('dx 解密: %d 条指令' % len(l1))
io.open('dx_instructions.json', 'w', encoding='utf-8').write(json.dumps(l1, ensure_ascii=False))

# 3) 解出真实累积对象：t 的内层 XOR 密钥（约束求解）
val = base64.b64decode(real_tok['t'])
print('真实 t: %d 字节' % len(val))
ALLOWED = set(string.printable.encode())
solved = None
for klen in range(2, 13):
    cols, ok = [], True
    for j in range(klen):
        col = [val[i] for i in range(j, len(val), klen)]
        cands = [x for x in range(256) if all((c ^ x) in ALLOWED for c in col)]
        if not cands:
            ok = False
            break
        cols.append(cands)
    if not ok:
        continue
    if klen >= 2:
        cols[0] = [x for x in cols[0] if (val[0] ^ x) == ord('{')]
        cols[1] = [x for x in cols[1] if (val[1] ^ x) == ord('"')]
    total = 1
    for cc in cols:
        total *= len(cc)
    if total == 0 or total > 500000:
        continue
    for combo in itertools.product(*cols):
        p2 = bytes(c ^ combo[i % klen] for i, c in enumerate(val))
        if p2[-1:] != b'}':
            continue
        try:
            obj = json.loads(p2.decode('utf-8'))
            solved = (bytes(combo), obj)
            break
        except Exception:
            continue
    if solved:
        break
if not solved:
    print('✗ 真实 t 内层密钥未解出')
    raise SystemExit(1)
rkey, real_acc = solved
print('真实内层密钥: %r | 累积对象 %d 字段' % (rkey, len(real_acc)))

# 4) 内层密钥在指令流的哪里？找 SET 字面量（形如 r.NN 的浮点）
def find_literal(instrs, key):
    ks = key.decode()
    hits = []
    for i, x in enumerate(instrs):
        for a in x[1:]:
            if isinstance(a, str) and a == ks:
                hits.append((i, x))
            elif isinstance(a, float) and ('%g' % a) == ks:
                hits.append((i, x))
    return hits

def walk(instrs, depth=0, seen=None):
    """展开所有子指令集（找 SET 字面量 + JSON-PARSE 队列替换）。"""
    if seen is None:
        seen = set()
    hits = find_literal(instrs, rkey)
    if hits:
        print('内层密钥字面量命中 @', [(i, x[:2]) for i, x in hits][:4])
    for i, x in enumerate(instrs):
        if isinstance(x, list):
            for a in x:
                if isinstance(a, list) and id(a) not in seen:
                    seen.add(id(a))
                    walk(a, depth + 1, seen)

# 4) 用我的 VM 跑同一挑战（vm.exe 读 dx_instructions.json）
r = subprocess.run([r'sentinelvm\vm.exe'], capture_output=True, text=True, errors='replace')
print(r.stdout[-300:])
my_t = io.open('t_value.txt', encoding='ascii').read().strip()
my_val = base64.b64decode(my_t)
my_acc = json.loads(bytes(c ^ rkey[i % len(rkey)] for i, c in enumerate(my_val)))
print('我的累积对象: %d 字段' % len(my_acc))

# 5) 逐字段对比
print()
print('%-9s %-34s | %-9s %s' % ('真实key', '真实值', '我的key', '我的值'))
def dec(v):
    if isinstance(v, (int, float)):
        return 'num(%s)' % v
    if not isinstance(v, str):
        return repr(v)[:36]
    if len(v) % 4:
        return repr(v)[:36]
    try:
        bs = base64.b64decode(v)
        s = bs.decode('ascii')
        try:
            inner = base64.b64decode(s)
            return 'b2(%r)' % inner[:26]
        except Exception:
            return 'b1(%r)' % s[:28]
    except Exception:
        return repr(v)[:36]
rk, mk = list(real_acc.items()), list(my_acc.items())
for i in range(max(len(rk), len(mk))):
    a = rk[i] if i < len(rk) else ('-', '-')
    c = mk[i] if i < len(mk) else ('-', '-')
    print('%-9s %-36s | %-9s %s' % (a[0], dec(a[1])[:36], c[0], dec(c[1])[:40]))
io.open('acc_real_mine.txt', 'w', encoding='utf-8').write(
    'REAL:\n' + json.dumps(real_acc, indent=1) + '\n\nMINE:\n' + json.dumps(my_acc, indent=1))
