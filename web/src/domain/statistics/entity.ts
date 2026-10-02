/**
 * 统计与监控领域实体与值对象
 * 严格对齐后端 /admin/stats 与 Prometheus /metrics 真实字段
 */

export interface GlobalAdminStats {
  uptimeSec: number;
  accountsTotal: number;
  accountsReady: number;
  projectCacheSize: number;
  captureEnabled: boolean;
  captureWritten: number;
  captureDropped: number;
  upstream: string;
  credsMode: string;
  credsFile: string;
}

export interface MetricSummary {
  totalRequests: number;
  failures: number;
  successRate: number;      // 百分比
  activeAccounts: number;
  accountsTotal: number;
  currentQPS: number;
  avgLatencyMs: number;
  projectCacheSize: number;
  uptimeSec: number;
}

export interface ModelUsage {
  model: string;
  requests: number;
  percentage: number;
  avgLatencyMs: number;
}

export interface TimeSeriesPoint {
  timestamp: string;
  qps: number;
  latency: number;
  errorRate: number;
}

export interface RequestLog {
  id: string;
  timestamp: string;
  method: string;
  path: string;
  model: string;
  accountId: string;
  statusCode: number;
  durationMs: number;
  promptTokens: number;
  completionTokens: number;
  errorMessage: string;
  clientIp: string;
  userAgent: string;
}

export interface RequestLogFilter {
  page: number;
  pageSize: number;
  model?: string;
  accountId?: string;
  statusCode?: number;
}

export interface RequestLogQueryResult {
  total: number;
  page: number;
  pageSize: number;
  items: RequestLog[];
}

export interface IStatisticsRepository {
  getSummary(): Promise<MetricSummary>;
  getAdminStats(): Promise<GlobalAdminStats>;
  getModelUsages(): Promise<ModelUsage[]>;
  getTimeSeries(): Promise<TimeSeriesPoint[]>;
  getAvailableModelIds(): Promise<string[]>;
  queryRequestLogs(filter: RequestLogFilter): Promise<RequestLogQueryResult>;
}

