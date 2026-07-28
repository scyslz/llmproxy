import { useState, useEffect } from "react";
import { Provider, VirtualKey, SystemLog } from "./types";
import { motion, AnimatePresence } from "motion/react";
import Header from "./components/Header";
import ProviderCard from "./components/ProviderCard";
import KeyManager from "./components/KeyManager";
import Playground from "./components/Playground";
import TerminalLogs from "./components/TerminalLogs";
import Login from "./components/Login";
import SecuritySettings from "./components/SecuritySettings";
import { apiFetch, setAdminToken } from "./lib/api";
import { Cpu, Terminal, Shield, Sparkles, CheckCircle2 } from "lucide-react";

export default function App() {
  const [activeTab, setActiveTab] = useState<"providers" | "keys" | "playground" | "logs" | "settings">("providers");
  const [enableAdminAuth, setEnableAdminAuth] = useState(false);
  const [isAuthenticated, setIsAuthenticated] = useState<boolean | null>(null);
  
  // App state
  const [providers, setProviders] = useState<Provider[]>([]);
  const [virtualKeys, setVirtualKeys] = useState<VirtualKey[]>([]);
  const [enableVirtualKey, setEnableVirtualKey] = useState(false);
  const [debug, setDebug] = useState(true);
  const [logs, setLogs] = useState<SystemLog[]>([]);
  
  // Loading & Error states
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isPollingLogs, setIsPollingLogs] = useState(true);

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

      const [provRes, keyRes, setRes, logRes] = await Promise.all([
        apiFetch("/api/providers"),
        apiFetch("/api/keys"),
        apiFetch("/api/settings"),
        apiFetch("/api/logs")
      ]);

      if (!provRes.ok || !keyRes.ok || !setRes.ok || !logRes.ok) {
        throw new Error("Failed to sync some application settings from server.");
      }

      const providersData = await provRes.json();
      const keysData = await keyRes.json();
      const settingsData = await setRes.json();
      const logsData = await logRes.json();

      setProviders(providersData);
      setVirtualKeys(keysData);
      setEnableVirtualKey(settingsData.enableVirtualKey);
      setEnableAdminAuth(settingsData.enableAdminAuth || false);
      setDebug(settingsData.debug);
      setLogs(logsData);
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

  // Poll server logs
  useEffect(() => {
    if (!isPollingLogs || (enableAdminAuth && isAuthenticated === false)) return;

    const interval = setInterval(async () => {
      try {
        const res = await apiFetch("/api/logs");
        if (res.ok) {
          const logsData = await res.json();
          setLogs(logsData);
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
      const logRes = await apiFetch("/api/logs");
      if (logRes.ok) setLogs(await logRes.json());
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

  const handleCreateKey = async (name: string, providerIds: string[]) => {
    try {
      const res = await apiFetch("/api/keys", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, providerIds })
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
    } catch (e) {
      // ignore
    }
  };

  const handleRefreshLogs = async () => {
    try {
      const res = await apiFetch("/api/logs");
      if (res.ok) setLogs(await res.json());
    } catch (e) {
      // ignore
    }
  };

  const activeProviderName = providers.find((p) => p.enabled)?.name || "No Active Router";

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
                enableVirtualKey={enableVirtualKey}
                onToggleVirtualKey={handleToggleVirtualKey}
                onCreateKey={handleCreateKey}
                onDeleteKey={handleDeleteKey}
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
                  <Playground virtualKeys={virtualKeys} activeProviderName={activeProviderName} enableVirtualKey={enableVirtualKey} />
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
                      curl <span className="text-neutral-900 font-semibold">http://localhost:3000/v1/chat/completions</span> \
                    </div>
                    <div className="pl-2">
                      -H <span className="text-amber-700">"Authorization: Bearer {enableVirtualKey ? (virtualKeys[0]?.key || "YOUR_VIRTUAL_KEY") : "ANY_KEY"}"</span> \
                    </div>
                    <div className="pl-2">
                      -H <span className="text-amber-700">"Content-Type: application/json"</span> \
                    </div>
                    <div className="pl-2">-d '{"{"}</div>
                    <div className="pl-4">"model": "any-supported-model",</div>
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
                debug={debug}
                onToggleDebug={async (val) => {
                  await apiFetch("/api/settings", {
                    method: "PUT",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ debug: val })
                  });
                  setDebug(val);
                }}
                onLogout={handleLogout}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </main>
    </div>
  );
}
