import React from 'react';
import { Drawer, Descriptions, Tag, Badge, Space, Alert, Typography, Divider } from 'antd';
import {
  CrownOutlined,
  ThunderboltOutlined,
  ClockCircleOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
} from '@ant-design/icons';
import { useAccountStore } from '../../../application/account/store';

const { Text } = Typography;

export const PlanDetailDrawer: React.FC = () => {
  const { selectedAccount, detailDrawerOpen, closeDetailDrawer } = useAccountStore();

  if (!selectedAccount) return null;

  const daysRemaining = selectedAccount.expires_in_sec
    ? Math.round(selectedAccount.expires_in_sec / 86400)
    : null;

  return (
    <Drawer
      title={
        <Space>
          <CrownOutlined style={{ color: '#faad14' }} />
          <span>账号计划明细 - {selectedAccount.name}</span>
        </Space>
      }
      placement="right"
      width={600}
      open={detailDrawerOpen}
      onClose={closeDetailDrawer}
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Alert
          message={`上游计划类型：${(selectedAccount.plan || 'pro').toUpperCase()}`}
          description={`该账号通过 ${selectedAccount.source} 方式注入，当前由 OAIprism 连接池统一纳管。`}
          type={selectedAccount.enabled ? 'success' : 'warning'}
          showIcon
        />

        <Descriptions title="基本身份元信息" bordered column={1} size="small">
          <Descriptions.Item label="账号标识 (ID)">{selectedAccount.id}</Descriptions.Item>
          <Descriptions.Item label="账号名称">{selectedAccount.name}</Descriptions.Item>
          <Descriptions.Item label="绑定邮箱">
            {selectedAccount.email || <Text type="secondary">（未公开或从 Cookie 直接继承）</Text>}
          </Descriptions.Item>
          <Descriptions.Item label="凭据来源 (Source)">
            <Tag color="purple">{selectedAccount.source}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="健康调度状态">
            <Badge
              status={selectedAccount.enabled ? 'success' : 'error'}
              text={selectedAccount.enabled ? '正常调度' : '已暂停调度'}
            />
          </Descriptions.Item>
        </Descriptions>

        <Divider style={{ margin: '8px 0' }} />

        <Descriptions title="计划与凭据有效期" bordered column={1} size="small">
          <Descriptions.Item label="计划等级 (Plan)">
            <Tag color="gold" icon={<CrownOutlined />}>
              {(selectedAccount.plan || 'pro').toUpperCase()}
            </Tag>
          </Descriptions.Item>
          <Descriptions.Item label="Token 过期时间">
            {selectedAccount.token_expires ? (
              <Space>
                <ClockCircleOutlined />
                <span>{new Date(selectedAccount.token_expires).toLocaleString()}</span>
                {daysRemaining !== null && (
                  <Tag color={daysRemaining > 3 ? 'green' : 'red'}>
                    剩余 {daysRemaining} 天
                  </Tag>
                )}
              </Space>
            ) : (
              <Text type="secondary">静态配置 / 未声明过期时间</Text>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="凭据挂载构成">
            <Space>
              <Tag icon={selectedAccount.has_access_token ? <CheckCircleOutlined /> : <CloseCircleOutlined />} color={selectedAccount.has_access_token ? 'success' : 'default'}>
                Access Token
              </Tag>
              <Tag icon={selectedAccount.has_session ? <CheckCircleOutlined /> : <CloseCircleOutlined />} color={selectedAccount.has_session ? 'processing' : 'default'}>
                Session Cookie
              </Tag>
              <Tag icon={selectedAccount.has_refresh_token ? <CheckCircleOutlined /> : <CloseCircleOutlined />} color={selectedAccount.has_refresh_token ? 'purple' : 'default'}>
                Refresh Token
              </Tag>
            </Space>
          </Descriptions.Item>
        </Descriptions>

        <Divider style={{ margin: '8px 0' }} />

        <Descriptions title="实时并发与性能统计" bordered column={2} size="small">
          <Descriptions.Item label="当前在途请求 (Inflight)">
            <Text strong style={{ color: selectedAccount.inflight > 0 ? '#1677ff' : '#666' }}>
              {selectedAccount.inflight}
            </Text>
          </Descriptions.Item>
          <Descriptions.Item label="最大并发限制 (Max Concurrency)">
            <Tag color="cyan" icon={<ThunderboltOutlined />}>
              {selectedAccount.max_concurrency > 0 ? `${selectedAccount.max_concurrency} 槽位` : '不限并发'}
            </Tag>
          </Descriptions.Item>
          <Descriptions.Item label="累计请求量 (Total Requests)">
            {selectedAccount.total_requests} 次
          </Descriptions.Item>
          <Descriptions.Item label="失败请求数 (Failures)">
            <Text type={selectedAccount.failures > 0 ? 'danger' : 'secondary'}>
              {selectedAccount.failures} 次
            </Text>
          </Descriptions.Item>
          <Descriptions.Item label="连续失败避让计数">
            {selectedAccount.fail_streak} 次
          </Descriptions.Item>
          <Descriptions.Item label="避让冷却剩余">
            {selectedAccount.cooldown_sec > 0 ? `${Math.round(selectedAccount.cooldown_sec)} 秒` : '无冷却'}
          </Descriptions.Item>
        </Descriptions>

        {selectedAccount.tags && selectedAccount.tags.length > 0 && (
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>附加标签 (Tags):</Text>
            <div style={{ marginTop: 4 }}>
              {selectedAccount.tags.map((t, idx) => (
                <Tag key={idx}>{t}</Tag>
              ))}
            </div>
          </div>
        )}
      </Space>
    </Drawer>
  );
};
