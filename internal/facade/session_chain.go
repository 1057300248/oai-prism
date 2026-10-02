package facade

// 会话续接链：同一个前端会话（X-Oaiprism-Session）的多轮请求
// 缓存对话历史，供单条消息客户端注入上下文。
//
// 实测结论（2026-10-02 完整实验矩阵 B/B2/K/V/W/Y/N，勿再按直觉改回去）：
//   - 上游后端**只提取"首条 system + 最后一条 user"，中间的 input
//     条目全部丢弃**（translate.go 头注释早已记载该缺陷）。把历史
//     插成独立 input 条目（无论带不带 conversationId/previousResponseId、
//     无论 system 是否加扰动标记、无论加不加零宽空格）全部失忆；
//     历史折叠成 [Previous Conversation History] 文本拼进 system
//     则全部成功 —— 与 translateChatMessages 对客户端多 messages
//     的处理完全同款。
//   - conversationId / previousResponseId 真实前端从不发（带不带
//     都不影响上下文），透传即可，网关不做续接。
//   - 同 project 二次 start 本身没问题（前提：历史在 system 里）。
//
// 历史注入判据：客户端本轮只发了 user 消息（没带 assistant 历史）
// 才注入 —— 已带完整历史的客户端由 translateChatMessages 统一折叠。
// 要开新话题请换会话（新 session ID 天然隔离）。
//
// 局限：内存态，网关重启后链条清空 —— 清空后首轮退化为新对话，
// 第二轮起自动重建链条，不影响正确性（只会丢一次上下文）。

import (
	"strings"
	"sync"
	"time"

	"github.com/oai-prism/oaiprism/internal/prism"
)

// 会话历史的容量上限：条数与字符双预算，超限从最旧的开始丢。
// 24k 字符约等于 6k~8k token，足以覆盖日常多轮；再大就该让客户端
// 显式开新会话，而不是无限堆历史。
const (
	chainHistoryMaxItems = 20
	chainHistoryMaxChars = 24000
)

type sessionChainEntry struct {
	ConversationID string
	ResponseID     string // 上一轮回复句柄 —— 关联用途（上游不代管历史）
	AccountID      string
	Model          string
	History        []ChatMessage // 本会话累积的对话历史（user/assistant 交替）
	UpdatedAt      time.Time
}

var sessionChain = struct {
	mu      sync.RWMutex
	entries map[string]*sessionChainEntry
}{entries: map[string]*sessionChainEntry{}}

const sessionChainTTL = 30 * time.Minute

// sessionChainHistory 取本会话累积的对话历史（过期/不存在返回 nil）。
// 返回副本，调用方可安全修改。
func sessionChainHistory(key string) []ChatMessage {
	sessionChain.mu.RLock()
	defer sessionChain.mu.RUnlock()
	e, ok := sessionChain.entries[key]
	if !ok || time.Since(e.UpdatedAt) > sessionChainTTL || len(e.History) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(e.History))
	copy(out, e.History)
	return out
}

// sessionChainAppend 把本轮的 user 消息与 assistant 回复追加进会话历史。
// user 文本为空（如纯工具轮）时跳过，避免历史里出现空轮次。
func sessionChainAppend(key, userText, assistantText string) {
	userText = strings.TrimSpace(userText)
	assistantText = strings.TrimSpace(assistantText)
	if strings.TrimSpace(key) == "" || (userText == "" && assistantText == "") {
		return
	}
	sessionChain.mu.Lock()
	defer sessionChain.mu.Unlock()
	e, ok := sessionChain.entries[key]
	if !ok || time.Since(e.UpdatedAt) > sessionChainTTL {
		return // 链已失效，历史从下一轮重建
	}
	if userText != "" {
		e.History = append(e.History, ChatMessage{Role: "user", Content: stringContent(userText)})
	}
	if assistantText != "" {
		e.History = append(e.History, ChatMessage{Role: "assistant", Content: stringContent(assistantText)})
	}
	// 双预算裁剪：先按条数，再按总字符。历史尾部是最新内容，必须保住。
	if len(e.History) > chainHistoryMaxItems {
		e.History = append([]ChatMessage{}, e.History[len(e.History)-chainHistoryMaxItems:]...)
	}
	total := 0
	for _, m := range e.History {
		total += len(m.Content.Text())
	}
	if total > chainHistoryMaxChars {
		for total > chainHistoryMaxChars && len(e.History) > 2 {
			total -= len(e.History[0].Content.Text())
			e.History = e.History[1:]
		}
		e.History = append([]ChatMessage{}, e.History...)
	}
}

// historyCarriesContext 判断客户端本轮请求是否已自带历史。
// 判据：messages 里出现过 assistant 角色（正常客户端的多轮请求
// 必然回传往轮 assistant 回复；只发 user 的说明是"单条消息"客户端）。
func historyCarriesContext(msgs []ChatMessage) bool {
	for _, m := range msgs {
		if strings.EqualFold(m.Role, "assistant") {
			return true
		}
	}
	return false
}

// historyCarriesContextA 是 Anthropic Messages 版本（结构同形，类型不同）。
func historyCarriesContextA(msgs []AnthropicMessage) bool {
	for _, m := range msgs {
		if strings.EqualFold(m.Role, "assistant") {
			return true
		}
	}
	return false
}

// injectChainHistory 把链历史折叠成文本块，追加到首条 system 尾部
// （无 system 则前插一条）。
//
// 为什么折叠成文本而不是插入独立 input 条目（2026-10-02 血泪实测）：
// 上游后端只提取"首条 system + 最后一条 user"，中间的 input 条目
// 全部丢弃 —— translate.go 头注释早就写了这个缺陷。往轮把历史插成
// 独立条目（V/W/Y/N 系列实验）全部失忆；折叠进 system（B/B2/K
// 系列实验，同款 [Previous Conversation History] 格式）全部成功。
func injectChainHistory(input []prism.InputItem, hist []ChatMessage) []prism.InputItem {
	if len(hist) == 0 {
		return input
	}
	var sb strings.Builder
	for _, m := range hist {
		txt := strings.TrimSpace(m.Content.Text())
		if txt == "" {
			continue
		}
		var speaker string
		switch {
		case strings.EqualFold(m.Role, "user"):
			speaker = "User"
		case strings.EqualFold(m.Role, "assistant"):
			speaker = "Assistant"
		default:
			continue // tool/function 等对上游无意义
		}
		sb.WriteString(speaker)
		sb.WriteString(": ")
		sb.WriteString(txt)
		sb.WriteString("\n")
	}
	if sb.Len() == 0 {
		return input
	}
	historyText := "\n\n[Previous Conversation History]\n" + sb.String()

	out := make([]prism.InputItem, 0, len(input)+1)
	if len(input) > 0 && input[0].Role == "system" {
		sys := input[0]
		for j := range sys.Content {
			sys.Content[j].Text += historyText
			break // 只拼第一个文本块
		}
		out = append(out, sys)
		out = append(out, input[1:]...)
	} else {
		out = append(out, prism.NewSystemItem(strings.TrimPrefix(historyText, "\n\n")))
		out = append(out, input...)
	}
	return out
}

// lastUserText 取 Input 里最后一条 user 消息的文本（本轮提问），
// 会话历史回写用。取不到返回空。
func lastUserText(input []prism.InputItem) string {
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role != "user" {
			continue
		}
		var sb strings.Builder
		for _, c := range input[i].Content {
			sb.WriteString(c.Text)
		}
		return sb.String()
	}
	return ""
}

// sessionChainPut 记录本轮的上游会话句柄。
func sessionChainPut(key, conversationID, responseID, accountID, model string) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(conversationID) == "" {
		return
	}
	sessionChain.mu.Lock()
	defer sessionChain.mu.Unlock()
	// 顺手清理过期项
	for k, e := range sessionChain.entries {
		if time.Since(e.UpdatedAt) > sessionChainTTL {
			delete(sessionChain.entries, k)
		}
	}
	e, ok := sessionChain.entries[key]
	if !ok {
		e = &sessionChainEntry{}
		sessionChain.entries[key] = e
	}
	// 只更新句柄字段，保留累积的 History（否则每轮 put 都会把历史清空）。
	e.ConversationID = conversationID
	e.ResponseID = responseID
	e.AccountID = accountID
	e.Model = model
	e.UpdatedAt = time.Now()
}

// chainConv 安全取 RunResult 的会话句柄（res 为 nil 时返回空）。
func chainConv(res *RunResult) string {
	if res == nil {
		return ""
	}
	return res.ConversationID
}

// resAccount 安全取 RunResult 的账号 ID。
func resAccount(res *RunResult) string {
	if res == nil {
		return ""
	}
	return res.AccountID
}

// resReqID 安全取 RunResult 的回复句柄（previous_response_id 用）。
//
// 优先 ResponseID（终态 payload.id，resp_* 形态）—— 实测这才是上游认的
// 上下文续接键；RequestID（start 受理号）只是兜底，上游查无此 response
// 时会静默忽略，表现为多轮无上下文。
func resReqID(res *RunResult) string {
	if res == nil {
		return ""
	}
	if res.ResponseID != "" {
		return res.ResponseID
	}
	return res.RequestID
}
