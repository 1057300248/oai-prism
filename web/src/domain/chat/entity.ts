/**
 * 对话调试领域实体与值对象
 */

export interface ChatModelInfo {
  id: string;
  name: string;
  description?: string;
}

export type ReasoningEffort = 'low' | 'medium' | 'high' | 'xhigh';

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  reasoning?: string;         // 模型思考过程（ThoughtChain 呈现）
  status?: 'loading' | 'success' | 'error';
  createdAt: string;
}

export interface ChatSession {
  id: string;
  title: string;
  model: string;
  reasoningEffort: ReasoningEffort;
  createdAt: string;
  updatedAt: string;
  messages: ChatMessage[];
}

export interface SendMessageOptions {
  sessionId: string;
  content: string;
  model: string;
  reasoningEffort: ReasoningEffort;
  onChunk?: (chunk: string, reasoningChunk?: string) => void;
  onError?: (err: Error) => void;
  onFinish?: () => void;
}

export interface IChatRepository {
  getAvailableModels(): Promise<ChatModelInfo[]>;
  sendMessageStream(options: SendMessageOptions): Promise<void>;
  listSessions(): Promise<ChatSession[]>;
  saveSession(session: ChatSession): Promise<void>;
  deleteSession(id: string): Promise<void>;
}
