import React, { useEffect, useState } from 'react';
import {
  Card,
  Row,
  Col,
  Statistic,
  Table,
  Space,
  Button,
  Tag,
  Typography,
  Modal,
  Descriptions,
  Select,
  Input,
  Tooltip,
} from 'antd';
import {
  LineChartOutlined,
  CheckCircleOutlined,
  ReloadOutlined,
  FireOutlined,
  FolderOpenOutlined,
  SearchOutlined,
  EyeOutlined,
  FieldTimeOutlined,
} from '@ant-design/icons';
import { Area, Pie } from '@ant-design/plots';
import { useStatisticsStore } from '../../../application/statistics/store';
import type { RequestLog } from '../../../domain/statistics/entity';

const { Text } = Typography;

export const StatisticsPage: React.FC = () => {
  const {
    summary,
    modelUsages,
    timeSeries,
    loading,
    requestLogs,
    requestLogsTotal,
    logsPage,
    logsPageSize,
    logsLoading,
    fetchMetrics,
    fetchRequestLogs,
    setLogsFilter,
  } = useStatisticsStore();

  const [selectedLog, setSelectedLog] = useState<RequestLog | null>(null);
  const [modelFilter, setModelFilter] = useState<string>('');
  const [statusFilter, setStatusFilter] = useState<number | undefined>(undefined);

  useEffect(() => {
    fetchMetrics();
    fetchRequestLogs(1, 10);
    const timer = setInterval(() => {
      fetchMetrics();
      fetchRequestLogs();
    }, 8000);
    return () => clearInterval(timer);
  }, [fetchMetrics, fetchRequestLogs]);

  // 格式化运行时长
  const formatUptime = (sec?: number) => {
    if (!sec) return '0秒';
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    if (m > 0) return `${m}分${s}秒`;
    return `${s}秒`;
  };

  // 折线/面积图配置 (QPS 走势)
  const areaConfig = {
    data: timeSeries,
    xField: 'timestamp',
    yField: 'qps',
    smooth: true,
    style: {
      fill: 'linear-gradient(-90deg, white 0%, #1677ff 100%)',
      fillOpacity: 0.35,
    },
    line: {
      style: {
        stroke: '#1677ff',
        lineWidth: 2,
      },
    },
  };

  // 饼图配置 (模型调用分布)
  const pieConfig = {
    data: modelUsages,
    angleField: 'requests',
    colorField: 'model',
    radius: 0.8,
    innerRadius: 0.6,
    label: {
      text: (d: any) => `${d.model}: ${d.percentage}%`,
      position: 'outside',
    },
    legend: {
      color: {
        title: false,
        position: 'right',
        rowPadding: 5,
      },
    },
  };

  // 模型统计列
  const modelColumns = [
    {
      title: '模型名称',
      dataIndex: 'model',
      key: 'model',
      render: (m: string) => (
        <Space>
          <FireOutlined style={{ color: m.includes('6') ? '#ff4d4f' : '#1677ff' }} />
          <span style={{ fontWeight: 600 }}>{m}</span>
          {m === 'gpt-6.1-sol' && <Tag color="red">当前旗舰</Tag>}
        </Space>
      ),
    },
    {
      title: '调用请求数',
      dataIndex: 'requests',
      key: 'requests',
    },
    {
      title: '流量占比',
      dataIndex: 'percentage',
      key: 'percentage',
      render: (p: number) => `${p}%`,
    },
    {
      title: '平均时延',
      dataIndex: 'avgLatencyMs',
      key: 'avgLatencyMs',
      render: (ms: number) => `${ms} ms`,
    },
  ];

  // 请求流水明细列
  const requestLogColumns = [
    {
      title: '时间',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 170,
      render: (ts: string) => (
        <Text style={{ fontSize: 13, fontFamily: 'monospace' }}>
          {ts ? new Date(ts).toLocaleTimeString() + ' ' + new Date(ts).toLocaleDateString() : '-'}
        </Text>
      ),
    },
    {
      title: '请求 ID',
      dataIndex: 'id',
      key: 'id',
      width: 140,
      render: (id: string) => (
        <Tooltip title={id}>
          <Text code style={{ fontSize: 12 }}>
            {id.length > 14 ? id.slice(0, 14) + '...' : id}
          </Text>
        </Tooltip>
      ),
    },
    {
      title: '请求路径',
      dataIndex: 'path',
      key: 'path',
      render: (p: string, r: RequestLog) => (
        <Space>
          <Tag color="blue">{r.method}</Tag>
          <span style={{ fontFamily: 'monospace', fontSize: 13 }}>{p}</span>
        </Space>
      ),
    },
    {
      title: '调用模型',
      dataIndex: 'model',
      key: 'model',
      render: (m: string) => (
        <Tag color={m.includes('6') ? 'volcano' : 'cyan'}>{m || '默认模型'}</Tag>
      ),
    },
    {
      title: '处理账号',
      dataIndex: 'accountId',
      key: 'accountId',
      render: (acc: string) => (
        <Tag color="geekblue">{acc || '默认池'}</Tag>
      ),
    },
    {
      title: '状态',
      dataIndex: 'statusCode',
      key: 'statusCode',
      width: 90,
      render: (code: number) => (
        <Tag color={code >= 200 && code < 300 ? 'success' : 'error'}>
          {code}
        </Tag>
      ),
    },
    {
      title: '耗时',
      dataIndex: 'durationMs',
      key: 'durationMs',
      width: 100,
      render: (ms: number) => (
        <span style={{
          fontWeight: 600,
          color: ms > 3000 ? '#ff4d4f' : ms > 1500 ? '#faad14' : '#52c41a'
        }}>
          {ms} ms
        </span>
      ),
    },
    {
      title: '客户端 IP',
      dataIndex: 'clientIp',
      key: 'clientIp',
      render: (ip: string) => (
        <Text type="secondary" style={{ fontSize: 12 }}>{ip || '127.0.0.1'}</Text>
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 80,
      render: (_: any, r: RequestLog) => (
        <Button
          type="link"
          size="small"
          icon={<EyeOutlined />}
          onClick={() => setSelectedLog(r)}
        >
          详情
        </Button>
      ),
    },
  ];

  return (
    <Space orientation="vertical" size="large" style={{ width: '100%' }}>
      {/* 顶部真实指标卡片 */}
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} md={6}>
          <Card hoverable>
            <Statistic
              title="累计真实请求总数"
              value={summary?.totalRequests || 0}
              prefix={<LineChartOutlined style={{ color: '#1677ff' }} />}
              suffix="次"
            />
            <div style={{ marginTop: 8, fontSize: 12, color: '#888' }}>
              真实失败次数: <Text type={summary?.failures ? 'danger' : 'secondary'}>{summary?.failures || 0}</Text>
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card hoverable>
            <Statistic
              title="请求成功率"
              value={summary?.successRate || 100.0}
              precision={1}
              prefix={<CheckCircleOutlined style={{ color: '#52c41a' }} />}
              suffix="%"
            />
            <div style={{ marginTop: 8, fontSize: 12, color: '#888' }}>
              就绪账号: <strong style={{ color: '#52c41a' }}>{summary?.activeAccounts || 0}</strong> / {summary?.accountsTotal || 0}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card hoverable>
            <Statistic
              title="平均处理耗时"
              value={summary?.avgLatencyMs || 0}
              prefix={<FieldTimeOutlined style={{ color: '#faad14' }} />}
              suffix="ms"
            />
            <div style={{ marginTop: 8, fontSize: 12, color: '#888' }}>
              服务已持续运行: {formatUptime(summary?.uptimeSec)}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card hoverable>
            <Statistic
              title="沙箱项目缓存池"
              value={summary?.projectCacheSize || 0}
              prefix={<FolderOpenOutlined style={{ color: '#722ed1' }} />}
              suffix="个工作区"
            />
            <div style={{ marginTop: 8, fontSize: 12, color: '#888' }}>
              持久化引擎: SQLite 零模拟假数据
            </div>
          </Card>
        </Col>
      </Row>

      {/* 图表展示区 */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={16}>
          <Card
            title="真实请求吞吐走势"
            extra={
              <Button icon={<ReloadOutlined />} onClick={fetchMetrics} loading={loading}>
                刷新指标
              </Button>
            }
          >
            <div style={{ height: 300 }}>
              {timeSeries.length > 0 ? (
                <Area {...areaConfig} />
              ) : (
                <div style={{ textAlign: 'center', paddingTop: 100, color: '#999' }}>
                  暂无请求数据，发起请求后将在此实时绘制真实曲线
                </div>
              )}
            </div>
          </Card>
        </Col>
        <Col xs={24} lg={8}>
          <Card title="模型真实调用分布">
            <div style={{ height: 300 }}>
              {modelUsages.length > 0 ? (
                <Pie {...pieConfig} />
              ) : (
                <div style={{ textAlign: 'center', paddingTop: 100, color: '#999' }}>
                  暂无模型调用分布记录
                </div>
              )}
            </div>
          </Card>
        </Col>
      </Row>

      {/* 核心！每一笔请求明细流水表格 */}
      <Card
        title={
          <Space wrap>
            <LineChartOutlined style={{ color: '#1677ff' }} />
            <span>请求明细流水 (SQLite 持久化详细审计)</span>
            <Tag color="processing">共 {requestLogsTotal} 笔记录</Tag>
          </Space>
        }
        extra={
          <Space wrap>
            <Input
              placeholder="按模型过滤 (如 gpt-6.1-sol)"
              allowClear
              value={modelFilter}
              onChange={(e) => setModelFilter(e.target.value)}
              onPressEnter={() => setLogsFilter({ model: modelFilter || undefined })}
              style={{ width: 180 }}
              prefix={<SearchOutlined />}
            />
            <Select
              placeholder="状态筛选"
              allowClear
              value={statusFilter}
              onChange={(val) => {
                setStatusFilter(val);
                setLogsFilter({ statusCode: val });
              }}
              style={{ width: 110 }}
              options={[
                { label: '200 OK', value: 200 },
                { label: '4xx 客户端', value: 400 },
                { label: '5xx 服务端', value: 500 },
              ]}
            />
            <Button
              icon={<ReloadOutlined />}
              onClick={() => fetchRequestLogs(logsPage, logsPageSize)}
              loading={logsLoading}
            >
              刷新流水
            </Button>
          </Space>
        }
      >
        <Table
          rowKey="id"
          columns={requestLogColumns}
          dataSource={requestLogs}
          loading={logsLoading}
          pagination={{
            current: logsPage,
            pageSize: logsPageSize,
            total: requestLogsTotal,
            showSizeChanger: true,
            pageSizeOptions: ['10', '20', '50'],
            showTotal: (total) => `共 ${total} 笔真实请求记录`,
            onChange: (p, ps) => fetchRequestLogs(p, ps),
          }}
        />
      </Card>

      {/* 模型性能明细 */}
      {modelUsages.length > 0 && (
        <Card title="模型性能明细">
          <Table
            rowKey="model"
            columns={modelColumns}
            dataSource={modelUsages}
            pagination={false}
          />
        </Card>
      )}

      {/* 请求详情弹窗 */}
      <Modal
        title="请求流水详细信息"
        open={Boolean(selectedLog)}
        onCancel={() => setSelectedLog(null)}
        footer={[
          <Button key="close" type="primary" onClick={() => setSelectedLog(null)}>
            关闭
          </Button>,
        ]}
        width={700}
        destroyOnHidden
      >
        {selectedLog && (
          <Descriptions bordered column={2} size="small" style={{ marginTop: 12 }}>
            <Descriptions.Item label="请求 ID" span={2}>
              <Text code copyable>{selectedLog.id}</Text>
            </Descriptions.Item>
            <Descriptions.Item label="请求时间">
              {selectedLog.timestamp}
            </Descriptions.Item>
            <Descriptions.Item label="请求方法与路径">
              <Tag color="blue">{selectedLog.method}</Tag> {selectedLog.path}
            </Descriptions.Item>
            <Descriptions.Item label="命中模型">
              <Tag color="volcano">{selectedLog.model || '-'}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="路由账号">
              <Tag color="geekblue">{selectedLog.accountId || '-'}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="响应状态码">
              <Tag color={selectedLog.statusCode === 200 ? 'success' : 'error'}>
                {selectedLog.statusCode}
              </Tag>
            </Descriptions.Item>
            <Descriptions.Item label="执行耗时">
              <strong>{selectedLog.durationMs} ms</strong>
            </Descriptions.Item>
            <Descriptions.Item label="客户端 IP">
              {selectedLog.clientIp || '127.0.0.1'}
            </Descriptions.Item>
            <Descriptions.Item label="User-Agent" span={2}>
              <Text style={{ fontSize: 12, color: '#666' }}>
                {selectedLog.userAgent || '-'}
              </Text>
            </Descriptions.Item>
            {selectedLog.errorMessage && (
              <Descriptions.Item label="异常错误摘要" span={2}>
                <Text type="danger">{selectedLog.errorMessage}</Text>
              </Descriptions.Item>
            )}
          </Descriptions>
        )}
      </Modal>
    </Space>
  );
};

