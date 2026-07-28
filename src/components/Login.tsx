import React, { useState } from "react";
import { Lock, KeyRound, Eye, EyeOff, AlertCircle, ShieldCheck, ArrowRight } from "lucide-react";
import { apiFetch, setAdminToken } from "../lib/api";

interface LoginProps {
  onLoginSuccess: (token: string) => void;
}

export default function Login({ onLoginSuccess }: LoginProps) {
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!password.trim()) {
      setError("Please enter the admin password");
      return;
    }
    try {
      setLoading(true);
      setError(null);
      const res = await apiFetch("/api/admin/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password })
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.error || "Invalid admin password");
      }
      setAdminToken(data.token);
      onLoginSuccess(data.token);
    } catch (err: any) {
      setError(err.message || "Failed to log in");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-neutral-50 text-neutral-800 flex flex-col justify-center items-center p-4 sm:p-6 font-sans antialiased selection:bg-neutral-900 selection:text-white">
      <div className="w-full max-w-md relative z-10 animate-fade-in">
        {/* Brand Header */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-white border border-neutral-200 text-neutral-800 shadow-xs mb-4">
            <ShieldCheck className="w-7 h-7" />
          </div>
          <h1 className="text-2xl sm:text-3xl font-display font-bold tracking-tight text-neutral-800">
            Admin Dashboard
          </h1>
          <p className="text-sm text-neutral-500 mt-1.5">
            LLM Proxy Gateway & Management Console
          </p>
        </div>

        {/* Login Card */}
        <div className="bg-white border border-neutral-200/80 rounded-2xl p-6 sm:p-8 shadow-sm">
          <div className="flex items-center space-x-2 text-xs font-semibold uppercase tracking-wider text-neutral-700 mb-6 bg-neutral-100 border border-neutral-200 px-3 py-1.5 rounded-lg w-fit">
            <Lock className="w-3.5 h-3.5 text-emerald-600" />
            <span>Protected Interface</span>
          </div>

          <form onSubmit={handleSubmit} className="space-y-5">
            <div>
              <label className="block text-xs font-semibold uppercase tracking-wider text-neutral-700 mb-2">
                Admin Password
              </label>
              <div className="relative">
                <div className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-neutral-400">
                  <KeyRound className="w-4 h-4" />
                </div>
                <input
                  type={showPassword ? "text" : "password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Enter management password..."
                  className="w-full bg-white border border-neutral-300 rounded-xl pl-10 pr-10 py-2.5 text-sm text-neutral-800 placeholder-neutral-400 focus:outline-none focus:border-neutral-800 focus:ring-1 focus:ring-neutral-800 transition-all shadow-inner"
                  autoFocus
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute inset-y-0 right-0 pr-3.5 flex items-center text-neutral-400 hover:text-neutral-700 transition-colors cursor-pointer"
                >
                  {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                </button>
              </div>
              <p className="text-[11px] text-neutral-500 mt-1.5">
                Default password is <code className="bg-neutral-100 border border-neutral-200 px-1.5 py-0.5 rounded text-neutral-700 font-mono">admin</code> unless configured otherwise.
              </p>
            </div>

            {error && (
              <div className="bg-red-50 border border-red-200 text-red-600 text-xs px-3.5 py-2.5 rounded-xl flex items-center space-x-2 animate-shake">
                <AlertCircle className="w-4 h-4 shrink-0" />
                <span className="font-medium">{error}</span>
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full bg-neutral-900 hover:bg-neutral-800 active:bg-neutral-950 text-white font-semibold py-2.5 px-4 rounded-xl shadow-xs flex items-center justify-center space-x-2 transition-all disabled:opacity-50 disabled:cursor-not-allowed text-sm cursor-pointer"
            >
              {loading ? (
                <span>Verifying...</span>
              ) : (
                <>
                  <span>Unlock Dashboard</span>
                  <ArrowRight className="w-4 h-4" />
                </>
              )}
            </button>
          </form>

          <div className="mt-6 pt-5 border-t border-neutral-200 text-center">
            <p className="text-xs text-neutral-500 leading-relaxed">
              <span className="font-semibold text-neutral-700">Architecture Note:</span> Proxy endpoints (<code className="text-neutral-800 font-mono bg-neutral-100 border border-neutral-200 px-1 py-0.5 rounded">/v1/*</code>) are governed by API Keys & Virtual Keys. This login only verifies access to the management console (<code className="text-neutral-800 font-mono bg-neutral-100 border border-neutral-200 px-1 py-0.5 rounded">/api/*</code>).
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
