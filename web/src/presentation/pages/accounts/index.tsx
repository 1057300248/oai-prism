import React, { useEffect, useState, useMemo } from 'react';
import {
  Card,
  Table,
  Tag,
  Button,
  Space,
  Input,
  Select,
  Badge,
  Tooltip,
  message,
  Typography,
  Popconfirm,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  SearchOutlined,
  ReloadOutlined,
  PlusOutlined,
  EyeOutlined,
  SyncOutlined,
  ClockCircleOutlined,
  EditOutlined,
  DeleteOutlined,
} from '@ant-design/icons';
import type { AccountStats } from '../../../domain/account/entity';
import { useAccountStore } from '../../../application/account/store';
import { AccountImportModal } from './AccountImportModal';
import { PlanDetailDrawer } from './PlanDetailDrawer';
import { AccountEditModal } from './AccountEditModal';

const { Text } = Typography;

export const AccountsPage: React.FC = () => {
  const {
    accounts,
    loading,
    credsFile,
    fetchAccounts,
    reloadPool,
    refreshAccount,
    deleteAccount,
    openDetailDrawer,
    openEditModal,
    setImportModalOpen,
  } = useAccountStore();

  // 搜索与过滤状态
  const [searchText, setSearchText] = useState('');
  const [planFilter, setPlanFilter] = useState<string>('all');
  const [statusFilter, setStatusFilter] = useState<string>('all');

  // 分页状态
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [refreshingId, setRefreshingId] = useState<string | null>(null);

  useEffect(() => {
    fetchAccounts();
  }, [fetchAccounts]);

  // 前端多维度实时过滤
  const filteredAccounts = useMemo(() => {
    return accounts.filter((acc) => {
      const matchSearch =
        !searchText.trim() ||
        acc.name.toLowerCase().includes(searchText.toLowerCase()) ||
        acc.id.toLowerCase().includes(searchText.toLowerCase()) ||
        (acc.email && acc.email.toLowerCase().includes(searchText.toLowerCase()));

      const matchPlan =
        planFilter === 'all' || acc.plan.toLowerCase() === planFilter.toLowerCase();

      const matchStatus =
        statusFilter === 'all' ||
        (statusFilter === 'enabled' && acc.enabled) ||
        (statusFilter === 'disabled' && !acc.enabled) ||
        (statusFilter === 'cooldown' && acc.cooldown_sec > 0);

      return matchSearch && matchPlan && matchStatus;
    });
  }, [accounts, searchText, planFilter, statusFilter]);

  // 单账号后端刷新
  const handleSingleRefresh = async (record: AccountStats) => {
    setRefreshingId(record.id);
    try {
      const res = await refreshAccount(record.id);
      message.success(`账号 [${record.name}] 凭据已刷新！计划: ${res.plan}，到期: ${new Date(res.expires_at).toLocaleDateString()}`);
    } catch (err: any) {
      message.error(`刷新失败: ${err.message}`);
    } finally {
      setRefreshingId(null);
    }
  };

  const columns: ColumnsType<AccountStats> = [
    {
      title: '账号 ID / 名称',
      dataIndex: 'name',
      key: 'name',
      width: 220,
      render: (text, record) => (
        <div>
          <Space orientation="horizontal" size="small">
            <Text strong>{text}</Text>
            <Tag color={record.source === 'oauth' ? 'purple' : 'default'} style={{ fontSize: 11 }}>
              {record.source}
            </Tag>
          </Space>
          <div style={{ fontSize: 12, color: '#888', marginTop: 2 }}>{record.id}</div>
        </div>
      ),
    },
    {
      title: '绑定邮箱',
      dataIndex: 'email',
      key: 'email',
      width: 170,
      ellipsis: true,
      render: (email) => email ? <Text copyable>{email}</Text> : <Text type="secondary">-</Text>,
    },
    {
      title: '计划类型',
      dataIndex: 'plan',
      key: 'plan',
      width: 95,
      render: (plan) => {
        const p = (plan || 'pro').toLowerCase();
        const color = p.includes('team') || p.includes('enterprise') ? 'gold' : p.includes('pro') ? 'blue' : 'default';
        return <Tag color={color} style={{ fontWeight: 600, textTransform: 'uppercase' }}>{plan || '未知'}</Tag>;
      },
    },
    {
      title: '调度状态',
      dataIndex: 'enabled',
      key: 'enabled',
      width: 120,
      render: (enabled, record) => {
        if (record.cooldown_sec > 0) {
          return (
            <Badge
              status="warning"
              text={
                <Tooltip title={`冷却中：因失败过多暂时避让，剩余 ${Math.round(record.cooldown_sec)} 秒后自动解除`}>
                  <span>冷却中 ({Math.round(record.cooldown_sec)}s)</span>
                </Tooltip>
              }
            />
          );
        }
        if (enabled) {
          return <Badge status="success" text="健康可用" />;
        }
        return (
          <Badge
            status="error"
            text={
              <Tooltip title={`已停用，连续失败 ${record.fail_streak} 次`}>
                <span>不可用 (失败{record.fail_streak})</span>
              </Tooltip>
            }
          />
        );
      },
    },
    {
      title: '凭据构成',
      key: 'credentials',
      width: 150,
      render: (_, record) => (
        <span className="cred-tags">
          <Tooltip title={record.has_access_token ? 'Access Token 正常' : '缺失 Access Token'}>
            <Tag color={record.has_access_token ? 'green' : 'default'}>JWT</Tag>
          </Tooltip>
          <Tooltip title={record.has_session ? 'Session Cookie 正常' : '缺失 Session'}>
            <Tag color={record.has_session ? 'blue' : 'default'}>Cookie</Tag>
          </Tooltip>
          {record.has_refresh_token && (
            <Tooltip title="支持 OAuth Refresh Token 自动续签">
              <Tag color="purple">OAuth</Tag>
            </Tooltip>
          )}
        </span>
      ),
    },
    {
      title: 'Token 到期',
      dataIndex: 'token_expires',
      key: 'token_expires',
      width: 130,
      render: (expires, record) => {
        if (!expires) return <Text type="secondary">永久/静态</Text>;
        const days = Math.round((record.expires_in_sec || 0) / 86400);
        return (
          <div>
            <div>{new Date(expires).toLocaleDateString()}</div>
            <Text type={days <= 2 ? 'danger' : 'secondary'} style={{ fontSize: 11 }}>
              <ClockCircleOutlined /> {days > 0 ? `剩余 ${days} 天` : '即将到期'}
            </Text>
          </div>
        );
      },
    },
    {
      title: '并发 (在途/上限)',
      key: 'concurrency',
      width: 120,
      render: (_, record) => {
        const max = record.max_concurrency > 0 ? record.max_concurrency : '无限制';
        return (
          <Text>
            <strong>{record.inflight}</strong> / {max}
          </Text>
        );
      },
    },
    {
      title: '请求统计 (总/败)',
      key: 'stats',
      width: 120,
      render: (_, record) => (
        <Text>
          {record.total_requests} / <Text type={record.failures > 0 ? 'danger' : 'secondary'}>{record.failures}</Text>
        </Text>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 320,
      render: (_, record) => (
        <Space size="small">
          <Button
            type="link"
            size="small"
            icon={<EyeOutlined />}
            onClick={() => openDetailDrawer(record)}
          >
            明细
          </Button>
          <Button
            type="link"
            size="small"
            icon={<EditOutlined />}
            onClick={() => openEditModal(record)}
          >
            编辑
          </Button>
          <Button
            type="link"
            size="small"
            icon={<SyncOutlined spin={refreshingId === record.id} />}
            loading={refreshingId === record.id}
            onClick={() => handleSingleRefresh(record)}
          >
            刷新
          </Button>
          <Popconfirm
            title="确定物理删除此账号？"
            description="将直接从 SQLite 数据库与运行池中永久删除，无需后端改配置！"
            onConfirm={async () => {
              try {
                await deleteAccount(record.id);
                message.success(`账号 [${record.name}] 已从 SQLite 物理删除并生效！`);
              } catch (err: any) {
                message.error(`删除失败: ${err.message}`);
              }
            }}
            okText="确定删除"
            cancelText="取消"
            okButtonProps={{ danger: true }}
          >
            <Button type="link" danger size="small" icon={<DeleteOutlined />}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card
      styles={{
        // 卡片撑满 Content 容器；body 为纵向 flex：工具栏固定、表格撑满、分页贴底
        body: { padding: '16px 20px', flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' },
      }}
      style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', width: '100%' }}
      title={
        <Space size="middle">
          <span style={{ fontWeight: 600 }}>账号与计划池</span>
          {credsFile && (
            <Tooltip title={`凭据源: ${credsFile}`}>
              <Text
                type="secondary"
                style={{
                  fontSize: 12,
                  fontWeight: 'normal',
                  maxWidth: 340,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  display: 'inline-block',
                  verticalAlign: 'middle',
                }}
              >
                凭据源: {credsFile}
              </Text>
            </Tooltip>
          )}
        </Space>
      }
      extra={
        <Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={async () => {
              try {
                await reloadPool();
                message.success('账号池已从 SQLite 热重载并同步最新状态！');
              } catch (err: any) {
                message.error(`重载失败: ${err.message}`);
              }
            }}
            loading={loading}
          >
            重载并刷新
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setImportModalOpen(true)}
          >
            导入新账号
          </Button>
        </Space>
      }
    >
      {/* 搜索与过滤工具栏（固定高度，不参与表格弹性） */}
      <Space orientation="horizontal" size="middle" style={{ marginBottom: 16, width: '100%', flexWrap: 'wrap', flexShrink: 0 }}>
        <Input
          placeholder="搜索账号 ID、名称或邮箱..."
          prefix={<SearchOutlined style={{ color: '#aaa' }} />}
          value={searchText}
          onChange={(e) => {
            setSearchText(e.target.value);
            setCurrentPage(1);
          }}
          style={{ width: 260 }}
          allowClear
        />

        <Space orientation="horizontal" size="small">
          <Text type="secondary">计划等级:</Text>
          <Select
            value={planFilter}
            onChange={(v) => {
              setPlanFilter(v);
              setCurrentPage(1);
            }}
            style={{ width: 120 }}
            options={[
              { value: 'all', label: '全部计划' },
              { value: 'pro', label: 'Pro 计划' },
              { value: 'team', label: 'Team 计划' },
              { value: 'free', label: 'Free 计划' },
            ]}
          />
        </Space>

        <Space orientation="horizontal" size="small">
          <Text type="secondary">调度状态:</Text>
          <Select
            value={statusFilter}
            onChange={(v) => {
              setStatusFilter(v);
              setCurrentPage(1);
            }}
            style={{ width: 130 }}
            options={[
              { value: 'all', label: '全部状态' },
              { value: 'enabled', label: '健康可用' },
              { value: 'cooldown', label: '冷却中' },
              { value: 'disabled', label: '已停用' },
            ]}
          />
        </Space>

        {(searchText || planFilter !== 'all' || statusFilter !== 'all') && (
          <Button
            type="link"
            size="small"
            onClick={() => {
              setSearchText('');
              setPlanFilter('all');
              setStatusFilter('all');
              setCurrentPage(1);
            }}
          >
            重置筛选
          </Button>
        )}
      </Space>

      {/* 标准自适应分页表格：表格撑满剩余高度、行多时内部滚动、分页固定底部（.table-fill） */}
      <div className="table-fill">
        <Table
          rowKey="id"
          columns={columns}
          dataSource={filteredAccounts}
          loading={loading}
          scroll={{ x: 1445, y: 200 }}
          pagination={{
            current: currentPage,
            pageSize: pageSize,
            total: filteredAccounts.length,
            showSizeChanger: true,
            pageSizeOptions: ['5', '10', '20', '50'],
            showQuickJumper: true,
            showTotal: (total, range) => `第 ${range[0]}-${range[1]} 条 / 共 ${total} 条账号`,
            onChange: (page, size) => {
              setCurrentPage(page);
              setPageSize(size);
            },
          }}
        />
      </div>

      <AccountImportModal />
      <PlanDetailDrawer />
      <AccountEditModal />
    </Card>
  );
};
