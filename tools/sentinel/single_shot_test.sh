#!/bin/bash
# Sentinel 单次干净测试（风控冷却后执行一次，不做多次尝试）
# 流程：真实浏览器捕获（token 不消费）→ 同挑战数据准备 → Go 组装测试 → 结果落盘
set -u
T="C:/Users/13080/AppData/Local/Temp"
NODE="C:/Users/13080/.workbuddy/binaries/node/versions/22.22.2-3/node.exe"
PY="C:/Users/13080/.workbuddy/binaries/python/envs/default/Scripts/python.exe"
OUT="$T/single_shot_result.txt"
cd "$T"

echo "=== $(date) 单次干净测试 ===" > "$OUT"

# 1) 捕获（token 不消费）
timeout 220 "$NODE" instrumented_cap.js > /dev/null 2>&1
echo "capture-exit=$?" >> "$OUT"

# 2) 同挑战数据
"$PY" - <<'PYEOF' >> "$OUT" 2>&1
import base64, json, io, os
T = r'C:\Users\13080\AppData\Local\Temp'
cap = json.load(io.open(os.path.join(T, 'instrumented_capture.json'), encoding='utf-8'))
p_req = json.loads(cap['sentinelReq']['body'])['p']
resp = json.loads(cap['sentinelResp']['body'])
k = p_req.encode()
raw = base64.b64decode(resp['turnstile']['dx'])
plain = bytes(c ^ k[i % len(k)] for i, c in enumerate(raw))
json.dump(json.loads(plain), io.open(os.path.join(T, 'dx_instructions.json'), 'w', encoding='utf-8'), ensure_ascii=False)
json.dump(json.loads(cap['acc'][-1]), io.open(os.path.join(T, 'real_acc_same.json'), 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
io.open(os.path.join(T, 'real_rand.txt'), 'w').write('\n'.join(repr(x) for x in cap['vm']['rand']))
print('prep-ok: acc %d fields' % len(json.loads(cap['acc'][-1])))
PYEOF

# 3) 我的 VM 跑同挑战（产出我的 acc）
cd "$T/sentinelvm"
SENTINEL_RAND="$T/real_rand.txt" ./vm.exe > /dev/null 2>&1
echo "vm-exit=$?" >> "$OUT"
cd "$T"

# 4) 单次测试（A2: 我的 p + 真实 acc 模板 t + 真实 c；A3 对照）
cd "$T/final_a"
./fa.exe >> "$OUT" 2>&1

echo "=== 完成 $(date) ===" >> "$OUT"
cat "$OUT"
