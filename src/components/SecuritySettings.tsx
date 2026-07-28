import React, { useState, useEffect } from "react";
import { Lock, ShieldCheck, ShieldAlert, KeyRound, Check, AlertTriangle, LogOut, Terminal, Save, Eye, EyeOff, FileText, Trash2, HardDrive } from "lucide-react";
import { apiFetch } from "../lib/api";

interface SecuritySettingsProps {
  enableVirtualKey: boolean;
  onToggleVirtualKey: (enabled: boolean) => void;
  enableAdminAuth: boolean;
  onUpdateAdminAuth: (enabled: boolean, newPassword?: string) => Promise<void>;
  debug: boolean;
  onToggleDebug: (enabled: boolean) => void;
  onLogout: () => void;
}

interface LogStatus {
  activeFile: number;
  file1Size: number;
  file2Size: number;
  maxLogSizeMB: number;
  totalLogs: number;
}

export default function SecuritySettings({
  enableVirtualKey,
  onToggleVirtualKey,
  enableAdminAuth,
  onUpdateAdminAuth,
  debug,
  onToggleDebug,
  onLogout
}: SecuritySettingsProps) {
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [loading, setLoading] = useState(false);
  const [successMsg, setSuccessMsg] = useState<string | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [logStatus, setLogStatus] = useState<LogStatus | null>(null);
  const [selectedMaxSize, setSelectedMaxSize] = useState<number>(2);
  const [logActionLoading, setLogActionLoading] = useState(false);
  const [logRequestBody, setLogRequestBody] = useState<boolean>(true);
  const [logResponseBody, setLogResponseBody] = useState<boolean>(false);
  const [customSizeInput, setCustomSizeInput] = useState<string>("");

  const fetchLogStatusAndSettings = async () => {
    try {
      const [statusRes, setRes] = await Promise.all([
        apiFetch("/api/logs/status"),
        apiFetch("/api/settings")
      ]);
      const statusData = await statusRes.json();
      setLogStatus(statusData);
      setSelectedMaxSize(statusData.maxLogSizeMB || 2);
      if (setRes.ok) {
        const setData = await setRes.json();
        setLogRequestBody(setData.logRequestBody !== undefined ? setData.logRequestBody : true);
        setLogResponseBody(setData.logResponseBody === true);
      }
    } catch {}
  };

  useEffect(() => {
    fetchLogStatusAndSettings();
  }, []);

  const handleToggleLogBody = async (field: "logRequestBody" | "logResponseBody", val: boolean) => {
    try {
      setLogActionLoading(true);
      if (field === "logRequestBody") setLogRequestBody(val);
      if (field === "logResponseBody") setLogResponseBody(val);
      await apiFetch("/api/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ [field]: val })
      });
      setSuccessMsg(`Updated ${field === "logRequestBody" ? "Request Body Logging" : "Response Body Logging"} setting.`);
    } catch (err: any) {
      setErrorMsg(err.message || "Failed to update logging setting");
    } finally {
      setLogActionLoading(false);
    }
  };

  const handleSaveLogSize = async (size: number) => {
    try {
      setLogActionLoading(true);
      await apiFetch("/api/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ maxLogSizeMB: size })
      });
      setSelectedMaxSize(size);
      setSuccessMsg(`Max log file size updated to ${size} MB.`);
      await fetchLogStatusAndSettings();
    } catch (err: any) {
      setErrorMsg(err.message || "Failed to update max log size");
    } finally {
      setLogActionLoading(false);
    }
  };

  const handleClearLogs = async () => {
    if (!window.confirm("Are you sure you want to clear all disk and memory log files?")) return;
    try {
      setLogActionLoading(true);
      await apiFetch("/api/logs/clear", { method: "POST" });
      setSuccessMsg("All system and proxy logs have been cleared.");
      await fetchLogStatus();
    } catch (err: any) {
      setErrorMsg(err.message || "Failed to clear logs");
    } finally {
      setLogActionLoading(false);
    }
  };

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  };

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPassword.trim()) {
      setErrorMsg("Password cannot be empty");
      return;
    }
    if (newPassword !== confirmPassword) {
      setErrorMsg("Passwords do not match");
      return;
    }
    try {
      setLoading(true);
      setErrorMsg(null);
      setSuccessMsg(null);
      await onUpdateAdminAuth(enableAdminAuth, newPassword.trim());
      setSuccessMsg("Admin password updated successfully. Please use the new password on next login.");
      setNewPassword("");
      setConfirmPassword("");
    } catch (err: any) {
      setErrorMsg(err.message || "Failed to update password");
    } finally {
      setLoading(false);
    }
  };

  const handleToggleAdminAuth = async (enabled: boolean) => {
    try {
      setErrorMsg(null);
      setSuccessMsg(null);
      await onUpdateAdminAuth(enabled);
      setSuccessMsg(`Admin dashboard authentication ${enabled ? "enabled" : "disabled"}.`);
    } catch (err: any) {
      setErrorMsg(err.message || "Failed to toggle admin authentication");
    }
  };

  return (
    <div className="space-y-6 animate-fade-in">
      {/* Page Header */}
      <div className="bg-white border border-neutral-200 rounded-2xl p-6 shadow-xs flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div className="flex items-center space-x-3.5">
          <div className="p-3 bg-neutral-100 border border-neutral-200 text-neutral-800 rounded-xl">
            <Lock className="w-6 h-6" />
          </div>
          <div>
            <h2 className="text-lg font-display font-bold text-neutral-800">Security & Access Control</h2>
            <p className="text-xs text-neutral-500">Configure management console login and API proxy authentication gates</p>
          </div>
        </div>
        
        {enableAdminAuth && (
          <button
            onClick={onLogout}
            className="inline-flex items-center space-x-2 bg-red-50 hover:bg-red-100 text-red-600 border border-red-200 px-4 py-2 rounded-xl text-xs font-semibold transition-all self-start sm:self-auto cursor-pointer"
          >
            <LogOut className="w-3.5 h-3.5" />
            <span>Lock & Log Out</span>
          </button>
        )}
      </div>

      {successMsg && (
        <div className="bg-emerald-50 border border-emerald-200 text-emerald-800 text-xs px-4 py-3 rounded-xl flex items-center justify-between shadow-xs">
          <div className="flex items-center space-x-2">
            <Check className="w-4 h-4 text-emerald-600 shrink-0" />
            <span className="font-medium">{successMsg}</span>
          </div>
          <button onClick={() => setSuccessMsg(null)} className="text-emerald-600 hover:text-emerald-800 text-xs font-bold px-1 cursor-pointer">✕</button>
        </div>
      )}

      {errorMsg && (
        <div className="bg-red-50 border border-red-200 text-red-800 text-xs px-4 py-3 rounded-xl flex items-center justify-between shadow-xs">
          <div className="flex items-center space-x-2">
            <AlertTriangle className="w-4 h-4 text-red-600 shrink-0" />
            <span className="font-medium">{errorMsg}</span>
          </div>
          <button onClick={() => setErrorMsg(null)} className="text-red-600 hover:text-red-800 text-xs font-bold px-1 cursor-pointer">✕</button>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {/* Section 1: Management Console Auth */}
        <div className="bg-white border border-neutral-200 rounded-2xl p-6 shadow-xs flex flex-col justify-between space-y-6">
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-2.5">
                <ShieldCheck className="w-5 h-5 text-neutral-800" />
                <h3 className="font-display font-semibold text-neutral-800 text-base">Dashboard Admin Auth</h3>
              </div>
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={enableAdminAuth}
                  onChange={(e) => handleToggleAdminAuth(e.target.checked)}
                  className="sr-only peer"
                />
                <div className="w-11 h-6 bg-neutral-250 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-neutral-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-neutral-900"></div>
              </label>
            </div>
            
            <p className="text-xs text-neutral-600 leading-relaxed">
              When enabled, accessing management APIs (<code className="text-neutral-800 font-mono bg-neutral-100 border border-neutral-200 px-1 py-0.5 rounded">/api/providers</code>, <code className="text-neutral-800 font-mono bg-neutral-100 border border-neutral-200 px-1 py-0.5 rounded">/api/keys</code>, <code className="text-neutral-800 font-mono bg-neutral-100 border border-neutral-200 px-1 py-0.5 rounded">/api/settings</code>) requires logging in with an admin password.
            </p>

            <div className="p-3.5 bg-neutral-50 border border-neutral-200 rounded-xl text-xs space-y-1.5">
              <div className="flex items-center justify-between text-neutral-700 font-medium">
                <span>Current Status:</span>
                <span className={enableAdminAuth ? "text-emerald-700 font-bold" : "text-amber-700 font-bold"}>
                  {enableAdminAuth ? "Protected (Password Required)" : "Open (No Password Required)"}
                </span>
              </div>
              <p className="text-[11px] text-neutral-500">
                Note: This does not affect LLM proxy requests to <code className="text-neutral-700 font-mono">/v1/chat/completions</code> or <code className="text-neutral-700 font-mono">/v1/models</code>.
              </p>
            </div>
          </div>

          {/* Change Password Form */}
          <form onSubmit={handlePasswordSubmit} className="pt-4 border-t border-neutral-200 space-y-3">
            <h4 className="text-xs font-bold uppercase tracking-wider text-neutral-600 flex items-center space-x-1.5">
              <KeyRound className="w-3.5 h-3.5 text-neutral-800" />
              <span>Update Admin Password</span>
            </h4>
            
            <div className="space-y-2.5">
              <div className="relative">
                <input
                  type={showPassword ? "text" : "password"}
                  placeholder="New password..."
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  className="w-full bg-white border border-neutral-300 rounded-xl pl-3 pr-10 py-2 text-xs text-neutral-800 placeholder-neutral-400 focus:outline-none focus:border-neutral-800 focus:ring-1 focus:ring-neutral-800 transition-colors"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute inset-y-0 right-0 pr-3 flex items-center text-neutral-400 hover:text-neutral-700 cursor-pointer"
                >
                  {showPassword ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
                </button>
              </div>
              <input
                type={showPassword ? "text" : "password"}
                placeholder="Confirm new password..."
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                className="w-full bg-white border border-neutral-300 rounded-xl px-3 py-2 text-xs text-neutral-800 placeholder-neutral-400 focus:outline-none focus:border-neutral-800 focus:ring-1 focus:ring-neutral-800 transition-colors"
              />
            </div>

            <button
              type="submit"
              disabled={loading || !newPassword}
              className="w-full bg-neutral-900 hover:bg-neutral-800 active:bg-neutral-950 disabled:opacity-40 text-white font-semibold py-2.5 px-3 rounded-xl text-xs flex items-center justify-center space-x-1.5 transition-all shadow-xs cursor-pointer"
            >
              <Save className="w-3.5 h-3.5" />
              <span>{loading ? "Saving..." : "Save Password"}</span>
            </button>
          </form>
        </div>

        {/* Section 2: API Proxy Security & Debug Mode */}
        <div className="space-y-6 flex flex-col justify-between">
          {/* Virtual Key Proxy Gate */}
          <div className="bg-white border border-neutral-200 rounded-2xl p-6 shadow-xs space-y-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-2.5">
                <KeyRound className="w-5 h-5 text-neutral-800" />
                <h3 className="font-display font-semibold text-neutral-800 text-base">API Proxy Gate (Virtual Keys)</h3>
              </div>
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={enableVirtualKey}
                  onChange={(e) => onToggleVirtualKey(e.target.checked)}
                  className="sr-only peer"
                />
                <div className="w-11 h-6 bg-neutral-250 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-neutral-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-neutral-900"></div>
              </label>
            </div>

            <p className="text-xs text-neutral-600 leading-relaxed">
              Governs authentication for AI generation endpoints (<code className="text-neutral-800 font-mono bg-neutral-100 border border-neutral-200 px-1 py-0.5 rounded">/v1/chat/completions</code>). When enabled, clients must provide a valid <code className="text-neutral-700 font-mono">sk-proxy-...</code> Virtual Key in their Authorization header.
            </p>

            <div className="p-3.5 bg-neutral-50 border border-neutral-200 rounded-xl text-xs flex items-center justify-between">
              <span className="text-neutral-700 font-medium">Proxy Mode:</span>
              <span className={enableVirtualKey ? "text-emerald-700 font-bold flex items-center space-x-1" : "text-amber-700 font-bold flex items-center space-x-1"}>
                {enableVirtualKey ? (
                  <>
                    <ShieldCheck className="w-3.5 h-3.5" />
                    <span>Strict (Virtual Key Required)</span>
                  </>
                ) : (
                  <>
                    <ShieldAlert className="w-3.5 h-3.5" />
                    <span>Direct Pass-Through (Open)</span>
                  </>
                )}
              </span>
            </div>
          </div>

          {/* System Debug Mode */}
          <div className="bg-white border border-neutral-200 rounded-2xl p-6 shadow-xs space-y-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-2.5">
                <Terminal className="w-5 h-5 text-neutral-800" />
                <h3 className="font-display font-semibold text-neutral-800 text-base">Verbose Debug Logging</h3>
              </div>
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={debug}
                  onChange={(e) => onToggleDebug(e.target.checked)}
                  className="sr-only peer"
                />
                <div className="w-11 h-6 bg-neutral-250 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-neutral-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-neutral-900"></div>
              </label>
            </div>

            <p className="text-xs text-neutral-600 leading-relaxed">
              When enabled, detailed system and proxy request diagnostics are recorded and displayed in the Live Dashboard Console below.
            </p>

            <div className="pt-3 mt-3 border-t border-neutral-100 grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="flex items-center justify-between bg-neutral-50 p-3 rounded-xl border border-neutral-200/80">
                <div className="space-y-0.5">
                  <div className="text-xs font-semibold text-neutral-800">Log Request Body</div>
                  <div className="text-[11px] text-neutral-500">Record incoming JSON payloads</div>
                </div>
                <label className="relative inline-flex items-center cursor-pointer select-none">
                  <input
                    type="checkbox"
                    className="sr-only peer"
                    checked={logRequestBody}
                    onChange={(e) => handleToggleLogBody("logRequestBody", e.target.checked)}
                    disabled={logActionLoading}
                  />
                  <div className="w-9 h-5 bg-neutral-250 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-neutral-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-neutral-900"></div>
                </label>
              </div>
              <div className="flex items-center justify-between bg-neutral-50 p-3 rounded-xl border border-neutral-200/80">
                <div className="space-y-0.5">
                  <div className="text-xs font-semibold text-neutral-800">Log Response Body</div>
                  <div className="text-[11px] text-neutral-500">Record downstream output text</div>
                </div>
                <label className="relative inline-flex items-center cursor-pointer select-none">
                  <input
                    type="checkbox"
                    className="sr-only peer"
                    checked={logResponseBody}
                    onChange={(e) => handleToggleLogBody("logResponseBody", e.target.checked)}
                    disabled={logActionLoading}
                  />
                  <div className="w-9 h-5 bg-neutral-250 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-neutral-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-neutral-900"></div>
                </label>
              </div>
            </div>
          </div>

          {/* Log Rotation & Size Limit Card */}
          <div className="bg-white border border-neutral-200 rounded-2xl p-6 shadow-xs space-y-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center space-x-2.5">
                <HardDrive className="w-5 h-5 text-neutral-800" />
                <h3 className="font-display font-semibold text-neutral-800 text-base">Log File Size Limit & Automatic Round-Robin</h3>
              </div>
            </div>

            <p className="text-xs text-neutral-600 leading-relaxed">
              Configures the max file size limit per log file. Uses an automatic round-robin storage mechanism in the background. When a file reaches the limit, writing automatically switches over to prevent unlimited disk growth.
            </p>

            {logStatus && (
              <div className="p-3.5 bg-neutral-50 border border-neutral-200 rounded-xl text-xs flex items-center justify-between">
                <div className="text-neutral-600 font-medium">
                  <span>Current Disk Log Storage Usage:</span>
                </div>
                <div className="font-mono font-bold text-neutral-800 text-sm">
                  {formatSize(logStatus.file1Size + logStatus.file2Size)} <span className="text-neutral-400 font-normal text-xs">/ {selectedMaxSize * 2} MB Max Capacity</span>
                </div>
              </div>
            )}

            <div className="space-y-2 pt-1">
              <label className="text-xs font-semibold text-neutral-700 block">Max Size Per File Limit:</label>
              <div className="flex flex-wrap items-center gap-2">
                {[1, 2, 5, 10, 20, 50, 100].map((size) => (
                  <button
                    key={size}
                    type="button"
                    onClick={() => {
                      handleSaveLogSize(size);
                      setCustomSizeInput("");
                    }}
                    disabled={logActionLoading || selectedMaxSize === size}
                    className={`px-3 py-1.5 rounded-xl text-xs font-semibold transition-all cursor-pointer ${
                      selectedMaxSize === size
                        ? "bg-neutral-900 text-white shadow-xs"
                        : "bg-neutral-100 hover:bg-neutral-200 text-neutral-700 border border-neutral-200"
                    }`}
                  >
                    {size} MB
                  </button>
                ))}
                <div className="flex items-center space-x-1.5 ml-1">
                  <input
                    type="number"
                    min="1"
                    max="10000"
                    placeholder="Custom MB"
                    value={customSizeInput}
                    onChange={(e) => setCustomSizeInput(e.target.value)}
                    className="w-24 px-2.5 py-1.5 bg-white border border-neutral-300 rounded-xl text-xs text-neutral-800 outline-none focus:border-neutral-500 shadow-2xs font-mono"
                  />
                  <button
                    type="button"
                    disabled={!customSizeInput || isNaN(Number(customSizeInput)) || Number(customSizeInput) <= 0 || logActionLoading}
                    onClick={() => {
                      const num = Number(customSizeInput);
                      if (num > 0) handleSaveLogSize(num);
                    }}
                    className="px-3 py-1.5 bg-neutral-800 hover:bg-neutral-900 text-white rounded-xl text-xs font-semibold disabled:opacity-40 transition-all cursor-pointer"
                  >
                    Apply
                  </button>
                </div>
              </div>
            </div>

            <div className="flex items-center justify-end space-x-3 pt-2 border-t border-neutral-100">
              <button
                type="button"
                onClick={handleClearLogs}
                disabled={logActionLoading}
                className="px-3 py-1.5 bg-red-50 hover:bg-red-100 text-red-600 border border-red-200 rounded-xl text-xs font-semibold flex items-center space-x-1.5 transition-all cursor-pointer"
              >
                <Trash2 className="w-3.5 h-3.5" />
                <span>Clear Disk Logs</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
