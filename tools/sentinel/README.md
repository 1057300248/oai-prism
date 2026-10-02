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
| **dx VM 解释器** | ✅ | 36 opcode 寄存器机器，同挑战执行路径与真实 100% 一致 |
| **t 内容过风控** | ❌ 未竟 | 结构 95% 对齐，内容级风控仍拒绝（见下）|

**决定性消融**：未消费的真实 token + Go 客户端 → **HTTP 200**（项目真实创建）。
即 Go 的 TLS/h2/头指纹完全够用，token 一次性（重放 403），唯一缺口是自产 t 的内容。

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

## 实测对齐进度（同挑战 + 随机序列回放）

- 15 字段全部键名一致、执行路径一致（455-480 步 resolved）
- 9/15 字段字节数完全一致；剩余 6 个为小文本/二进制内容差
  （timing 值、比值测量 "1.43"、部分集合长度）
- 我的 t 长度 1360 vs 真实 1548（结构性接近，内容级风控未过）

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
| `probe_main.go.example` | 完整纯 Go 链路探针（换发→PoW→req→dx→VM→API）|
| `vm_standalone_main.go.example` | 独立 VM 运行器（--trace 全轨迹 + setLog）|
| `instrumented_cap.js` | 真实浏览器仪器化捕获（钩 JSON.stringify/Math.random）|
| `fresh_token_cap.js` | 未消费 token 捕获（消融用）|
| `crack_real_t2.py` | t 内层 XOR 密钥约束求解 |
| `precision_align.py` | 同挑战累积对象逐字段 diff |

环境数据依赖（运行时同目录）：`env_real.json`（真实浏览器 screen/navigator/
scripts/windowKeys/localStorage 等，由 harvest 脚本生成）。
