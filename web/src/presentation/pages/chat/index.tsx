import React, { useEffect, useState } from 'react';
import {
  Card,
  Row,
  Col,
  Select,
  Segmented,
  Space,
  Button,
  Tag,
  Typography,
  Divider,
  Avatar,
} from 'antd';
import {
  RobotOutlined,
  UserOutlined,
  PlusOutlined,
  ThunderboltOutlined,
  BulbOutlined,
  CodeOutlined,
  FileSearchOutlined,
  PictureOutlined,
} from '@ant-design/icons';
import { Bubble, Sender, ThoughtChain, Conversations, Prompts } from '@ant-design/x';
import type { ReasoningEffort } from '../../../domain/chat/entity';
import { useChatStore } from '../../../application/chat/store';

const { Text } = Typography;

export const ChatPlaygroundPage: React.FC = () => {
  const {
    models,
    sessions,
    currentSessionId,
    selectedModel,
    reasoningEffort,
    isStreaming,
    init,
    selectSession,
    createNewSession,
    setModel,
    setReasoningEffort,
    sendMessage,
  } = useChatStore();

  const [input, setInput] = useState('');

  useEffect(() => {
    init();
  }, [init]);

  const activeSession = sessions.find((s) => s.id === currentSessionId);
  const messages = activeSession?.messages || [];

  const handleSend = () => {
    if (!input.trim() || isStreaming) return;
    const text = input;
    setInput('');
    sendMessage(text);
  };

  // 推荐提示词项
  const promptItems = [
    {
      key: 'p1',
      icon: <CodeOutlined style={{ color: '#1677ff' }} />,
      description: '生成一个鹈鹕骑自行车的 SVG，用 HTML 实现',
    },
    {
      key: 'p2',
      icon: <FileSearchOutlined style={{ color: '#52c41a' }} />,
      description: '测试本地文件修改，查看 Unified Diff 工具调用',
    },
    {
      key: 'p3',
      icon: <PictureOutlined style={{ color: '#faad14' }} />,
      description: '上传并分析图片附件，测试多模态输入能力',
    },
  ];

  // 会话列表转换
  const conversationItems = sessions.map((s) => ({
    key: s.id,
    label: s.title,
  }));

  // Bubble 列表转换
  const bubbleItems = messages.map((m) => {
    const isUser = m.role === 'user';
    return {
      key: m.id,
      role: m.role,
      placement: (isUser ? 'end' : 'start') as 'end' | 'start',
      avatar: (
        <Avatar
          icon={isUser ? <UserOutlined /> : <RobotOutlined />}
          style={{ backgroundColor: isUser ? '#1677ff' : '#52c41a' }}
        />
      ),
      content: (
        <div>
          {/* 若包含思考链，使用 @ant-design/x 的 ThoughtChain 组件呈现 */}
          {m.reasoning && (
            <div style={{ marginBottom: 8 }}>
              <ThoughtChain
                items={[
                  {
                    title: 'GPT-6 Astra 深度推理过程',
                    status: m.status === 'loading' ? 'loading' : 'success',
                    description: (
                      <Text type="secondary" style={{ whiteSpace: 'pre-wrap', fontSize: 13 }}>
                        {m.reasoning}
                      </Text>
                    ),
                  },
                ]}
              />
            </div>
          )}
          <div style={{ whiteSpace: 'pre-wrap', fontSize: 14 }}>
            {m.content || (m.status === 'loading' ? '正在思考生成中...' : '')}
          </div>
        </div>
      ),
      loading: m.status === 'loading' && !m.content && !m.reasoning,
    };
  });

  return (
    <Card
      styles={{ body: { padding: 0 } }}
      title={
        <Space wrap>
          <RobotOutlined style={{ color: '#1677ff' }} />
          <span style={{ fontWeight: 600 }}>Codex AI 模型调试控制台</span>
          <Tag color="cyan">SQLite 持久化</Tag>
          <Divider type="vertical" />
          <Text type="secondary" style={{ fontSize: 13 }}>调试模型:</Text>
          <Select
            value={selectedModel}
            onChange={setModel}
            style={{ width: 220 }}
            options={models.map((m) => ({
              value: m.id,
              label: (
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span>{m.name}</span>
                  {m.id === 'gpt-6-astra' && <Tag color="red" style={{ marginLeft: 6 }}>主模型</Tag>}
                </div>
              ),
            }))}
          />
          <Text type="secondary" style={{ fontSize: 13 }}>推理强度:</Text>
          <Segmented
            value={reasoningEffort}
            onChange={(val) => setReasoningEffort(val as ReasoningEffort)}
            options={[
              { label: '低 (Low)', value: 'low' },
              { label: '中 (Medium)', value: 'medium' },
              { label: '高 (High)', value: 'high' },
            ]}
          />
        </Space>
      }
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={createNewSession}>
          新建会话
        </Button>
      }
    >
      <Row style={{ height: '72vh' }}>
        {/* 左侧会话抽屉列表（使用官方 Conversations 组件） */}
        <Col xs={0} sm={7} md={6} lg={5} style={{ borderRight: '1px solid #f0f0f0', padding: 12, overflowY: 'auto', background: '#fafafa' }}>
          <div style={{ marginBottom: 12, fontWeight: 600, color: '#555', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <span><BulbOutlined /> 调试会话列表</span>
            <Text type="secondary" style={{ fontSize: 12 }}>共 {sessions.length} 个</Text>
          </div>
          <Conversations
            items={conversationItems}
            activeKey={currentSessionId || undefined}
            onActiveChange={(key) => selectSession(key)}
          />
        </Col>

        {/* 右侧对话主体区（使用官方 Bubble.List + Sender + Prompts 组件） */}
        <Col xs={24} sm={17} md={18} lg={19} style={{ display: 'flex', flexDirection: 'column', height: '100%', background: '#fff' }}>
          {/* 消息展示区 */}
          <div style={{ flex: 1, padding: 20, overflowY: 'auto' }}>
            {messages.length === 0 ? (
              <div style={{ textAlign: 'center', marginTop: 80 }}>
                <RobotOutlined style={{ fontSize: 48, color: '#1677ff' }} />
                <h3 style={{ marginTop: 16 }}>欢迎体验 OAIprism 交互式调试终端</h3>
                <p style={{ color: '#888', maxWidth: 500, margin: '0 auto' }}>
                  直连上游 Prism 代理，支持 GPT-6 Astra、工具调用落盘测试与滑动窗口压缩。
                </p>
                <div style={{ marginTop: 24, display: 'inline-block', textAlign: 'left' }}>
                  <Prompts
                    title="推荐快捷调试场景："
                    items={promptItems}
                    onItemClick={(item) => {
                      if (item.data?.description) {
                        setInput(String(item.data.description));
                      }
                    }}
                  />
                </div>
              </div>
            ) : (
              <Bubble.List items={bubbleItems} />
            )}
          </div>

          {/* 快捷 Prompts 推荐 */}
          {messages.length > 0 && (
            <div style={{ padding: '0 20px 8px' }}>
              <Prompts
                items={promptItems}
                onItemClick={(item) => {
                  if (item.data?.description) {
                    setInput(String(item.data.description));
                  }
                }}
              />
            </div>
          )}

          {/* 底部输入框（使用官方 Sender 组件） */}
          <div style={{ padding: '12px 20px 20px', borderTop: '1px solid #f0f0f0', background: '#fff' }}>
            <Sender
              value={input}
              onChange={setInput}
              onSubmit={handleSend}
              loading={isStreaming}
              placeholder="输入调试指令，例如：生成一个鹈鹕骑自行车的 SVG，用 HTML 实现..."
              prefix={<ThunderboltOutlined style={{ color: '#1677ff' }} />}
            />
          </div>
        </Col>
      </Row>
    </Card>
  );
};
