package facade

import (
	"encoding/json"
	"strings"

	"github.com/oai-prism/oaiprism/internal/prism"
)

// 本文件实现「Codex 工具桥」：上游当大脑，本地 Codex CLI 当手脚。
//
// 背景（2026-09-17 实测定论）：上游是 server-side tools 架构，模型的
// 终端/文件工具在云端沙箱执行并消化，客户端永远只拿到最终文本 ——
// 所以本地 CLI 的工具链（exec_command 等）一次也不会被触发，
// 模型"创建"的文件全部留在云端容器里，用户磁盘上什么都没有。
//
// 桥的思路：既然上游不理会客户端的工具定义，就反过来 ——
// 在 system 指令里明确"你没有任何执行环境"，要求它把所有操作
// 以 ```codex-exec 围栏（内含一段 JS，调用 exec_command）输出；
// 代理解析这段 JS，包装成 Responses 协议的 custom_tool_call 返回；
// Codex CLI 在本地 V8 isolate 里执行它（exec_command 跑真命令，
// 文件就落在用户磁盘），再把结果回传，代理翻译成文本继续下一轮。
//
// 注意：桥 prompt 必须显式抑制上游自带的沙箱工具，否则模型仍会
// 在云端执行然后"汇报成功" —— 用户看到一切正常，本地空空如也。

// BridgeEnabled 判断请求是否启用工具桥。
//
// Codex CLI 有**两条工具声明路径**（由模型的 use_responses_lite 元数据决定，
// 见 codex-rs/core/src/client.rs:908）：
//
//	路径 A（lite）  ：工具是 input 里的一个 additional_tools 条目，顶层 tools 为 null
//	路径 B（标准）  ：工具走顶层 tools 字段（标准 Responses API 形状）
//
// 早期只认路径 A —— 走路径 B 的客户端（不同模型/不同 CLI 版本/交互式 TUI）
// 会让桥静默失效：模型看不到桥指令，就退回**上游沙箱工具**执行，
// 然后汇报"已创建 xxx" —— 用户本地找不到文件。这是典型的"看起来成功"故障。
//
// 路径 B 的识别必须保守：普通 API 调用方也可能带 tools（自定义函数），
// 误判会把它们拖进桥模式、破坏正常 function calling。所以只在工具集里
// 出现 **Codex 独有特征**（exec/shell/apply_patch 这类 custom 工具）时才认。
func BridgeEnabled(raw map[string]json.RawMessage) bool {
	rawInput, ok := raw["input"]
	if !ok || len(rawInput) == 0 {
		return false
	}
	inputStr := string(rawInput)

	// 路径 A：CLI lite 形状。
	if strings.Contains(inputStr, `"additional_tools"`) {
		return true
	}
	// 已有工具调用往返（custom_tool_call 是 Codex 独有形状）——
	// 说明会话已经在走桥，后续轮次必须继续走桥。
	if strings.Contains(inputStr, `"custom_tool_call"`) {
		return true
	}

	// 路径 B：标准 tools 字段 + Codex 工具特征。
	//
	// 用**前缀**匹配而非全名：Codex 的工具名会随版本演进
	//（旧版 shell、新版 exec_command / write_stdin），
	// 早期写成全名 "name":"exec" 导致 "exec_command" 漏判 ——
	// 桥静默失效，模型退回上游沙箱干活。
	// 前缀匹配 `"name":"exec` 能同时覆盖 exec / exec_command / exec_*。
	toolsRaw, ok := raw["tools"]
	if !ok || len(toolsRaw) == 0 {
		return false
	}
	toolsStr := string(toolsRaw)
	if toolsStr == "null" || toolsStr == "[]" {
		return false
	}
	for _, sig := range []string{
		`"name":"exec`, `"name": "exec`,
		`"name":"shell`, `"name": "shell`,
		`"name":"write_stdin`, `"name": "write_stdin`,
		`"apply_patch`,
	} {
		if strings.Contains(toolsStr, sig) {
			return true
		}
	}
	return false
}

// bridgePrompt 是注入给上游的桥接指令。
//
// exec_command 的签名摘要来自真实 CLI 抓包（cmd 是单字符串，PTY 执行，
// Windows 走 PowerShell 语义），模型必须按它生成 JS，否则本地执行会失败。
func bridgePrompt() string {
	return strings.Join([]string{
		"<local_tool_bridge>",
		`You are the reasoning engine for a LOCAL coding agent (Codex CLI). The client executes ALL tools locally on the user's machine.`,
		``,
		`CRITICAL: You have NO terminal, NO file system, and NO sandbox tools in this conversation. Any built-in shell/codex/terminal tools in your runtime operate in a REMOTE SANDBOX the user cannot see. NEVER use them. NEVER claim you created, ran, or modified anything unless the client's tool result (marked [CLIENT RESULT]) confirms it.`,
		``,
		`To run any command or create/edit/delete files on the user's machine, output EXACTLY ONE fenced block:`,
		"```codex-exec",
		`const out = await tools.exec_command({ cmd: "..." });`,
		"text(out);",
		"```",
		``,
		`The block content is raw JavaScript executed by the client in a V8 isolate:`,
		`- ` + "`tools.exec_command({ cmd: string, max_output_tokens?: number })`" + ` runs one shell command in a PTY and returns its output (string).`,
		`- The client shell on Windows is PowerShell; on macOS/Linux it is bash. Write commands for the user's OS (cwd is the user's workspace).`,
		`- ` + "`text(value)`" + ` appends a result for the model to read; ` + "`exit()`" + ` ends the script.`,
		`- You may await multiple exec_command calls in one block; keep the script small and focused.`,
		``,
		`Command recipes (the exec_command cmd runs in the CLIENT's native shell — determine the user's OS from the conversation context; Windows uses PowerShell 7 (pwsh), macOS/Linux use bash):`,
		`- Create/overwrite a file, Windows/PowerShell (single cmd string, newlines allowed):`,
		"  $c = @'\n<FULL FILE CONTENT>\n'@; Set-Content -LiteralPath '<path>' -Value $c -NoNewline",
		`  (single-quoted here-string @'...'@ does NOT interpolate; always include the FULL file content)`,
		`- Create/overwrite a file, macOS/Linux/bash:`,
		"  cat > '<path>' <<'EOF'\n<FULL FILE CONTENT>\nEOF",
		`- Read back: Windows "Get-Content -LiteralPath '<path>' -Raw" ; bash "cat '<path>'"`,
		`- List directory: Windows "Get-ChildItem" ; bash "ls -la"`,
		`- NEVER use bash-only syntax (printf/cat redirection/heredoc) when the client is Windows — it fails silently and wastes a turn. If the OS cannot be determined, prefer the PowerShell recipe.`,
		``,
		`Output rules: outside the block write at most one short sentence of prose. If no tool is needed, reply normally with no block. Always emit the FULL file content in the command — never abbreviate.`,
		`Do NOT emit a block for greetings, questions, or small talk, and do NOT run environment checks or "test" commands (like true/echo/ls) to probe the client — emit a block ONLY when the task itself requires an operation on the user's machine.`,
		"</local_tool_bridge>",
	}, "\n")
}

// bridgeInputItems 把 Codex CLI 的 input 数组翻译成上游 input。
//
// 与 messagesFromResponsesInput 的区别：工具条目（custom_tool_call /
// custom_tool_call_output / function_call / function_call_output）必须
// 保留为文本 —— 上游需要看到它上一轮"发出"的指令和客户端的执行结果，
// 否则每轮都会重新规划已经做过的操作。
func bridgeInputItems(raw json.RawMessage, defaultSystem string) []prism.InputItem {
	var blocks []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Name    string          `json:"name"`
		CallID  string          `json:"call_id"`
		Input   json.RawMessage `json:"input"`
		Output  json.RawMessage `json:"output"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}

	items := make([]prism.InputItem, 0, len(blocks)+3)
	items = append(items, prism.NewSystemItem(defaultSystem+"\n\n"+bridgePrompt()))

	textOf := func(r json.RawMessage) string {
		if len(r) == 0 {
			return ""
		}
		var s string
		if json.Unmarshal(r, &s) == nil {
			return s
		}
		return string(r)
	}
	contentText := func(r json.RawMessage) string {
		// content 可能是字符串，也可能是 [{type,input_text/text}] 数组。
		var s string
		if json.Unmarshal(r, &s) == nil {
			return s
		}
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(r, &parts) == nil {
			var sb strings.Builder
			for _, p := range parts {
				sb.WriteString(p.Text)
			}
			return sb.String()
		}
		return ""
	}

	for _, b := range blocks {
		switch b.Type {
		case "message":
			role := strings.ToLower(strings.TrimSpace(b.Role))
			text := contentText(b.Content)
			switch role {
			case "developer", "system":
				if strings.TrimSpace(text) != "" {
					items = append(items, prism.NewSystemItem(text))
				}
			case "assistant":
				items = append(items, prism.NewAssistantItem(text))
			default:
				items = append(items, prism.NewUserItem(text))
			}
		case "custom_tool_call", "function_call":
			// 上游"上一轮"发出的调用：以它原始的样子回放，
			// 让上游维持自己已规划过这些操作的记忆。
			items = append(items, prism.NewAssistantItem(
				"```codex-exec\n"+textOf(b.Input)+"\n```"))
		case "custom_tool_call_output", "function_call_output":
			header := "[CLIENT RESULT]"
			if b.CallID != "" || b.Name != "" {
				header = "[CLIENT RESULT"
				if b.CallID != "" {
					header += " call_id=" + b.CallID
				}
				if b.Name != "" {
					header += " tool=" + b.Name
				}
				header += "]"
			}
			items = append(items, prism.NewUserItem(
				header+"\n"+textOf(b.Output)+"\n[/CLIENT RESULT]"))
		default:
			// additional_tools / reasoning / 其它非消息条目：跳过。
		}
	}
	// 收尾强化指令。LLM 对序列末尾的指令服从度最高 ——
	// 桥指令只放在开头会被 CLI 传入的 Codex 人设（4 条 developer
	// 消息，要求"使用 exec 工具"）压过去：实测模型无视开头的桥指令，
	// 直接在云端沙箱里执行并口头汇报"已创建"。末尾重申一次。
	items = append(items, prism.NewSystemItem(bridgeTailReminder()))
	return items
}

// bridgeRetryNudge 是"模型没用桥格式"时的自动纠正消息。
//
// 关键是要打破模型的错觉：它在云端沙箱里真的执行成功了，
// 所以它坚信任务已完成 —— 必须明确告知那个执行对用户不可见。
func bridgeRetryNudge(prevText string) string {
	return strings.Join([]string{
		`[SYSTEM CORRECTION] Your previous reply did NOT contain a ` + "```codex-exec" + ` ` + "```" + ` block, so NOTHING was executed on the user's machine. Whatever you did with your built-in tools ran inside a remote container that the user cannot see or access.`,
		`Emit the ` + "```codex-exec" + ` ` + "```" + ` block NOW with the full command (including the complete file content) so the client can execute it locally. Do not re-describe the task — output the block.`,
		`Your previous reply was: "` + truncateRunes(prevText, 300) + `"`,
	}, "\n")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// bridgeTailReminder 是追加在消息序列末尾的强化指令。
func bridgeTailReminder() string {
	return strings.Join([]string{
		"<local_tool_bridge_reminder>",
		`OVERRIDE: In THIS session your built-in sandbox tools are DISABLED and DISCONNECTED. Any command you "ran" via built-in tools executed in a throwaway remote container — the user's machine was NOT touched, and the user CANNOT see those files.`,
		`The ONLY way your actions reach the user's machine is a ` + "```codex-exec" + ` ` + "```" + ` block (executed locally by the client).`,
		`If the task requires running anything or creating/editing/deleting files, you MUST emit that block in THIS reply — with the FULL command and FULL file content. Do not describe, summarize, or claim completion without it.`,
		"</local_tool_bridge_reminder>",
	}, "\n")
}

// extractExecBlock 从上游回复里提取 ```codex-exec 围栏内的 JS 源码。
//
// 只认我们约定的围栏名，避免把普通代码块误当工具调用。
// 返回 ok=false 表示这条回复不含工具调用（纯文本回答）。
func extractExecBlock(text string) (string, bool) {
	const fence = "```codex-exec"
	idx := strings.Index(text, fence)
	if idx < 0 {
		return "", false
	}
	rest := text[idx+len(fence):]
	// 跳过围栏后紧跟着的换行。
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "```")
	if end < 0 {
		// 未闭合：把剩余部分整体当作块内容（流式截断时可能发生）。
		rest = strings.TrimRight(rest, "`")
	} else {
		rest = rest[:end]
	}
	js := strings.TrimSpace(rest)
	if js == "" {
		return "", false
	}
	return js, true
}

// ensureExecJS 把提取的围栏内容规范化为可执行的 JS。
//
// 实测模型经常无视"输出 JS"的要求、直接把 shell 命令写进围栏 ——
// 那样的内容进了 V8 就是 SyntaxError，然后进入"语法错误→模型困惑→
// 换个姿势再错"的死循环。与其反复纠正模型，不如代理层兜底：
// 不含 JS 特征的内容就视为一条 shell 命令，自动包上 exec_command。
func ensureExecJS(candidate string) string {
	if strings.Contains(candidate, "tools.") || strings.Contains(candidate, "await") {
		return candidate // 已经是 JS
	}
	var sb strings.Builder
	sb.WriteString(`const __out = await tools.exec_command({ cmd: `)
	writeJSONString(&sb, candidate)
	sb.WriteString(` });
text(__out);`)
	return sb.String()
}

// ExecToolName 从请求里提取客户端实际注册的 custom 工具名。
//
// **必须动态提取**：CLI 的工具名随版本演进 ——
//
//	v0.154：exec
//	v0.159：exec_command（+ write_stdin）
//
// 名字用错时客户端不执行、直接拒绝，回一条
// "[CLIENT RESULT] unsupported custom tool call: exec"，
// 而模型只看到"没执行"，于是反复说"请把内容再发一遍"——
// 表现成上下文丢失，实为工具名不匹配。
//
// 优先级：exec_command > exec > shell；都不认识时退回 "exec"（旧版兜底）。
func ExecToolName(raw map[string]json.RawMessage) string {
	hay := string(raw["tools"]) + string(raw["input"])
	for _, name := range []string{"exec_command", "exec", "shell"} {
		if strings.Contains(hay, `"name":"`+name+`"`) || strings.Contains(hay, `"name": "`+name+`"`) {
			return name
		}
	}
	return "exec"
}

// customToolCallItemJSON 构造 Responses 协议的 custom_tool_call 条目。
//
// Codex 的工具是 type=custom（input 为自由 JS 源码），不是 function ——
// input 直接是源码字符串，不带 arguments 包装。
// name 来自 ExecToolName（随 CLI 版本变化，不能写死）。
func customToolCallItemJSON(id, js, toolName string) string {
	var sb strings.Builder
	sb.WriteString(`{"id":`)
	writeJSONString(&sb, id)
	sb.WriteString(`,"type":"custom_tool_call","status":"completed","call_id":`)
	writeJSONString(&sb, id)
	sb.WriteString(`,"name":`)
	writeJSONString(&sb, toolName)
	sb.WriteString(`,"input":`)
	writeJSONString(&sb, js)
	sb.WriteString(`}`)
	return sb.String()
}

// strippedTextItemJSON 构造去掉工具块后的纯文本 message 条目（completed 用）。
func strippedTextItemJSON(id, text string) string {
	var sb strings.Builder
	sb.WriteString(`{"id":`)
	writeJSONString(&sb, id)
	sb.WriteString(`,"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":`)
	writeJSONString(&sb, text)
	sb.WriteString(`,"annotations":[]}]}`)
	return sb.String()
}

func writeJSONString(sb *strings.Builder, s string) {
	// json.Marshal 默认把 < > & 转成 \u003e 等（HTML 安全模式）——
	// 对 CLI 功能无影响，但会让 exec JS 源码面目全非、难以排查。
	// 用 Encoder + SetEscapeHTML(false) 保持原字符。
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return
	}
	sb.WriteString(strings.TrimRight(buf.String(), "\n"))
}
