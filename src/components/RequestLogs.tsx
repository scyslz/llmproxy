import { useState, useEffect, useCallback } from "react";
import { RequestLog, RequestLogStats, VirtualKey, Provider } from "../types";
import { RefreshCw, Filter, Activity, ArrowDownToLine, ArrowUpFromLine, Database, Cpu, FileSearch } from "lucide-react";
import { apiFetch } from "../lib/api";

interface RequestLogsProps {
  virtualKeys: VirtualKey[];
  providers: Provider[];
  onViewLogs?: (requestId: string, hasDetail: boolean) => void;
}

export default function RequestLogs({ virtualKeys, providers, onViewLogs }: RequestLogsProps) {
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [stats, setStats] = useState<RequestLogStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [keyFilter, setKeyFilter] = useState("");
  const [providerFilter, setProviderFilter] = useState("");
  const [modelFilter, setModelFilter] = useState("");
  const [timeRange, setTimeRange] = useState("all");
  const [page, setPage] = useState(1);
  const PAGE_SIZE = 20;

  const TIME_RANGES = [
    { id: "all", label: "All Time" },
    { id: "15m", label: "Last 15 minutes" },
    { id: "1h", label: "Last 1 hour" },
    { id: "6h", label: "Last 6 hours" },
    { id: "24h", label: "Last 24 hours" },
    { id: "7d", label: "Last 7 days" },
    { id: "1mo", label: "Last 1 month" },
    { id: "today", label: "Today" }
  ];

  const buildQuery = useCallback((p: number) => {
    const params = new URLSearchParams();
    if (keyFilter.trim()) params.set("key", keyFilter.trim());
    if (providerFilter) params.set("provider", providerFilter);
    if (modelFilter.trim()) params.set("model", modelFilter.trim());
    if (timeRange !== "all") {
      const now = new Date();
      let from: Date | null = null;
      if (timeRange === "today") {
        from = new Date(now.getFullYear(), now.getMonth(), now.getDate());
      } else {
        const minutes: Record<string, number> = { "15m": 15, "1h": 60, "6h": 360, "24h": 1440, "7d": 10080, "1mo": 43200 };
        if (minutes[timeRange]) from = new Date(now.getTime() - minutes[timeRange] * 60000);
      }
      if (from) params.set("from", from.toISOString());
    }
    params.set("limit", String(PAGE_SIZE));
    if (p > 1) params.set("offset", String((p - 1) * PAGE_SIZE));
    return params.toString();
  }, [keyFilter, providerFilter, modelFilter, timeRange]);

  const fetchLogs = useCallback(async (p: number) => {
    try {
      const q = buildQuery(p);
      const [logsRes, statsRes] = await Promise.all([
        apiFetch(`/api/request-logs?${q}`),
        apiFetch(`/api/request-logs/stats?${q}`)
      ]);
      if (logsRes.ok) setLogs(await logsRes.json());
      if (statsRes.ok) setStats(await statsRes.json());
    } catch {
      // ignore polling errors
    }
  }, [buildQuery]);

  useEffect(() => {
    fetchLogs(page);
  }, [timeRange, page]);

  const handleApplyFilter = async () => {
    setLoading(true);
    setPage(1);
    await fetchLogs(1);
    setLoading(false);
  };

  const handleRefresh = async () => {
    await fetchLogs(page);
  };

  const fmtNum = (n: number) => (n || 0).toLocaleString();
  const fmtCompact = (n: number) => {
    const v = n || 0;
    if (v >= 1e9) return (v / 1e9).toFixed(2).replace(/\.?0+$/, "") + "G";
    if (v >= 1e6) return (v / 1e6).toFixed(2).replace(/\.?0+$/, "") + "M";
    if (v >= 1e3) return (v / 1e3).toFixed(2).replace(/\.?0+$/, "") + "K";
    return String(v);
  };
  const fmtTime = (ts: string) =>
    new Date(ts).toLocaleString([], { year: "2-digit", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" });

  return (
    <div className="space-y-4">
      {/* Filter / control bar */}
      <div className="bg-white border border-neutral-200 rounded-2xl shadow-sm p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center space-x-2">
            <div className="p-2 rounded-xl bg-blue-50 text-blue-600">
              <Activity className="w-4 h-4" />
            </div>
            <h4 className="text-xs font-bold uppercase tracking-wider text-neutral-600">Request Filters</h4>
          </div>
          <div className="flex items-center space-x-2">
            <button
              onClick={handleApplyFilter}
              title="Apply filters & refresh"
              className="px-2.5 py-1 rounded-lg text-[10px] uppercase font-bold tracking-wider bg-neutral-900 text-white hover:bg-neutral-800 transition-colors cursor-pointer"
            >
              {loading ? "..." : "Apply"}
            </button>
            <button
              onClick={handleRefresh}
              title="Refresh request logs"
              className="p-1.5 hover:bg-neutral-100 rounded-lg text-neutral-500 hover:text-neutral-800 transition-colors cursor-pointer"
            >
              <RefreshCw className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
          <div className="space-y-1">
            <label className="text-[10px] font-bold uppercase tracking-wider text-neutral-400">Key</label>
            <select
              value={keyFilter}
              onChange={(e) => setKeyFilter(e.target.value)}
              className="w-full bg-white border border-neutral-200 rounded-lg px-3 py-1.5 text-xs text-neutral-800 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 cursor-pointer"
            >
              <option value="">All Keys</option>
              {virtualKeys.map((k) => (
                <option key={k.key} value={k.name}>{k.name}</option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label className="text-[10px] font-bold uppercase tracking-wider text-neutral-400">Model</label>
            <input
              type="text"
              placeholder="All models"
              value={modelFilter}
              onChange={(e) => setModelFilter(e.target.value)}
              className="w-full bg-white border border-neutral-200 rounded-lg px-3 py-1.5 text-xs text-neutral-800 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400"
            />
          </div>
          <div className="space-y-1">
            <label className="text-[10px] font-bold uppercase tracking-wider text-neutral-400">Provider</label>
            <select
              value={providerFilter}
              onChange={(e) => setProviderFilter(e.target.value)}
              className="w-full bg-white border border-neutral-200 rounded-lg px-3 py-1.5 text-xs text-neutral-800 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 cursor-pointer"
            >
              <option value="">All Providers</option>
              {providers.map((p) => (
                <option key={p.id} value={p.name}>{p.name}</option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label className="text-[10px] font-bold uppercase tracking-wider text-neutral-400">Time Range</label>
            <select
              value={timeRange}
              onChange={(e) => { setTimeRange(e.target.value); setPage(1); }}
              className="w-full bg-white border border-neutral-200 rounded-lg px-3 py-1.5 text-xs text-neutral-800 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400"
            >
              {TIME_RANGES.map((r) => (
                <option key={r.id} value={r.id}>{r.label}</option>
              ))}
            </select>
          </div>
        </div>
      </div>

      {/* Stats summary */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        {[
          { label: "Requests", value: fmtCompact(stats?.count || 0), icon: <Activity className="w-4 h-4" />, color: "text-neutral-700 bg-neutral-100" },
          { label: "Read", value: fmtCompact(stats?.promptTokens || 0), icon: <ArrowDownToLine className="w-4 h-4" />, color: "text-blue-700 bg-blue-50" },
          { label: "Cached", value: fmtCompact(stats?.cachedTokens || 0), icon: <Database className="w-4 h-4" />, color: "text-violet-700 bg-violet-50" },
          { label: "Write", value: fmtCompact(stats?.completionTokens || 0), icon: <ArrowUpFromLine className="w-4 h-4" />, color: "text-emerald-700 bg-emerald-50" },
          { label: "Total", value: fmtCompact(stats?.totalTokens || 0), icon: <Cpu className="w-4 h-4" />, color: "text-amber-700 bg-amber-50" }
        ].map((s) => (
          <div key={s.label} className="bg-white border border-neutral-200 rounded-2xl shadow-sm p-4">
            <div className="flex items-center space-x-2">
              <span className={`p-2 rounded-lg ${s.color}`}>{s.icon}</span>
              <span className="text-[10px] font-bold uppercase tracking-wider text-neutral-400">{s.label}</span>
            </div>
            <p className="mt-2 text-lg font-bold text-neutral-800 font-mono">{s.value}</p>
          </div>
        ))}
      </div>

      {/* Log table */}
      <div className="bg-white border border-neutral-200 rounded-2xl shadow-sm overflow-hidden">
        <div className="bg-neutral-50 px-5 py-3 border-b border-neutral-200 flex items-center space-x-2">
          <Filter className="w-3.5 h-3.5 text-neutral-400" />
          <span className="text-xs font-bold uppercase tracking-wider text-neutral-600">Request Usage Logs</span>
          <span className="text-[10px] text-neutral-400">({stats?.count || 0} total · page {page} of {Math.max(1, Math.ceil((stats?.count || 0) / PAGE_SIZE))})</span>
        </div>
        {logs.length === 0 ? (
          <div className="p-10 text-center text-neutral-400 text-xs">
            No request logs match the current filters. API calls routed through <code className="font-mono bg-neutral-100 px-1 py-0.5 rounded">/v1/*</code> will appear here.
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse min-w-[900px]">
              <thead>
                <tr className="border-b border-neutral-150 text-[10px] uppercase font-bold tracking-wider text-neutral-400">
                  <th className="py-3 pl-5 pr-3">Time</th>
                  <th className="py-3 px-3">Key</th>
                  <th className="py-3 px-3">Model</th>
                  <th className="py-3 px-3">Provider</th>
                  <th className="py-3 px-3 text-right">Read</th>
                  <th className="py-3 px-3 text-right">Cached</th>
                  <th className="py-3 px-3 text-right">Write</th>
                  <th className="py-3 px-3 text-right">Total</th>
                  <th className="py-3 px-3 text-center">Status</th>
                  <th className="py-3 pl-3 pr-5 text-right">Duration</th>
                  <th className="py-3 pl-3 pr-5 text-center">Logs</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-neutral-100 text-xs text-neutral-700">
                {logs.map((log) => (
                  <tr key={log.id} className="hover:bg-neutral-50/50 transition-colors align-top">
                    <td className="py-2.5 pl-5 pr-3 font-mono text-[10px] text-neutral-500 whitespace-nowrap">{fmtTime(log.timestamp)}</td>
                    <td className="py-2.5 px-3">
                      <span className="inline-flex items-center space-x-1">
                        <span className="font-semibold text-neutral-800">{log.keyName || <span className="text-neutral-400 font-normal">—</span>}</span>
                        {log.keyId && (
                          <span className="font-mono text-[9px] text-neutral-400" title="Masked API key">{log.keyId}</span>
                        )}
                      </span>
                    </td>
                    <td className="py-2.5 px-3 font-mono text-[11px] text-blue-700 whitespace-nowrap">{log.model || "—"}</td>
                    <td className="py-2.5 px-3 text-neutral-600 whitespace-nowrap">{log.provider || "—"}</td>
                    <td className="py-2.5 px-3 text-right font-mono text-neutral-700">{fmtNum(log.promptTokens)}</td>
                    <td className="py-2.5 px-3 text-right font-mono text-violet-600">{fmtNum(log.cachedTokens)}</td>
                    <td className="py-2.5 px-3 text-right font-mono text-emerald-700">{fmtNum(log.completionTokens)}</td>
                    <td className="py-2.5 px-3 text-right font-mono font-bold text-neutral-800">{fmtNum(log.totalTokens)}</td>
                    <td className="py-2.5 px-3 text-center">
                      <span className={`px-1.5 py-0.5 rounded-full text-[9px] font-bold ${log.status >= 200 && log.status < 300 ? "bg-emerald-100 text-emerald-700" : log.status >= 400 ? "bg-rose-100 text-rose-700" : "bg-amber-100 text-amber-700"}`}>
                        {log.status || "ERR"}
                      </span>
                      {log.stream && <span className="ml-1 text-[9px] text-neutral-400">stream</span>}
                    </td>
                    <td className="py-2.5 pl-3 pr-5 text-right font-mono text-neutral-500 whitespace-nowrap">{log.durationMs}ms</td>
                    <td className="py-2.5 pl-3 pr-5 text-center whitespace-nowrap">
                      {log.requestId && onViewLogs ? (
                        <button
                          onClick={() => onViewLogs(log.requestId!, !!log.hasDetail)}
                          title={log.hasDetail ? "View related log details" : "View related summary logs (no detailed logs were recorded for this request)"}
                          className={`inline-flex items-center space-x-1 px-2 py-1 rounded-lg text-[10px] font-bold transition-colors cursor-pointer ${
                            log.hasDetail
                              ? "bg-neutral-900 text-white hover:bg-neutral-800"
                              : "bg-neutral-100 text-neutral-500 hover:bg-neutral-200 border border-neutral-200"
                          }`}
                        >
                          <FileSearch className="w-3 h-3" />
                          <span>{log.hasDetail ? "Detail" : "Summary"}</span>
                        </button>
                      ) : (
                        <span className="text-neutral-300 text-[10px]">—</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {Math.ceil((stats?.count || 0) / PAGE_SIZE) > 1 && (
          <div className="px-5 py-3 border-t border-neutral-200 flex items-center justify-between">
            <span className="text-[10px] text-neutral-400">
              Showing {(page - 1) * PAGE_SIZE + 1}–{Math.min(page * PAGE_SIZE, stats?.count || 0)} of {stats?.count || 0}
            </span>
            <div className="flex items-center space-x-1">
              <button
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={page <= 1}
                className="px-2.5 py-1 rounded-lg text-[10px] font-bold uppercase tracking-wider bg-neutral-900 text-white hover:bg-neutral-800 disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
              >
                Prev
              </button>
              {Array.from({ length: Math.ceil((stats?.count || 0) / PAGE_SIZE) }, (_, i) => i + 1)
                .filter((p) => p === 1 || p === Math.ceil((stats?.count || 0) / PAGE_SIZE) || Math.abs(p - page) <= 1)
                .reduce<number[]>((acc, p) => {
                  if (acc.length && p - acc[acc.length - 1] > 1) acc.push(NaN);
                  acc.push(p);
                  return acc;
                }, [])
                .map((p, i) =>
                  Number.isNaN(p) ? (
                    <span key={`ellipsis-${i}`} className="px-1 text-[10px] text-neutral-400">…</span>
                  ) : (
                    <button
                      key={p}
                      onClick={() => setPage(p)}
                      className={`w-6 h-6 rounded-lg text-[10px] font-bold transition-colors cursor-pointer ${p === page ? "bg-neutral-900 text-white" : "text-neutral-500 hover:bg-neutral-100"}`}
                    >
                      {p}
                    </button>
                  )
                )}
              <button
                onClick={() => setPage((p) => p + 1)}
                disabled={page >= Math.ceil((stats?.count || 0) / PAGE_SIZE)}
                className="px-2.5 py-1 rounded-lg text-[10px] font-bold uppercase tracking-wider bg-neutral-900 text-white hover:bg-neutral-800 disabled:opacity-40 disabled:cursor-not-allowed transition-colors cursor-pointer"
              >
                Next
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
