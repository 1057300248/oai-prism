# Codex 与动态模型目录

本轮完成模型/思考档位声明发现、按账号能力路由、Codex 0.160.0 标准 Responses 工具闭环和真实 CLI 协议验收。只修改 oai-prism，不修改 NewAPI，不部署生产。核心集成提交 `efb7c29772dccf5f0f570b8288051621876c797b` 已通过 GitHub Actions；最新分支状态以 PR Checks 为准。

## 1. 能力分层

| 状态 | 能证明什么 |
|---|---|
| configured | 运营者写在配置中；不是动态发现或推理验证 |
| declared | 在指定账号的模型元数据中观察到；不是模型实际能力测试 |
| stale / unavailable | 声明过期、未能取得或不满足请求，不用于动态模型路由 |
| 真实 CLI fixture 通过 | Codex 的请求/解析/本地工具确实走通；不代表真实模型会正确选择这些操作 |
| live inference / semantic validation | 必须另行执行经授权的真实上游测试。本轮不进行真实模型消费 |

不把HTTP200、菜单模型名或模型自报身份当成能力验证。思考档位按模型与账号记录，不假定全部模型都有low/medium/high，也不自动降级指定档位。

## 2. 网关开关

在现有私有配置的 `facade.gateway` 下增加：

```yaml
codex_tools: true
prompt_tools: true
catalog:
  refresh_interval: 5m
  max_age: 1h
  publish: [YOUR_OBSERVED_PUBLIC_MODEL_ID]
  sources:
    - name: account-a-models
      account_id: YOUR_EXISTING_ACCOUNT_POOL_ID
      path: /var/lib/oaiprism/catalog/account-a.json
```

`account_id` 必须对应现有账号池，且由运营者配置，不允许终端请求、client_metadata 或 user 字段指定。每个元数据源只代表一个账号。也可将 `path` 换为运营者控制的 `url`（HTTPS或字面回环HTTP），通过 `bearer_env` 指定目录服务凭据环境变量。它读取下述标准化快照，不猜测任意上游接口形状；禁止HTTP重定向。

默认未配置sources时保持静态模型列表。新发现模型只有加入publish后才公开；不自动修改NewAPI价格、权限、渠道或生产配置。

### 标准化快照

```json
{
  "observed_at": "<实际观察时间，RFC3339>",
  "models": [{
    "id": "<对外模型ID>",
    "upstream_id": "<已观察到的真实模型ID>",
    "display_name": "<显示名>",
    "reasoning_efforts": ["<该账号实际声明的档位>"],
    "default_effort": "<实际声明的默认档位>",
    "context_window": 0,
    "input_modalities": ["text"],
    "capabilities": {}
  }]
}
```

这是字段结构说明，不是可以直接导入的真实目录。未知窗口填0，没有证据的档位/模态/能力不填。Codex导出要求已知默认档位和正数窗口，信息不完整的模型不会被伪装成完整Codex模型。元数据不导入上游指令模板、审批规则、MCP地址或任意客户端配置。

刷新失败保留最后有效数据直至max_age过期，不把一次429或网络故障当成永久模型下架。已纳入动态目录的模型即便与静态别名同名，目录不可用时也不会退回任意账号。每次解析只复制和聚合一次目录快照，避免模型数量增加时出现二次方复制开销。

## 3. 从当前已授权浏览器观察目录

`tools/model-catalog/observe.mjs` 是只读辅助工具：不登录、不读取/复制Cookie、不调用模型、不绕过访问控制，也不会自动选择身份不明确的账号。

为单个账号准备已登录、仅回环CDP可达的专用浏览器，指定已经确认的元数据响应前缀：

```bash
node tools/model-catalog/observe.mjs \
  --cdp http://127.0.0.1:9222 \
  --response-prefix 'https://YOUR_OBSERVED_METADATA_HOST/YOUR_OBSERVED_PATH' \
  --output /var/lib/oaiprism/catalog/account-a.json
```

需要安装playwright-core，或用 `--playwright-module /absolute/path/to/playwright-core/index.mjs` 指定已安装模块。观察器只接受一个匹配的Prism页面；账号/页面不明确会拒绝。正常刷新已授权页面后，监听选定响应中的 `prism_codex_models`；可用 `--pointer /path/to/models` 指定实际JSON路径。

未知长度或超过2MiB的响应会跳过，解析失败保留旧快照。标准化与监听代码已经实现，但没有在用户真实浏览器验收具体元数据路径。不能填入猜测路径就宣称已发现当前真实模型。

合法捕获文件可离线转换：

```bash
node tools/model-catalog/observe.mjs \
  --from-json /private/observed-metadata.json \
  --pointer /path/to/models \
  --output /var/lib/oaiprism/catalog/account-a.json
```

离线转换以文件修改时间保留观察时效，不因重新导入就伪造新观察时间；复制文件时需维护正确来源时间。不要将包含认证信息的原始捕获文件上传GitHub。

## 4. 对外目录与Codex配置

认证后的接口：

- `/v1/models`：静态模型与已公开、新鲜的动态模型。
- `/v1/model-capabilities`：能力声明及过期状态，不暴露账号ID、凭据、目录源路径。
- `/v1/codex/models`：面向固定Codex 0.160.0的保守ModelInfo目录。

后两个是网关扩展接口，定制NewAPI不一定转发。可从网关受限网络获取目录文件，再将**推理请求**配置为经过NewAPI。不要把网关渠道Key混作终端用户的NewAPI Key。

首次测试使用单独的 `CODEX_HOME`，在该目录放入 `config.toml` 和导出的 `models.json`；不要覆盖原 `~/.codex/config.toml`。以下是独立config.toml的顶层配置，不是旧版嵌套profile示例：

```toml
model_provider = "oaiprism"
model = "YOUR_PUBLISHED_MODEL_ID"
model_reasoning_effort = "YOUR_DECLARED_EFFORT"
model_catalog_json = "/absolute/path/to/isolated-codex-home/models.json"
web_search = "disabled"

[model_providers.oaiprism]
name = "oai-prism via NewAPI"
base_url = "https://YOUR_NEWAPI_ORIGIN/v1"
wire_api = "responses"
env_key = "YOUR_NEWAPI_KEY_ENV"
```

模型名必须与NewAPI允许/映射的模型名一致。通过 `CODEX_HOME=/absolute/path/to/isolated-codex-home codex` 启动，Windows在当前PowerShell会话设置同名环境变量即可。此示例不替用户修改审批和沙箱规则。目录在客户端启动时加载，网关刷新不会立即修改已启动客户端菜单。

## 5. 已实现的工具协议边界

`codex_tools` 必须显式启用，不根据提示词包含exec/apply_patch字样自动触发。支持标准Responses顶层function/custom工具、一级namespace、命名工具选择、调用与结果回放。结果可以是文本或支持的内容块数组；call_id保持配对，不能拿function输出冒充custom输出。

custom工具支持文本、Go兼容正则完整匹配，以及指纹固定的Codex 0.160.0 apply_patch Lark语法。任意Lark语法不冒充支持；新版Codex修改语法时需更新验证和回归。命令、文件、补丁由Codex客户端执行，网关不执行客户端传来的JavaScript/shell或直接读写客户端文件。

继续采用提示词→严格结果校验的工具适配，不是上游原生约束解码。保留system/developer约束、项目上下文、工具结果和message phase，不照搬EmpFish01中丢弃角色约束或按字节删除中段历史的实现。

`reasoning.summary` 接受auto/concise/detailed偏好；只有上游返回可用公开摘要时才输出，不凭空生成。`include: ["reasoning.encrypted_content"]` 可作为可选输出请求出现，没有真实加密内容就不返回；输入中的非空加密推理状态明确拒绝，不伪造解密或跨上游复用。

client_metadata只作为经过限制的诊断字段保留在请求对象，不作为身份、账号选择、系统指令或上游参数。

## 6. 真实客户端验证

`tools/codex_contract_test.py` 与 `tools/codex-fixture` 使用真实Codex 0.160.0，在独立临时仓库完成：读取calc.py→custom apply_patch→运行单元测试→只读review。测试核对实际文件、客户端命令返回及review前后工作树。上游是固定测试计划，故障和review发现预先定义；这证明协议、客户端执行和工作树变化，不证明模型会自主发现同一个问题。

测试还要求两项真实系统拒绝：read-only模式不能写工作区，workspace模式不能写工作区外的兄弟目录。测试使用独立CODEX_HOME、假Key及不携带GitHub/模型凭据的环境。没有用danger-full-access或跳过审批来使测试变绿。

GitHub Ubuntu runner的AppArmor可能阻止bubblewrap创建user namespace。`tools/codex_ci_sandbox.sh` 仅在一次性Linux GitHub Actions runner加载限定 `/usr/bin/bwrap` 的临时配置，结束时移除；不关闭全局AppArmor、不改全局sysctl，也不移除Codex的文件系统、网络或seccomp隔离。该脚本不是生产部署脚本。负向写入测试必须一起通过。

核心提交 `efb7c297` 的完整验证记录：
https://github.com/1057300248/oai-prism/actions/runs/37119424375

该运行包含318项Go测试、6项目录标准化测试、真实CLI流程、沙箱边界、全仓race和Linux amd64/arm64构建。后续性能回归增加1项Go测试；最终数量以最新Gateway Cloud Verification逐测试结果为准。

常规工作流 `.github/workflows/codex-contract.yml` 保留真实客户端验证；原SDK、上下文压缩/缓存和Go全量检查继续运行。临时有写权限的集成/诊断工作流已经移除。

## 7. 后续阶段与明确限制

本轮不是Codex所有功能完整上线。通用Files API、真实图像理解与媒体预算、任意自定义语法、Responses-lite/additional_tools、完整MCP/子代理/交互式进程矩阵、官方托管 `@codex review` 和权限审批auto_review均不据此宣称完成。

图片内容块可在已有受支持的内联路径保留，但真实视觉模型没有验收；图片仍受现有用量和上下文预算限制，Codex目录保守导出text模态。自动模型目录是声明发现与刷新，不是自动低成本穷举/验证所有模型，也不是实际缓存命中证据。

下一阶段应执行经授权的真实上游小流量能力验证，再扩大图片、文件及更多工具场景。生产NewAPI的特殊字段/目录转发、计费、长任务恢复及沙箱回收仍需现场验收，不能用固定fixture通过来替代。

一手参考：OpenAI Codex `rust-v0.160.0` 的 `codex-rs/protocol/src/openai_models.rs`、`codex-rs/protocol/src/models.rs`、`codex-rs/core/assets/tools/apply_patch.lark`；https://developers.openai.com/codex/config-reference/ 和 https://developers.openai.com/codex/concepts/sandboxing 。原作者归属不变，未整包复制第三方fork。
