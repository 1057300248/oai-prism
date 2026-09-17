import React, { Suspense, lazy, useState } from 'react';
import { ConfigProvider, App as AntdApp, Spin } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { MainLayout } from './presentation/layouts/MainLayout';

// 按页代码分割：antd + @ant-design/x + plots 体积大，
// 单 chunk 2.86MB 会让首屏白白等全量下载。lazy 后每页独立分包。
const AccountsPage = lazy(() =>
  import('./presentation/pages/accounts').then((m) => ({ default: m.AccountsPage })),
);
const StatisticsPage = lazy(() =>
  import('./presentation/pages/statistics').then((m) => ({ default: m.StatisticsPage })),
);
const ChatPlaygroundPage = lazy(() =>
  import('./presentation/pages/chat').then((m) => ({ default: m.ChatPlaygroundPage })),
);

const PageFallback: React.FC = () => (
  <div style={{ display: 'flex', justifyContent: 'center', padding: 80 }}>
    <Spin size="large" />
  </div>
);

export const App: React.FC = () => {
  const [currentTab, setCurrentTab] = useState('accounts');

  return (
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#1677ff',
          borderRadius: 8,
          fontFamily: `-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial`,
        },
      }}
    >
      <AntdApp>
        <MainLayout currentKey={currentTab} onKeyChange={setCurrentTab}>
          <Suspense fallback={<PageFallback />}>
            {currentTab === 'accounts' && <AccountsPage />}
            {currentTab === 'statistics' && <StatisticsPage />}
            {currentTab === 'chat' && <ChatPlaygroundPage />}
          </Suspense>
        </MainLayout>
      </AntdApp>
    </ConfigProvider>
  );
};

export default App;
