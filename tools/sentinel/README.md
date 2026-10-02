# Sentinel 纯 Go 逆向 —— 研究报告（2026-10-02）

关联目标：摆脱浏览器 sidecar，让 OAIprism 上游走纯 Go 链路。
关联 issue：ranxi2001/sub2api#256 的登录/认证适配。

## 结论速览

| 链路层 | 纯 Go 可行性 | 状态 |
|---|---|---|
| Cloudflare TLS 指纹 | ✅ | uTLS / tls-client(Chrome_152) 实测 200 |
| session 换发 | ✅ | `GET /auth/session` + access_token cookie |
| requirements PoW（p_req） | ✅ | fnv1a + 25 元 config，服务端接受 |
| 取 c | ✅ | `POST sentinel.openai.com/backend-api/sentinel/req` |
| **dx 解密** | ✅ | `JSON.parse(XOR(atob(dx), p_req))` |
| **dx VM 解释器** | ✅ | 36 opcode 寄存器机器 |
| **t 生成** | ✅✅✅ | **与真实浏览器逐字节相同**（2026-10-02 12:38 捕获挑战，1628 字符）|
| 全新挑战变体（86-95 条指令） | 🔶 | 结构全通；变体专属采集项的环境保真度需逐变体校准 |

**决定性消融**（2026-10-02）：
1. 未消费真实 token + Go tls-client 重放 → **HTTP 200**（环境/传输层完全没问题）
2. 我的 VM 在捕获挑战上的 t 与真实浏览器 t **逐字节相同**（穷举 K='37.43' 解出真实 accJSON 比对，
   1221 字符原始串一致、17 字段键序一致）
3. 组装的 token 字符串与真实 token 逐字节一致

即 t 生成链路 100% 还原。未竟项：**挑战变体在演化**（83→95 条指令、15→17 字段），
每个变体的采集项组合不同，静态环境桩（env_real.json/env_template.json）对未捕获过的
变体可能产出错误采集值 → 需"捕获→离线对齐→单发"循环逐变体校准。

## 完整协议（全部实测还原）

```
token = {"p","t","c","flow":"prism_inference"}   （header openai-sentinel-token）

p（两种）:
  requirements = "gAAAAAC" + b64(JSON(config25)) + "~S"   # sentinel/req 请求体
    seed = ""+Math.random()（客户端随机）、difficulty = "0"
  enforcement  = "gAAAAAB" + b64(JSON(config25)) + "~S"   # 最终 token 的 p
    seed/difficulty 来自 sentinel/req 响应的 proofofwork
  算法: fnv1a(seed + b64) 前 N 位 hex <= difficulty；maxAttempts=5e5
  config25: [screen.w+h, ""+new Date, perf.memory.jsHeapSizeLimit, 迭代值,
             userAgent, R(scripts), ..., "hardwareConcurrency−16"(U+2212),
             R(keys(document)), R(keys(window)), perf.now, sid(uuid), ...]

c  = sentinel/req 响应 token 字段（一次性，重放 403）
dx = 响应 turnstile.dx（~20K 字符，加密的 VM 指令集）
  明文 = JSON.parse(XOR(atob(dx), p_req))     ← 密钥 = 我们发送的 p_req（服务端回显加密）
  L1(80-90条): 预装内建 → 环境采集 → 解内嵌 b64 块 → 替换队列(寄存器9)
  L2(300+条): 深度指纹 → 装载 blob2(5条)
  尾部: JSON.stringify(acc) → XOR(随机数) → RESOLVE

t  = btoa( JSON.stringify(acc) ⊕ key )    # key = 每次运行随机的 "NN.NN"（密钥空间仅 1e4！
                                          #   服务端可暴力穷举解密 → 内容级校验）
```

## VM 语义（寄存器机器，sdk.js 反混淆还原）

- 指令 `[op, args...]`：**op 是寄存器键**（内建闭包由 `_n` 的 IIFE 预载到 1-35，
  指令先 COPY 到随机浮点键，后续指令经由这些键调用）
- **队列就是寄存器 9**（JSON-PARSE 写 regs[9] 即换队列）
- 传参规则（细节关键，错一个就静默走偏）：
  - CALL(7)/ACALL(17)：解引用参数后调用
  - DEFINED-CALL(23)/EQ-CALL(20)/DELTA-CALL(21)/FUNCDEF(30) 闭包：**原样传参**
- RESOLVE(3) → `resolve(btoa(""+v))`；异常 → `btoa("步数: 错误")`；500ms 超时 → `""+步数`
- 采集面：screen 全属性、navigator（deviceMemory/vendor/platform/maxTouchPoints）、
  `Object.keys(localStorage)`（真实站点 9 个条目！）、fontRect 测量（Arial 19px +
  组合字符）、`performance.now` 多点 timing、Reflect.set 累积对象（15-17 字段）

## 实测对齐进度（同挑战 + 随机序列回放，2026-10-02 收官）

- **17/17 字段明文级一致**（零钩子：acc 值本身是 b64，同挑战共享槽密钥 → crib 恢复）
- **t 与真实浏览器逐字节相同**（crack_real_t3.py 穷举 K 空间解出真实 accJSON 对比）
- 关键修复（每一条都对应一个静默丢槽/值偏差的根因）：
  1. **错误传播语义**：callRaw 不得吞 panic —— JS 异常要传到最近 FCALL/ACALL catch
     （ipinfo 槽 = 9 连读全抛 TypeError，链变量被最后一次 catch 覆盖 + ADD 拼 key）
  2. **PROP 错误格式**：`Cannot read properties of undefined (reading 'KEY')`，
     尾部 key 由后续 ADD 拼接，panic 消息不能自带
  3. **ADD 数值语义**：两个 JSON number → 数值加法（0.7000000000000001+0.1=0.8，
     字符串拼接会产出 21 字节尾巴）
  4. **BIND 引用语义**：boundFn.recv 支持字符串/数组接收者；pop/shift 的变异
     要反映回接收者（版本槽 = split("/").pop().pop() 两次）
  5. **SCRIPT-FIND 返回全匹配字符串**（不是 str.match 的数组），随后 split('/').pop()
     得 SDK 版本号
  6. **document.location 字符串化即 href**（`""+location` = 25 字符 href）
  7. **localStorage 键集 = env_template 的解码值本身**（追加 envData 键会多 76 字节）；
     setItem 的新键要出现在 Object.keys 里
  8. **fontRect 逐捕获校准**（同浏览器不同页面状态测量值不同：17.78/15 vs 34.47/22.5）
  9. **随机回放偏移**：捕获序列前段是 SDK 层消耗的（PoW 种子等），VM 回放起点 =
     random_2 槽值在序列中的索引 - 1（verify_pipeline 自动定位）

## 服务端校验推断

- t 的 XOR 密钥是每次随机的 "NN.NN"（1e4 空间）→ 服务端可穷举解密 → **内容级校验**
- 校验点推测：字段结构、timing 合理性、环境一致性（UA/脚本/存储），
  即 Cloudflare Turnstile 式风险评分——这解释了为什么社区项目
  （sora2api、codex-register）全部收敛到"浏览器算 token、Go 发请求"

## 可行落地架构（下一步）

**token oracle**：浏览器只做 `SentinelSDK.token()` 签发（~100ms，9 分钟缓存无效——
token 一次性，每请求一次），Go 承担全部 API 传输。相比现在的请求转发 sidecar：
无长连接、无流转发、崩溃面小一个数量级。

## 文件

| 文件 | 说明 |
|---|---|
| `vm.go` | dx VM 解释器（36 opcode，随机序列回放、Reflect.set 追踪）|
| `probe_main.go.example` | 完整纯 Go 链路探针（换发→PoW→req→dx→VM→API，单发）|
| `vm_standalone_main.go.example` | 独立 VM 运行器（--trace 全轨迹 + setLog）|
| `instrumented_cap.js` | 真实浏览器仪器化捕获（JSON.stringify/Math.random/perf.now；API 请求 abort 不消费 token）|
| `verify_pipeline.py` | 全链路核对：捕获→解密→VM（随机回放+偏移自动定位）→委托 diff |
| `diff_same_challenge.py` | 同挑战逐槽明文 diff（crib 恢复槽密钥，零钩子依赖）|
| `diff_plaintext.py` | 双 btoa 日志版逐槽 diff（需浏览器钩 btoa 的捕获）|
| `crack_real_t3.py` | 穷举真实 token 的 t 密钥 → 解 accJSON → 与我的输出逐字节对比 |
| `decode_slots.py` | 我方 acc 槽自解码（trace 密钥候选）|
| `check_token_str.py` | 重组 token 字符串 vs 捕获 token 的逐字节一致性 |
| `crack_real_t2.py` | （旧）t 内层 XOR 密钥约束求解 |
| `precision_align.py` | （旧）同挑战累积对象逐字段 diff |

环境数据依赖（运行时同目录）：`env_real.json`（screen/navigator/scripts/windowKeys/
localStorage/docTitle/fontRect —— 由捕获脚本 harvest；fontRect 需逐捕获更新）。
