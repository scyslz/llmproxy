import { useState, useEffect, useRef } from "react";
import { SystemLog, LogStatus } from "../types";
import { Trash2, AlertCircle, Info, AlertTriangle, RefreshCw, FileText, Filter, X, FileSearch } from "lucide-react";

interface TerminalLogsProps {
  logs: SystemLog[];
  onClear: () => void;
  onRefresh: () => void;
  isPolling: boolean;
  setIsPolling: (polling: boolean) => void;
  viewRequestId?: string;
  onClearView?: () => void;
}

export default function TerminalLogs({ logs, onClear, onRefresh, isPolling, setIsPolling, viewRequestId, onClearView }: TerminalLogsProps) {
  const [levelFilter, setLevelFilter] = useState<"all" | "info" | "warn" | "error">("all");
  const [categoryFilter, setCategoryFilter] = useState<"all" | "proxy" | "system">("all");
  const [logStatus, setLogStatus] = useState<LogStatus | null>(null);
  const [viewLogs, setViewLogs] = useState<SystemLog[] | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  const fetchStatus = async () => {
    try {
      const res = await fetch("/api/logs/status");
      if (res.ok) {
        const data = await res.json();
        setLogStatus(data);
      }
    } catch {}
  };

  useEffect(() => {
    fetchStatus();
  }, [logs]);

  useEffect(() => {
    if (!viewRequestId) {
      setViewLogs(null);
      return;
    }
    let cancelled = false;
    const load = async () => {
      try {
        const res = await fetch(`/api/logs?requestId=${encodeURIComponent(viewRequestId)}&limit=200`);
        if (!res.ok) return;
        const data = await res.json();
        const entries = Array.isArray(data) ? data : data?.logs ?? [];
        if (!cancelled) setViewLogs(entries);
      } catch {}
    };
    load();
    const t = setInterval(load, 2500);
    return () => { cancelled = true; clearInterval(t); };
  }, [viewRequestId]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs, levelFilter, categoryFilter]);

  const displayLogs = viewRequestId ? (viewLogs ?? []) : logs;

  const filteredLogs = displayLogs.filter((log) => {
    if (levelFilter !== "all" && log.level !== levelFilter) return false;
    if (categoryFilter !== "all") {
      const cat = log.category || "system";
      if (cat !== categoryFilter) return false;
    }
    return true;
  });

  const getLevelColor = (level: "info" | "warn" | "error") => {
    switch (level) {
      case "error":
        return "text-rose-400 font-bold bg-rose-500/10 px-1.5 py-0.5 rounded border border-rose-500/20";
      case "warn":
        return "text-amber-300 font-bold bg-amber-500/10 px-1.5 py-0.5 rounded border border-amber-500/20";
      default:
        return "text-cyan-300 font-bold bg-cyan-500/10 px-1.5 py-0.5 rounded border border-cyan-500/20";
    }
  };

  const getLevelIcon = (level: "info" | "warn" | "error") => {
    switch (level) {
      case "error":
        return <AlertCircle className="w-3.5 h-3.5 inline mr-1 text-rose-400 shrink-0" />;
      case "warn":
        return <AlertTriangle className="w-3.5 h-3.5 inline mr-1 text-amber-300 shrink-0" />;
      default:
        return <Info className="w-3.5 h-3.5 inline mr-1 text-cyan-400 shrink-0" />;
    }
  };

  const getCategoryBadge = (category?: string) => {
    const cat = category || "system";
    switch (cat) {
      case "proxy":
        return <span className="bg-blue-500/20 text-blue-300 text-[9px] px-1.5 py-0.5 rounded font-bold uppercase tracking-wider mr-1 border border-blue-500/30 select-none">PROXY</span>;
      default:
        return <span className="bg-purple-500/20 text-purple-300 text-[9px] px-1.5 py-0.5 rounded font-bold uppercase tracking-wider mr-1 border border-purple-500/30 select-none">SYSTEM</span>;
    }
  };

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  };

  return (
    <div className="bg-white border border-neutral-200 rounded-2xl overflow-hidden shadow-xs font-mono text-xs text-neutral-800">
      {/* Terminal Title Bar */}
      <div className="bg-neutral-50 px-4 py-3 border-b border-neutral-200 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center space-x-2">
          <div className="flex space-x-1.5">
            <span className="w-3 h-3 rounded-full bg-red-400 inline-block"></span>
            <span className="w-3 h-3 rounded-full bg-amber-400 inline-block"></span>
            <span className="w-3 h-3 rounded-full bg-emerald-400 inline-block"></span>
          </div>
          <span className="text-neutral-600 text-xs font-semibold select-none ml-2">LLM-PROXY@SYSTEM-LOGGER</span>
          {logStatus && (
            <span className="bg-neutral-900 text-white text-[10px] px-2 py-0.5 rounded-md flex items-center gap-1 font-sans">
              <FileText className="w-3 h-3 text-emerald-400" />
              <span>system_logs.db: <strong>{formatSize(logStatus.totalSize)}</strong> / {logStatus.maxLogSizeMB} MB cap</span>
            </span>
          )}
        </div>
        <div className="flex items-center space-x-2">
          <button
            onClick={() => setIsPolling(!isPolling)}
            className={`px-2 py-1 rounded-lg text-[10px] uppercase font-bold tracking-wider transition-colors cursor-pointer ${
              isPolling ? "bg-emerald-50 text-emerald-700 border border-emerald-200" : "bg-neutral-100 text-neutral-600 border border-neutral-200"
            }`}
          >
            {isPolling ? "• Live Polling" : "Paused"}
          </button>
          <button
            onClick={() => { onRefresh(); fetchStatus(); }}
            title="Refresh logs"
            className="p-1 hover:bg-neutral-100 rounded-lg text-neutral-500 hover:text-neutral-800 transition-colors cursor-pointer"
          >
            <RefreshCw className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={onClear}
            title="Clear all logs"
            className="p-1 hover:bg-red-50 rounded-lg text-red-500 hover:text-red-700 transition-colors cursor-pointer"
          >
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      {/* Request Detail View Banner */}
      {viewRequestId && (
        <div className="bg-amber-50/60 border-b border-amber-200 px-4 py-2 flex items-center justify-between gap-2">
          <div className="flex items-center space-x-2 text-[11px] text-amber-800">
            <FileSearch className="w-3.5 h-3.5" />
            <span className="font-bold">Viewing logs for request</span>
            <code className="font-mono text-[10px] bg-white border border-amber-200 px-1.5 py-0.5 rounded">{viewRequestId}</code>
            <span className="text-amber-700/80">({filteredLogs.length} entries)</span>
          </div>
          <button
            onClick={onClearView}
            className="inline-flex items-center space-x-1 px-2 py-1 rounded-lg text-[10px] font-bold bg-white border border-amber-200 text-amber-800 hover:bg-amber-100 transition-colors cursor-pointer"
          >
            <X className="w-3 h-3" />
            <span>Exit request view</span>
          </button>
        </div>
      )}

      {/* Terminal Filter Bar */}
      <div className="bg-neutral-50/70 px-4 py-2 border-b border-neutral-200 flex flex-wrap items-center justify-between gap-2 text-[11px]">
        <div className="flex items-center space-x-1.5 overflow-x-auto">
          <Filter className="w-3 h-3 text-neutral-400 mr-1" />
          <span className="text-neutral-500 font-bold uppercase text-[10px] tracking-wider">Category:</span>
          {[
            { id: "all", label: "All Logs" },
            { id: "system", label: "⚙️ System" },
            { id: "proxy", label: "⚡ Proxy" },
          ].map((cat) => (
            <button
              key={cat.id}
              onClick={() => setCategoryFilter(cat.id as any)}
              className={`px-2.5 py-0.5 rounded-full text-[10px] font-sans font-semibold transition-colors cursor-pointer ${
                categoryFilter === cat.id
                  ? "bg-blue-600 text-white font-bold shadow-2xs"
                  : "text-neutral-600 hover:text-neutral-900 hover:bg-neutral-100 border border-transparent"
              }`}
            >
              {cat.label}
            </button>
          ))}
        </div>

        <div className="flex items-center space-x-1">
          <span className="text-neutral-400 font-bold uppercase text-[10px] mr-1">Level:</span>
          {(["all", "info", "warn", "error"] as const).map((lvl) => (
            <button
              key={lvl}
              onClick={() => setLevelFilter(lvl)}
              className={`px-2 py-0.5 rounded-full text-[10px] uppercase tracking-wider font-semibold transition-colors cursor-pointer ${
                levelFilter === lvl
                  ? "bg-neutral-900 text-white font-bold shadow-2xs"
                  : "text-neutral-500 hover:text-neutral-900 hover:bg-neutral-100"
              }`}
            >
              {lvl === "all" ? "All" : lvl}
            </button>
          ))}
        </div>
      </div>

      {/* Terminal Body */}
      <div className="p-4 h-[380px] overflow-y-auto space-y-2 scrollbar-thin scrollbar-thumb-neutral-200 bg-neutral-950 text-neutral-100 font-mono text-[11px]">
        {filteredLogs.length === 0 ? (
          <div className="h-full flex flex-col items-center justify-center text-neutral-500 space-y-2 font-sans">
            <span className="font-mono font-bold select-none text-neutral-400">[ NO MATCHING LOGS ]</span>
            <span className="text-xs text-center max-w-[300px]">
              No log records match the current filter criteria. Active proxy requests and system operations will appear here in real-time.
            </span>
          </div>
        ) : (
          filteredLogs.map((log, index) => (
            <div
              key={log.id ?? index}
              className="px-3 py-2 rounded-lg transition-colors hover:bg-neutral-900/50 opacity-85"
            >
              <div className="flex items-center gap-x-2 flex-wrap">
                <span className="text-neutral-500 select-none font-mono text-[10px]">
                  {new Date(log.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
                </span>
                {getCategoryBadge(log.category)}
                <span className={`${getLevelColor(log.level)} uppercase select-none font-bold text-[10px]`}>
                  [{log.level}]
                </span>
              </div>
              <div className="mt-1 text-neutral-200 break-all font-mono whitespace-pre-wrap leading-relaxed">
                {getLevelIcon(log.level)}
                {log.message}
              </div>
            </div>
          ))
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
