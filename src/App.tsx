import { useState, useEffect, useCallback, useRef } from "react";
import { Provider, VirtualKey, SystemLog, ProviderGroup, GroupTestEntry } from "./types";
import { motion, AnimatePresence } from "motion/react";
import Header from "./components/Header";
import ProviderCard from "./components/ProviderCard";
import KeyManager from "./components/KeyManager";
import GroupCard from "./components/GroupCard";
import Playground from "./components/Playground";
import TerminalLogs from "./components/TerminalLogs";
import RequestLogs from "./components/RequestLogs";
import Login from "./components/Login";
import SecuritySettings from "./components/SecuritySettings";
import { apiFetch, setAdminToken } from "./lib/api";
import { Cpu, Terminal, Shield, Sparkles, CheckCircle2 } from "lucide-react";

export default function App() {
  const [activeTab, setActiveTab] = useState<"providers" | "groups" | "keys" | "playground" | "logs" | "requestlogs" | "settings">("providers");
  const [enableAdminAuth, setEnableAdminAuth] = useState(false);
  const [isAuthenticated, setIsAuthenticated] = useState<boolean | null>(null);
  
  // App state
  const [providers, setProviders] = useState<Provider[]>([]);
  const [virtualKeys, setVirtualKeys] = useState<VirtualKey[]>([]);
  const [groups, setGroups] = useState<ProviderGroup[]>([]);
  const [enableVirtualKey, setEnableVirtualKey] = useState(false);
  const [logs, setLogs] = useState<SystemLog[]>([]);
  const [viewRequestId, setViewRequestId] = useState<string | null>(null);
  
  // Loading & Error states
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isPollingLogs, setIsPollingLogs] = useState(true);
  const [playgroundKey, setPlaygroundKey] = useState("");
  const [playgroundModel, setPlaygroundModel] = useState("");
  const [groupTestResults, setGroupTestResults] = useState<GroupTestEntry[] | null>(null);
  const [isTestingGroup, setIsTestingGroup] = useState(false);
  const lastLogIdRef = useRef(0);

  const fetchSystemLogs = useCallback(async () => {
    try {
      const res = await apiFetch("/api/logs?limit=15");
      if (res.ok) {
        const data = await res.json();
        const logsData = Array.isArray(data) ? data : data?.logs ?? [];
        setLogs(logsData);
        if (logsData.length > 0) lastLogIdRef.current = logsData[logsData.length - 1].id ?? 0;
      }
    } catch {}
  }, []);

  // Listen for unauthorized events
  useEffect(() => {
    const handleUnauthorized = () => {
      setIsAuthenticated(false);
    };
    window.addEventListener("admin-unauthorized", handleUnauthorized);
    return () => window.removeEventListener("admin-unauthorized", handleUnauthorized);
  }, []);

  // Fetch initial data
  const fetchData = async () => {
    try {
      setIsLoading(true);
      setError(null);
      
      const statusRes = await apiFetch("/api/admin/status");
      if (statusRes.ok) {
        const statusData = await statusRes.json();
        setEnableAdminAuth(statusData.enableAdminAuth);
        setIsAuthenticated(statusData.isAuthenticated);
        if (statusData.enableAdminAuth && !statusData.isAuthenticated) {
          setIsLoading(false);
          return;
        }
      }

      const [provRes, keyRes, grpRes, setRes, logRes] = await Promise.all([
        apiFetch("/api/providers"),
        apiFetch("/api/keys"),
        apiFetch("/api/groups"),
        apiFetch("/api/settings"),
        apiFetch("/api/logs?limit=15")
      ]);

      if (!provRes.ok || !keyRes.ok || !grpRes.ok || !setRes.ok || !logRes.ok) {
        throw new Error("Failed to sync some application settings from server.");
      }

      const providersData = await provRes.json();
      const keysData = await keyRes.json();
      setGroups(await grpRes.json());
      const settingsData = await setRes.json();
      const logsDataRaw = await logRes.json();

      setProviders(providersData);
      setVirtualKeys(keysData);
      setEnableVirtualKey(settingsData.enableVirtualKey);
      setEnableAdminAuth(settingsData.enableAdminAuth || false);
      const logsData = Array.isArray(logsDataRaw) ? logsDataRaw : logsDataRaw?.logs ?? [];
      setLogs(logsData);
      if (logsData.length > 0) lastLogIdRef.current = logsData[logsData.length - 1].id ?? 0;
    } catch (err: any) {
      console.error(err);
      setError(err.message || "An unexpected error occurred during startup data fetch.");
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  // Poll server logs: fetch incrementally since the last seen id.
  // Total stored logs are bounded by backend maxLogSizeMB rotation, not by page limits.
  useEffect(() => {
    if (!isPollingLogs || (enableAdminAuth && isAuthenticated === false)) return;

    const interval = setInterval(async () => {
      try {
        const res = await apiFetch(`/api/logs?since=${lastLogIdRef.current}&limit=500`);
        if (res.ok) {
          const data = await res.json();
          const newLogs = Array.isArray(data) ? data : data?.logs ?? [];
          if (newLogs.length > 0) {
            setLogs((prev) => {
              const merged = [...prev, ...newLogs];
              return merged.length > 2000 ? merged.slice(merged.length - 2000) : merged;
            });
            lastLogIdRef.current = newLogs[newLogs.length - 1].id ?? lastLogIdRef.current;
          }
        }
      } catch (e) {
        // ignore log polling errors to prevent spamming
      }
    }, 2500);

    return () => clearInterval(interval);
  }, [isPollingLogs, enableAdminAuth, isAuthenticated]);

  // Provider operations
  const handleEnableProvider = async (id: string) => {
    try {
      const res = await apiFetch(`/api/providers/${id}/enable`, { method: "POST" });
      if (!res.ok) throw new Error("Failed to enable provider");
      
      // Update local state instantly
      setProviders((prev) =>
        prev.map((p) => ({ ...p, enabled: p.id === id }))
      );
      
      // Force refresh logs
      await fetchSystemLogs();
    } catch (err: any) {
      alert(`Error enabling provider: ${err.message}`);
    }
  };

  const handleCreateProvider = async (pData: Omit<Provider, "enabled">) => {
    try {
      const res = await apiFetch("/api/providers", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(pData)
      });
      if (!res.ok) throw new Error("Failed to create provider");
      const created = await res.json();
      setProviders((prev) => [...prev, created]);
    } catch (err: any) {
      alert(`Error creating provider: ${err.message}`);
    }
  };

  const handleUpdateProvider = async (id: string, pData: Partial<Provider>) => {
    try {
      const res = await apiFetch(`/api/providers/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(pData)
      });
      if (!res.ok) throw new Error("Failed to update provider");
      const updated = await res.json();
      setProviders((prev) => prev.map((p) => (p.id === id ? updated : p)));
    } catch (err: any) {
      alert(`Error updating provider: ${err.message}`);
    }
  };

  const handleDeleteProvider = async (id: string) => {
    try {
      const res = await apiFetch(`/api/providers/${id}`, { method: "DELETE" });
      if (!res.ok) throw new Error("Failed to delete provider");
      setProviders((prev) => prev.filter((p) => p.id !== id));
    } catch (err: any) {
      alert(`Error deleting provider: ${err.message}`);
    }
  };

  // Virtual Key operations
  const handleToggleVirtualKey = async (enabled: boolean) => {
    try {
      const res = await apiFetch("/api/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enableVirtualKey: enabled })
      });
      if (!res.ok) throw new Error("Failed to update settings");
      setEnableVirtualKey(enabled);
    } catch (err: any) {
      alert(`Error updating settings: ${err.message}`);
    }
  };

  const handleCreateKey = async (name: string, providerIds: string[], groupId?: string) => {
    try {
      const res = await apiFetch("/api/keys", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, providerIds, groupId })
      });
      if (!res.ok) throw new Error("Failed to create virtual key");
      const created = await res.json();
      setVirtualKeys((prev) => [...prev, created]);
    } catch (err: any) {
      alert(`Error generating virtual key: ${err.message}`);
    }
  };

  const handleDeleteKey = async (keyStr: string) => {
    try {
      const res = await apiFetch(`/api/keys/${keyStr}`, { method: "DELETE" });
      if (!res.ok) throw new Error("Failed to delete key");
      setVirtualKeys((prev) => prev.filter((k) => k.key !== keyStr));
    } catch (err: any) {
      alert(`Error deleting key: ${err.message}`);
    }
  };

  const handleUpdateKey = async (keyStr: string, name: string, providerIds: string[], groupId?: string) => {
    try {
      const res = await apiFetch(`/api/keys/${keyStr}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, providerIds, groupId })
      });
      if (!res.ok) throw new Error("Failed to update virtual key");
      const updated = await res.json();
      setVirtualKeys((prev) => prev.map((k) => (k.key === keyStr ? updated : k)));
    } catch (err: any) {
      alert(`Error updating virtual key: ${err.message}`);
    }
  };

  // Admin Auth operations
  const handleUpdateAdminAuth = async (enabled: boolean, newPassword?: string) => {
    const body: any = { enableAdminAuth: enabled };
    if (newPassword) body.adminPassword = newPassword;
    const res = await apiFetch("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    if (!res.ok) throw new Error("Failed to update security settings");
    const data = await res.json();
    setEnableAdminAuth(data.enableAdminAuth);
  };

  const handleLogout = async () => {
    await apiFetch("/api/admin/logout", { method: "POST" });
    setAdminToken(null);
    setIsAuthenticated(false);
  };

  // Logs operations
  const handleClearLogs = async () => {
    try {
      await apiFetch("/api/logs/clear", { method: "POST" });
      setLogs([]);
      lastLogIdRef.current = 0;
    } catch (e) {
      // ignore
    }
  };

  const handleRefreshLogs = async () => {
    await fetchSystemLogs();
  };

  const activeProviderName = providers.find((p) => p.enabled)?.name || "No Active Router";

  // Generated curl command reflects the playground selections; the proxy path is always /v1/chat/completions
  const proxyOrigin = typeof window !== "undefined" ? window.location.origin : "http://localhost:3000";
  const curlEndpoint = `${proxyOrigin}/v1/chat/completions`;
  const curlKey = enableVirtualKey ? (playgroundKey || "YOUR_VIRTUAL_KEY") : "ANY_KEY";
  const curlModel = playgroundModel || "any-supported-model";

  if (isLoading) {
    return (
      <div className="min-h-screen bg-neutral-50/50 flex flex-col items-center justify-center space-y-4">
        <div className="bg-white border border-neutral-200 text-neutral-800 p-4 rounded-2xl shadow-sm animate-bounce">
          <Cpu className="w-8 h-8 animate-spin" />
        </div>
        <p className="font-display font-semibold text-neutral-800 text-sm">Initializing LLM Proxy Engine...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="min-h-screen bg-neutral-50/50 flex flex-col items-center justify-center p-6 text-center">
        <div className="bg-red-50 text-red-800 border border-red-200 rounded-2xl p-6 max-w-md shadow-md space-y-3">
          <h2 className="font-display font-bold text-base">Server Sync Failure</h2>
          <p className="text-sm leading-relaxed">{error}</p>
          <button
            onClick={fetchData}
            className="bg-neutral-900 text-white font-semibold text-xs px-4 py-2.5 rounded-xl hover:bg-neutral-800 transition-colors cursor-pointer shadow-xs"
          >
            Retry Connection
          </button>
        </div>
      </div>
    );
  }

  if (isAuthenticated === false && enableAdminAuth) {
    return (
      <Login
        onLoginSuccess={() => {
          setIsAuthenticated(true);
          fetchData();
        }}
      />
    );
  }

  return (
    <div className="min-h-screen bg-neutral-50/50 flex flex-col font-sans">
      <Header
        providers={providers}
        virtualKeys={virtualKeys}
        enableVirtualKey={enableVirtualKey}
        enableAdminAuth={enableAdminAuth}
        activeTab={activeTab}
        setActiveTab={setActiveTab}
      />

      <main className="flex-1 max-w-7xl w-full mx-auto p-6 md:p-8">
        <AnimatePresence mode="wait">
          {activeTab === "providers" && (
            <motion.div
              key="providers"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
            >
              <ProviderCard
                providers={providers}
                onEnable={handleEnableProvider}
                onCreate={handleCreateProvider}
                onUpdate={handleUpdateProvider}
                onDelete={handleDeleteProvider}
              />
            </motion.div>
          )}

          {activeTab === "groups" && (
            <motion.div
              key="groups"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
            >
              <GroupCard
                groups={groups}
                providers={providers}
                onCreate={async (g) => {
                  const res = await apiFetch("/api/groups", { method: "POST", body: JSON.stringify(g) });
                  if (!res.ok) throw new Error("Failed to create group");
                  const created = await res.json();
                  setGroups((prev) => [...prev, created]);
                }}
                onUpdate={async (id, patch) => {
                  const res = await apiFetch("/api/groups/" + id, { method: "PUT", body: JSON.stringify(patch) });
                  if (!res.ok) throw new Error("Failed to update group");
                  const updated = await res.json();
                  setGroups((prev) => prev.map((g) => g.id === id ? updated : g));
                }}
                onDelete={async (id) => {
                  const res = await apiFetch("/api/groups/" + id, { method: "DELETE" });
                  if (!res.ok) throw new Error("Failed to delete group");
                  setGroups((prev) => prev.filter((g) => g.id !== id));
                  setGroupTestResults(null);
                }}
                onTest={async (id) => {
                  setIsTestingGroup(true);
                  try {
                    const res = await apiFetch("/api/groups/" + id + "/test");
                    if (!res.ok) throw new Error("Test failed");
                    setGroupTestResults(await res.json());
                  } finally {
                    setIsTestingGroup(false);
                  }
                }}
                testResults={groupTestResults}
                isTesting={isTestingGroup}
              />
            </motion.div>
          )}

          {activeTab === "keys" && (
            <motion.div
              key="keys"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
            >
              <KeyManager
                keys={virtualKeys}
                providers={providers}
                groups={groups}
                enableVirtualKey={enableVirtualKey}
                onToggleVirtualKey={handleToggleVirtualKey}
                onCreateKey={handleCreateKey}
                onDeleteKey={handleDeleteKey}
                onUpdateKey={handleUpdateKey}
              />
            </motion.div>
          )}

          {activeTab === "playground" && (
            <motion.div
              key="playground"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
              className="space-y-6"
            >
              <div>
                <h3 className="font-display font-semibold text-neutral-800 text-base">API Playground</h3>
                <p className="text-xs text-neutral-500">Test completions and stream tokens directly through your proxy</p>
              </div>
              <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 items-start">
                <div className="lg:col-span-2">
                  <Playground virtualKeys={virtualKeys} providers={providers} activeProviderName={activeProviderName} enableVirtualKey={enableVirtualKey} onStateChange={(key, model) => { setPlaygroundKey(key); setPlaygroundModel(model); }} />
                </div>
                <div className="bg-white border border-neutral-200 rounded-2xl p-5 shadow-sm space-y-3.5">
                  <h4 className="text-xs font-bold uppercase tracking-wider text-neutral-600 flex items-center space-x-1.5">
                    <Terminal className="w-4 h-4 text-neutral-500" />
                    <span>Integration Code Snippet</span>
                  </h4>
                  <p className="text-xs text-neutral-500 leading-relaxed">
                    This proxy is fully OpenAI-compatible. You can switch any client SDK endpoint to this service URL.
                  </p>
                  <div className="bg-slate-50 p-4 rounded-xl font-mono text-[11px] text-slate-800 space-y-2 overflow-x-auto border border-slate-200 select-all">
                    <div className="text-slate-400">// Shell/Curl integration example</div>
                    <div>
                      curl <span className="text-neutral-900 font-semibold">{curlEndpoint}</span> \
                    </div>
                    <div className="pl-2">
                      -H <span className="text-amber-700">"Authorization: Bearer {curlKey}"</span> \
                    </div>
                    <div className="pl-2">
                      -H <span className="text-amber-700">"Content-Type: application/json"</span> \
                    </div>
                    <div className="pl-2">-d '{"{"}</div>
                    <div className="pl-4">"model": "{curlModel}",</div>
                    <div className="pl-4">"messages": [{"{"}"role": "user", "content": "Hello!"{"}"}]</div>
                    <div className="pl-2">{"}"}'</div>
                  </div>
                </div>
              </div>
            </motion.div>
          )}

          {activeTab === "logs" && (
            <motion.div
              key="logs"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
              className="space-y-6"
            >
              <div>
                <h3 className="font-display font-semibold text-neutral-800 text-base">Full Logger System</h3>
                <p className="text-xs text-neutral-500">Live operational audit of all incoming headers, router mappings, and errors</p>
              </div>
              <TerminalLogs
                logs={logs}
                onClear={handleClearLogs}
                onRefresh={handleRefreshLogs}
                isPolling={isPollingLogs}
                setIsPolling={setIsPollingLogs}
                viewRequestId={viewRequestId || undefined}
                onClearView={() => setViewRequestId(null)}
              />
            </motion.div>
          )}

          {activeTab === "requestlogs" && (
            <motion.div
              key="requestlogs"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
              className="space-y-6"
            >
              <div>
                <h3 className="font-display font-semibold text-neutral-800 text-base">Request Usage</h3>
                <p className="text-xs text-neutral-500">Per-request token usage (prompt / cached / completion), key and model attribution, filterable by key and time range</p>
              </div>
              <RequestLogs
                  virtualKeys={virtualKeys}
                  providers={providers}
                  onViewLogs={(rid) => {
                    setViewRequestId(rid);
                    setActiveTab("logs");
                  }}
                />
            </motion.div>
          )}

          {activeTab === "settings" && (
            <motion.div
              key="settings"
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -15 }}
              transition={{ duration: 0.15 }}
            >
              <SecuritySettings
                enableVirtualKey={enableVirtualKey}
                onToggleVirtualKey={handleToggleVirtualKey}
                enableAdminAuth={enableAdminAuth}
                onUpdateAdminAuth={handleUpdateAdminAuth}
                onLogout={handleLogout}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </main>
    </div>
  );
}
