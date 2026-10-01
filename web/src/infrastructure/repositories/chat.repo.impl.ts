import type {
  ChatModelInfo,
  ChatMessage,
  ChatSession,
  IChatRepository,
  SendMessageOptions,
} from '../../domain/chat/entity';
import { getApiKey, httpClient } from '../http/client';

export class ChatRepositoryImpl implements IChatRepository {
  async getAvailableModels(): Promise<ChatModelInfo[]> {
    try {
      const res = await httpClient.get<any>('/v1/models');
      const data = res.data?.data || [];
      if (Array.isArray(data) && data.length > 0) {
        // 主力模型优先级（与上游 Statsig 清单同步，2026-10）。
        // 上游模型会下线/新增：astra 已下线（此处不再置顶），
        // 6.1 Sol 为当前旗舰。
        const PRIORITY: Record<string, number> = {
          'gpt-6.1-sol': 0,
          'gpt-6-luna': 1,
          'gpt-5.6-terra': 2,
          'gpt-5.6-sol': 3,
        };
        const rank = (id: string) => {
          if (id in PRIORITY) return PRIORITY[id];
          // 历史名（astra 等）排后，但仍可用（后端已重定向到当前旗舰）
          if (/astra|^gpt-6$/.test(id)) return 90;
          if (/-low$|-xhigh$/.test(id)) return 80; // effort 变体靠后
          return 50;
        };
        const sorted = [...data].sort(
          (a: any, b: any) => rank(a.id) - rank(b.id) || a.id.localeCompare(b.id),
        );

        return sorted.map((m: any) => {
          let desc = '标准对话与推理模型';
          if (m.id === 'gpt-6.1-sol') desc = '当前旗舰 · 最强推理与全模态支持';
          else if (m.id === 'gpt-6-luna') desc = '6 Luna · 新一代均衡模型';
          else if (m.id === 'gpt-5.6-terra') desc = '5.6 Terra · 稳定通用';
          else if (m.id.includes('sol')) desc = 'Sol 系列 · 高效响应';
          else if (/astra|^gpt-6$/.test(m.id)) desc = '已下线模型别名（自动转 6.1 Sol）';
          return {
            id: m.id,
            name: m.name || m.id,
            description: desc,
          };
        });
      }
    } catch {
      // 容灾兜底
    }
    // 兜底清单：与上游 Statsig prism_codex_models 保持一致（2026-10）。
    return [
      { id: 'gpt-6.1-sol', name: '6.1 Sol', description: '当前旗舰 · 最强推理与全模态支持' },
      { id: 'gpt-6-luna', name: '6 Luna', description: '6 Luna · 新一代均衡模型' },
      { id: 'gpt-5.6-terra', name: '5.6 Terra', description: '5.6 Terra · 稳定通用' },
      { id: 'gpt-5.6-sol', name: '5.6 Sol', description: 'Sol 系列 · 高效响应' },
    ];
  }

  async listSessions(): Promise<ChatSession[]> {
    try {
      const res = await httpClient.get<any[]>('/admin/chat/sessions');
      const list = res.data || [];

      if (list.length === 0) {
        // 后端无会话记录，在 SQLite 初始化默认引导会话
        const defaultSession: ChatSession = {
          id: 'sess_default_playground',
          title: '6.1 Sol 调试会话',
          model: 'gpt-6.1-sol',
          reasoningEffort: 'medium',
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
          messages: [
            {
              id: 'msg_sys_intro',
              role: 'assistant',
              content: '你好！我是接入 OAIprism 代理网关的 Codex AI 助手（当前使用 **6.1 Sol**）。对话与调试记录已在服务端 SQLite 持久化，请随时发送测试请求！',
              createdAt: new Date().toISOString(),
              status: 'success',
            },
          ],
        };
        await this.saveSession(defaultSession);
        return [defaultSession];
      }

      // 获取每个会话的消息历史
      const sessionsWithMessages = await Promise.all(
        list.map(async (s: any): Promise<ChatSession> => {
          let msgs: ChatMessage[] = [];
          try {
            const mRes = await httpClient.get<any[]>(`/admin/chat/sessions/${s.id}/messages`);
            msgs = (mRes.data || []).map((m: any): ChatMessage => ({
              id: m.id,
              role: m.role,
              content: m.content || '',
              reasoning: m.reasoning || '',
              status: m.status || 'success',
              createdAt: m.created_at || new Date().toISOString(),
            }));
          } catch {
            msgs = [];
          }

          return {
            id: s.id,
            title: s.title,
            model: s.model,
            reasoningEffort: s.reasoning_effort,
            createdAt: s.created_at,
            updatedAt: s.updated_at,
            messages: msgs,
          };
        })
      );

      return sessionsWithMessages;
    } catch {
      return [];
    }
  }

  async saveSession(session: ChatSession): Promise<void> {
    try {
      // 1. 持久化会话元数据至 SQLite
      await httpClient.post('/admin/chat/sessions', {
        id: session.id,
        title: session.title,
        model: session.model,
        reasoning_effort: session.reasoningEffort,
      });

      // 2. 持久化最新消息至 SQLite
      if (session.messages && session.messages.length > 0) {
        for (const m of session.messages) {
          await httpClient.post(`/admin/chat/sessions/${session.id}/messages`, {
            id: m.id,
            role: m.role,
            content: m.content,
            reasoning: m.reasoning,
            status: m.status,
          });
        }
      }
    } catch {
      // 网络或接口异常
    }
  }

  async deleteSession(id: string): Promise<void> {
    try {
      await httpClient.delete(`/admin/chat/sessions/${id}`);
    } catch {
      // 异常忽略
    }
  }

  async sendMessageStream(options: SendMessageOptions): Promise<void> {
    const { sessionId, model, reasoningEffort, content, onChunk, onError, onFinish } = options;

    try {
      // 1. 先将用户消息持久化入库
      const userMsgId = `msg_u_${Date.now()}`;
      await httpClient.post(`/admin/chat/sessions/${sessionId}/messages`, {
        id: userMsgId,
        role: 'user',
        content,
        status: 'success',
      }).catch(() => {});

      // 2. 发起流式推理请求
      const response = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Oaiprism-Session': sessionId,
          // 后端启用 APIKeyAuth 时缺失此头会 401
          ...(getApiKey() ? { Authorization: 'Bearer ' + getApiKey() } : {}),
        },
        body: JSON.stringify({
          model,
          reasoning_effort: reasoningEffort,
          stream: true,
          messages: [
            {
              role: 'user',
              content,
            },
          ],
        }),
      });

      if (!response.ok) {
        const errText = await response.text();
        throw new Error(`上游响应失败 (${response.status}): ${errText}`);
      }

      const reader = response.body?.getReader();
      if (!reader) {
        throw new Error('当前浏览器环境不支持 ReadableStream');
      }

      const decoder = new TextDecoder('utf-8');
      let buffer = '';
      let fullAssistantText = '';
      let fullAssistantReasoning = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed || !trimmed.startsWith('data:')) continue;
          const dataStr = trimmed.replace(/^data:\s*/, '').trim();
          if (dataStr === '[DONE]') {
            // 将 assistant 回复持久化入库
            await httpClient.post(`/admin/chat/sessions/${sessionId}/messages`, {
              id: `msg_a_${Date.now()}`,
              role: 'assistant',
              content: fullAssistantText,
              reasoning: fullAssistantReasoning,
              status: 'success',
            }).catch(() => {});

            onFinish?.();
            return;
          }

          try {
            const parsed = JSON.parse(dataStr);
            const delta = parsed.choices?.[0]?.delta;
            if (delta) {
              const textChunk = delta.content || '';
              const reasoningChunk = delta.reasoning_content || delta.reasoning || '';
              fullAssistantText += textChunk;
              fullAssistantReasoning += reasoningChunk;
              onChunk?.(textChunk, reasoningChunk);
            }
          } catch {
            // 忽略非 JSON 行
          }
        }
      }

      // 如果流自然结束但没有收到 [DONE]
      if (fullAssistantText || fullAssistantReasoning) {
        await httpClient.post(`/admin/chat/sessions/${sessionId}/messages`, {
          id: `msg_a_${Date.now()}`,
          role: 'assistant',
          content: fullAssistantText,
          reasoning: fullAssistantReasoning,
          status: 'success',
        }).catch(() => {});
      }

      onFinish?.();
    } catch (err: any) {
      onError?.(err instanceof Error ? err : new Error(String(err)));
    }
  }
}
