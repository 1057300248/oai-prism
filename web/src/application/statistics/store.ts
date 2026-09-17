import { create } from 'zustand';
import type {
  MetricSummary,
  ModelUsage,
  TimeSeriesPoint,
  RequestLog,
  RequestLogFilter,
} from '../../domain/statistics/entity';
import { StatisticsRepositoryImpl } from '../../infrastructure/repositories/statistics.repo.impl';

const repo = new StatisticsRepositoryImpl();

interface StatisticsState {
  summary: MetricSummary | null;
  modelUsages: ModelUsage[];
  timeSeries: TimeSeriesPoint[];
  loading: boolean;

  // 请求流水明细状态
  requestLogs: RequestLog[];
  requestLogsTotal: number;
  logsPage: number;
  logsPageSize: number;
  logsFilter: { model?: string; accountId?: string; statusCode?: number };
  logsLoading: boolean;

  fetchMetrics: () => Promise<void>;
  fetchRequestLogs: (page?: number, pageSize?: number, filter?: Partial<RequestLogFilter>) => Promise<void>;
  setLogsFilter: (filter: Partial<RequestLogFilter>) => void;
}

export const useStatisticsStore = create<StatisticsState>((set, get) => ({
  summary: null,
  modelUsages: [],
  timeSeries: [],
  loading: false,

  requestLogs: [],
  requestLogsTotal: 0,
  logsPage: 1,
  logsPageSize: 10,
  logsFilter: {},
  logsLoading: false,

  fetchMetrics: async () => {
    set({ loading: true });
    try {
      const [summary, modelUsages, timeSeries] = await Promise.all([
        repo.getSummary(),
        repo.getModelUsages(),
        repo.getTimeSeries(),
      ]);
      set({ summary, modelUsages, timeSeries });
    } finally {
      set({ loading: false });
    }
  },

  fetchRequestLogs: async (page, pageSize, filter) => {
    set({ logsLoading: true });
    try {
      const curPage = page || get().logsPage;
      const curPageSize = pageSize || get().logsPageSize;
      const curFilter = { ...get().logsFilter, ...(filter || {}) };

      const res = await repo.queryRequestLogs({
        page: curPage,
        pageSize: curPageSize,
        ...curFilter,
      });

      set({
        requestLogs: res.items,
        requestLogsTotal: res.total,
        logsPage: curPage,
        logsPageSize: curPageSize,
        logsFilter: curFilter,
      });
    } finally {
      set({ logsLoading: false });
    }
  },

  setLogsFilter: (filter) => {
    set((state) => ({ logsFilter: { ...state.logsFilter, ...filter }, logsPage: 1 }));
    get().fetchRequestLogs(1, get().logsPageSize, filter);
  },
}));

