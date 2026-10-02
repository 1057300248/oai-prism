import type {
  ChatAttachment,
  ChatModelInfo,
  ChatMessage,
  ChatSession,
  IChatRepository,
  SendMessageOptions,
} from '../../domain/chat/entity';
import { compareModelNewestFirst, pickMainModels } from '../../domain/modelFilter';
import { getApiKey, httpClient } from '../http/client';

export class ChatRepositoryImpl implements IChatRepository {
  async fetchModelCatalog(): Promise<{ mains: ChatModelInfo[]; allIds: string[] }> {
    try {
      const res = await httpClient.get<any>('/v1/models');
      const data = res.data?.data || [];
      if (Array.isArray(data) && data.length > 0) {
        // 后端 /v1/models 已只暴露现役模型（含各自主模型的档位变体）。
        // mains = 不带档位后缀的条目；allIds 全量保留 —— 各模型的可用推理档位
        // 由其 effort 变体是否存在推导（domain/modelFilter.ts）。
        const mains = pickMainModels(data).sort(compareModelNewestFirst);
        if (mains.length > 0) {
          return {
            mains: mains.map((m: any) => ({ id: m.id, name: m.name || m.id })),
            allIds: data.map((m: any) => m.id),
          };
        }
      }
    } catch {
      // 容灾兜底：后端不可达时仅保旗舰，避免空 UI
    }
    return {
      mains: [{ id: 'gpt-6.1-sol', name: '6.1 Sol' }],
      allIds: ['gpt-6.1-sol', 'gpt-6.1-sol-low', 'gpt-6.1-sol-high', 'gpt-6.1-sol-xhigh'],
    };
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
            msgs = (mRes.data || []).map((m: any): ChatMessage => {
              // 多模态兼容：content 若为 JSON 数组串（text + image_url）→ 拆出文本与附件
              let content: string = m.content || '';
              let attachments: ChatAttachment[] | undefined;
              if (content.startsWith('[')) {
                try {
                  const parts = JSON.parse(content);
                  if (Array.isArray(parts)) {
                    content = parts
                      .filter((p: any) => p.type === 'text')
                      .map((p: any) => p.text || '')
                      .join('\n');
                    const imgs = parts
                      .filter((p: any) => p.type === 'image_url' && p.image_url?.url)
                      .map((p: any) => ({ name: '图片附件', dataUrl: p.image_url.url as string }));
                    if (imgs.length > 0) attachments = imgs;
                  }
                } catch {
                  // 非 JSON 串，按纯文本处理
                }
              }
              return {
                id: m.id,
                role: m.role,
                content,
                attachments,
                reasoning: m.reasoning || '',
                status: m.status || 'success',
                createdAt: m.created_at || new Date().toISOString(),
              };
            });
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
    const { sessionId, model, reasoningEffort, content, attachments, onChunk, onError, onFinish } = options;

    try {
      // 组装 OpenAI 多模态消息：无附件 = 纯字符串；有附件 = text + image_url 内容块
      // （后端 facade/translate.go 原生支持 image_url → input_image 转换）
      const userContent: any = attachments && attachments.length > 0
        ? [
            { type: 'text', text: content },
            ...attachments.map((a) => ({ type: 'image_url', image_url: { url: a.dataUrl } })),
          ]
        : content;
      const persistContent = typeof userContent === 'string' ? userContent : JSON.stringify(userContent);

      // 1. 先将用户消息持久化入库（多模态内容以 JSON 串存储，读取端解析）
      const userMsgId = `msg_u_${Date.now()}`;
      await httpClient.post(`/admin/chat/sessions/${sessionId}/messages`, {
        id: userMsgId,
        role: 'user',
        content: persistContent,
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
              content: userContent,
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
