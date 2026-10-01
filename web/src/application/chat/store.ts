import { create } from 'zustand';
import type { ChatMessage, ChatModelInfo, ChatSession, ReasoningEffort } from '../../domain/chat/entity';
import { ChatRepositoryImpl } from '../../infrastructure/repositories/chat.repo.impl';

const repo = new ChatRepositoryImpl();

interface ChatState {
  models: ChatModelInfo[];
  sessions: ChatSession[];
  currentSessionId: string | null;
  selectedModel: string;
  reasoningEffort: ReasoningEffort;
  isStreaming: boolean;

  // Actions
  init: () => Promise<void>;
  selectSession: (id: string) => void;
  createNewSession: () => void;
  deleteSession: (id: string) => Promise<void>;
  setModel: (model: string) => void;
  setReasoningEffort: (effort: ReasoningEffort) => void;
  sendMessage: (text: string) => Promise<void>;
}

export const useChatStore = create<ChatState>((set, get) => ({
  models: [],
  sessions: [],
  currentSessionId: null,
  selectedModel: 'gpt-6.1-sol',
  reasoningEffort: 'medium',
  isStreaming: false,

  init: async () => {
    const [models, sessions] = await Promise.all([
      repo.getAvailableModels(),
      repo.listSessions(),
    ]);
    const defaultSessionId = sessions[0]?.id || null;
    set({
      models,
      sessions,
      currentSessionId: defaultSessionId,
      selectedModel: models[0]?.id || 'gpt-6.1-sol',
    });
  },

  selectSession: (id: string) => {
    set({ currentSessionId: id });
  },

  createNewSession: () => {
    const newSession: ChatSession = {
      id: `sess_${Date.now()}`,
      title: '新调试会话',
      model: get().selectedModel,
      reasoningEffort: get().reasoningEffort,
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      messages: [],
    };
    const sessions = [newSession, ...get().sessions];
    repo.saveSession(newSession);
    set({ sessions, currentSessionId: newSession.id });
  },

  deleteSession: async (id: string) => {
    await repo.deleteSession(id);
    const sessions = get().sessions.filter((s) => s.id !== id);
    const nextId = sessions[0]?.id || null;
    set({ sessions, currentSessionId: nextId });
  },

  setModel: (model: string) => {
    set({ selectedModel: model });
  },

  setReasoningEffort: (effort: ReasoningEffort) => {
    set({ reasoningEffort: effort });
  },

  sendMessage: async (text: string) => {
    const { currentSessionId, selectedModel, reasoningEffort, sessions } = get();
    if (!text.trim() || !currentSessionId) return;

    const session = sessions.find((s) => s.id === currentSessionId);
    if (!session) return;

    const userMsg: ChatMessage = {
      id: `msg_${Date.now()}_u`,
      role: 'user',
      content: text,
      createdAt: new Date().toISOString(),
      status: 'success',
    };

    const assistantMsgId = `msg_${Date.now()}_a`;
    const assistantMsg: ChatMessage = {
      id: assistantMsgId,
      role: 'assistant',
      content: '',
      reasoning: '',
      createdAt: new Date().toISOString(),
      status: 'loading',
    };

    const updatedMessages = [...session.messages, userMsg, assistantMsg];
    const updatedSession: ChatSession = {
      ...session,
      title: session.messages.length === 0 ? text.slice(0, 18) : session.title,
      messages: updatedMessages,
      updatedAt: new Date().toISOString(),
    };

    const updatedSessions = sessions.map((s) => (s.id === currentSessionId ? updatedSession : s));
    set({ sessions: updatedSessions, isStreaming: true });

    let currentContent = '';
    let currentReasoning = '';

    await repo.sendMessageStream({
      sessionId: currentSessionId,
      content: text,
      model: selectedModel,
      reasoningEffort,
      onChunk: (chunk, reasoningChunk) => {
        if (chunk) currentContent += chunk;
        if (reasoningChunk) currentReasoning += reasoningChunk;

        const liveSessions = get().sessions.map((s) => {
          if (s.id !== currentSessionId) return s;
          const msgs = s.messages.map((m) => {
            if (m.id !== assistantMsgId) return m;
            return {
              ...m,
              content: currentContent,
              reasoning: currentReasoning,
              status: 'loading' as const,
            };
          });
          return { ...s, messages: msgs };
        });
        set({ sessions: liveSessions });
      },
      onFinish: () => {
        const finalSessions = get().sessions.map((s) => {
          if (s.id !== currentSessionId) return s;
          const msgs = s.messages.map((m) => {
            if (m.id !== assistantMsgId) return m;
            return {
              ...m,
              content: currentContent || '（已完成响应）',
              reasoning: currentReasoning,
              status: 'success' as const,
            };
          });
          const completedSession = { ...s, messages: msgs };
          repo.saveSession(completedSession);
          return completedSession;
        });
        set({ sessions: finalSessions, isStreaming: false });
      },
      onError: (err) => {
        const errorSessions = get().sessions.map((s) => {
          if (s.id !== currentSessionId) return s;
          const msgs = s.messages.map((m) => {
            if (m.id !== assistantMsgId) return m;
            return {
              ...m,
              content: `[请求失败] ${err.message}`,
              status: 'error' as const,
            };
          });
          return { ...s, messages: msgs };
        });
        set({ sessions: errorSessions, isStreaming: false });
      },
    });
  },
}));
