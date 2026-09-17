import React, { useState } from 'react';
import { Modal, Tabs, Input, Upload, message, Typography, Space } from 'antd';
import { InboxOutlined, KeyOutlined, FileTextOutlined } from '@ant-design/icons';
import { useAccountStore } from '../../../application/account/store';

const { TextArea } = Input;
const { Dragger } = Upload;
const { Text } = Typography;

export const AccountImportModal: React.FC = () => {
  const { importModalOpen, setImportModalOpen, importAccounts } = useAccountStore();
  const [activeTab, setActiveTab] = useState('text');
  const [rawText, setRawText] = useState('');
  const [loading, setLoading] = useState(false);

  const handleOk = async () => {
    if (!rawText.trim()) {
      message.warning('请先输入或上传账号凭据内容');
      return;
    }
    setLoading(true);
    try {
      await importAccounts({ rawText });
      message.success('账号导入成功！');
      setRawText('');
      setImportModalOpen(false);
    } catch (err: any) {
      message.error(err.message || '导入失败，请检查数据格式');
    } finally {
      setLoading(false);
    }
  };

  const items = [
    {
      key: 'text',
      label: (
        <span>
          <KeyOutlined /> 文本 / JSON 粘贴
        </span>
      ),
      children: (
        <Space orientation="vertical" style={{ width: '100%' }}>
          <Text type="secondary">
            支持单行/多行 Cookie 字符串，或直接粘贴 accounts.json 格式的 JSON 数组：
          </Text>
          <TextArea
            rows={8}
            placeholder={'__Secure-next-auth.session-token=eyJhbGciOiJkaXIi...\n或\n[{"name":"Team通道","cookie":"...","plan":"Enterprise"}]'}
            value={rawText}
            onChange={(e) => setRawText(e.target.value)}
          />
        </Space>
      ),
    },
    {
      key: 'file',
      label: (
        <span>
          <FileTextOutlined /> 文件拖拽导入
        </span>
      ),
      children: (
        <Dragger
          accept=".json,.txt"
          showUploadList={false}
          beforeUpload={(file) => {
            const reader = new FileReader();
            reader.onload = (e) => {
              const content = e.target?.result as string;
              if (content) {
                setRawText(content);
                message.info(`已读取文件: ${file.name}，可切换到文本选项卡预览并点击确定`);
              }
            };
            reader.readAsText(file);
            return false;
          }}
        >
          <p className="ant-upload-drag-icon">
            <InboxOutlined style={{ fontSize: 48, color: '#1677ff' }} />
          </p>
          <p className="ant-upload-text">点击或拖拽 accounts.json 或 cookie.txt 文件到此区域</p>
          <p className="ant-upload-hint">支持单个或批量账号 JSON 配置文件直接解析</p>
        </Dragger>
      ),
    },
  ];

  return (
    <Modal
      title="导入账号凭据"
      open={importModalOpen}
      onOk={handleOk}
      confirmLoading={loading}
      onCancel={() => setImportModalOpen(false)}
      width={600}
      okText="确认导入"
      cancelText="取消"
    >
      <Tabs activeKey={activeTab} onChange={setActiveTab} items={items} />
    </Modal>
  );
};
