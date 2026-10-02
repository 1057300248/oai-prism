import React, { useState } from 'react';
import {
  Layout,
  Menu,
  Typography,
  Space,
  Tag,
  Badge,
  Button,
  Breadcrumb,
  Tooltip,
  message,
  Dropdown,
  Avatar,
} from 'antd';
import {
  TeamOutlined,
  BarChartOutlined,
  CommentOutlined,
  ApiOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  CloudServerOutlined,
  KeyOutlined,
  UserOutlined,
  SafetyCertificateOutlined,
  LogoutOutlined,
} from '@ant-design/icons';
import { useAccountStore } from '../../application/account/store';
import { ApiKeyModal } from '../components/ApiKeyModal';
import { AdminLoginModal } from '../components/AdminLoginModal';

const { Header, Sider, Content } = Layout;
const { Text } = Typography;

interface MainLayoutProps {
  currentKey: string;
  onKeyChange: (key: string) => void;
  children: React.ReactNode;
}

export const MainLayout: React.FC<MainLayoutProps> = ({
  currentKey,
  onKeyChange,
  children,
}) => {
  const [collapsed, setCollapsed] = useState(false);
  const { readyCount, totalCount } = useAccountStore();
  const [apiKeyModalOpen, setApiKeyModalOpen] = useState(false);
  const [loginModalOpen, setLoginModalOpen] = useState(false);

  const menuItems = [
    {
      key: 'accounts',
      icon: <TeamOutlined />,
      label: '账号与计划池',
    },
    {
      key: 'statistics',
      icon: <BarChartOutlined />,
      label: '调用统计看板',
    },
    {
      key: 'chat',
      icon: <CommentOutlined />,
      label: 'Chat 调试工作台',
    },
  ];

  const breadcrumbNameMap: Record<string, string> = {
    accounts: '账号与计划池',
    statistics: '调用统计看板',
    chat: 'Chat 调试工作台',
  };

  return (
    <Layout style={{ minHeight: '100vh', overflowX: 'hidden' }}>
      {/* 企业级浅色侧边栏 */}
      <Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        trigger={null}
        width={230}
        theme="light"
        style={{
          background: '#ffffff',
          borderRight: '1px solid #f0f0f0',
          boxShadow: '0 2px 8px rgba(0, 0, 0, 0.02)',
          zIndex: 10,
        }}
      >
        {/* Logo 与系统标题 */}
        <div
          style={{
            height: 64,
            display: 'flex',
            alignItems: 'center',
            padding: collapsed ? '0 24px' : '0 20px',
            background: '#ffffff',
            borderBottom: '1px solid #f0f0f0',
            overflow: 'hidden',
            transition: 'all 0.2s',
          }}
        >
          <ApiOutlined style={{ fontSize: 24, color: '#1677ff', flexShrink: 0 }} />
          {!collapsed && (
            <div style={{ marginLeft: 12, overflow: 'hidden' }}>
              <div style={{ color: '#1f2328', fontWeight: 600, fontSize: 16, lineHeight: 1.2 }}>
                OAIprism
              </div>
              <div style={{ color: '#8c8c8c', fontSize: 11 }}>
                Codex 代理管理控制台
              </div>
            </div>
          )}
        </div>

        {/* 侧边菜单栏 */}
        <Menu
          theme="light"
          mode="inline"
          selectedKeys={[currentKey]}
          onClick={(e) => onKeyChange(e.key)}
          items={menuItems}
          style={{ marginTop: 8, borderRight: 0 }}
        />

        {/* 底部折叠切换按钮 */}
        <div
          style={{
            position: 'absolute',
            bottom: 0,
            width: '100%',
            height: 48,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            borderTop: '1px solid #f0f0f0',
            color: '#595959',
            background: '#fafafa',
            cursor: 'pointer',
          }}
          onClick={() => setCollapsed(!collapsed)}
        >
          {collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
        </div>
      </Sider>

      <Layout style={{ background: '#f5f7fa' }}>
        {/* 企业级白色顶部栏 */}
        <Header
          style={{
            background: '#fff',
            padding: '0 24px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            borderBottom: '1px solid #f0f0f0',
            height: 64,
            lineHeight: '64px',
          }}
        >
          {/* 面包屑 */}
          <Space size="middle">
            <Button
              type="text"
              icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
              onClick={() => setCollapsed(!collapsed)}
              style={{ fontSize: 16, width: 40, height: 40 }}
            />
            <Breadcrumb
              items={[
                { title: '控制台' },
                { title: breadcrumbNameMap[currentKey] || '当前页面' },
              ]}
            />
          </Space>

          {/* 右侧状态区 */}
          <Space size="middle">
            <Tooltip title="当前后端健康活跃账号数 / 挂载账号总数">
              <Badge
                status={readyCount > 0 ? 'success' : 'error'}
                text={
                  <Text style={{ fontSize: 13 }}>
                    就绪: <strong style={{ color: readyCount > 0 ? '#52c41a' : '#ff4d4f' }}>{readyCount}</strong> / {totalCount}
                  </Text>
                }
              />
            </Tooltip>

            <Tag color="geekblue" icon={<CloudServerOutlined />}>
              prism.openai.com
            </Tag>

            {/* 对外 API Key 管理与快速接入入口 */}
            <Tooltip title="查看与配置对外客户端调用的 API 密钥 (Bearer Token)">
              <Button
                icon={<KeyOutlined />}
                size="small"
                onClick={() => setApiKeyModalOpen(true)}
              >
                API 密钥
              </Button>
            </Tooltip>

            {/* 管理员登录与身份入口 */}
            <Dropdown
              menu={{
                items: [
                  {
                    key: 'apikey',
                    icon: <KeyOutlined />,
                    label: '对外 API 密钥',
                    onClick: () => setApiKeyModalOpen(true),
                  },
                  {
                    key: 'auth',
                    icon: <SafetyCertificateOutlined />,
                    label: '登录认证设置',
                    onClick: () => setLoginModalOpen(true),
                  },
                  { type: 'divider' },
                  {
                    key: 'logout',
                    icon: <LogoutOutlined />,
                    label: '退出 / 重新登录',
                    danger: true,
                    onClick: () => {
                      message.info('请重新认证管理员凭证');
                      setLoginModalOpen(true);
                    },
                  },
                ],
              }}
              placement="bottomRight"
            >
              <Space style={{ cursor: 'pointer' }}>
                <Avatar size="small" icon={<UserOutlined />} style={{ backgroundColor: '#1677ff' }} />
                <Text style={{ fontSize: 13, fontWeight: 500 }}>管理员</Text>
              </Space>
            </Dropdown>
          </Space>
        </Header>

        {/* 页面内容容器：高度 = 100vh - 顶栏 64px - 上下边距 16/20，
            body 级零滚动条，滚动收敛到各页面内部 */}
        <Content
          style={{
            margin: '16px 24px 20px',
            height: 'calc(100vh - 100px)',
            minHeight: 0,
            overflow: 'hidden',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {children}
        </Content>

        {/* API 密钥与管理员登录弹窗 */}
        <ApiKeyModal open={apiKeyModalOpen} onClose={() => setApiKeyModalOpen(false)} />
        <AdminLoginModal open={loginModalOpen} onClose={() => setLoginModalOpen(false)} />
      </Layout>
    </Layout>
  );
};
