import React, { useState } from "react";
import { VirtualKey, Provider } from "../types";
import { Key, Copy, Check, Trash2, Plus, ShieldCheck, ShieldAlert, ToggleLeft, ToggleRight } from "lucide-react";

interface KeyManagerProps {
  keys: VirtualKey[];
  providers: Provider[];
  enableVirtualKey: boolean;
  onToggleVirtualKey: (enabled: boolean) => void;
  onCreateKey: (name: string, providerIds: string[]) => void;
  onDeleteKey: (keyStr: string) => void;
}

export default function KeyManager({
  keys,
  providers,
  enableVirtualKey,
  onToggleVirtualKey,
  onCreateKey,
  onDeleteKey
}: KeyManagerProps) {
  const [name, setName] = useState("");
  const [selectedProviders, setSelectedProviders] = useState<string[]>([]);
  const [scopeMode, setScopeMode] = useState<"all" | "custom">("all");
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [isAdding, setIsAdding] = useState(false);
  const [confirmDeleteKey, setConfirmDeleteKey] = useState<string | null>(null);

  const handleCopy = (keyStr: string) => {
    navigator.clipboard.writeText(keyStr);
    setCopiedKey(keyStr);
    setTimeout(() => setCopiedKey(null), 2000);
  };

  const handleProviderToggle = (id: string) => {
    setSelectedProviders((prev) =>
      prev.includes(id) ? prev.filter((pId) => pId !== id) : [...prev, id]
    );
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    const finalProviders = scopeMode === "all" ? ["all"] : selectedProviders;
    onCreateKey(name.trim(), finalProviders);
    setName("");
    setSelectedProviders([]);
    setScopeMode("all");
    setIsAdding(false);
  };

  return (
    <div className="bg-white border border-neutral-200 rounded-2xl shadow-sm overflow-hidden">
      {/* Header section with Global Toggle */}
      <div className="bg-neutral-50 px-6 py-5 border-b border-neutral-150 flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center space-x-3">
          <div className={`p-2.5 rounded-xl ${enableVirtualKey ? "bg-emerald-50 text-emerald-600" : "bg-neutral-100 text-neutral-500"}`}>
            <Key className="w-5 h-5" />
          </div>
          <div>
            <h3 className="font-display font-semibold text-neutral-800 text-base">Virtual Keys</h3>
            <p className="text-xs text-neutral-500">Enable authentication to restrict proxy routing</p>
          </div>
        </div>

        {/* Global Key Gate Toggle */}
        <div className="flex items-center space-x-3 bg-white border border-neutral-200 rounded-xl px-4 py-2 text-sm">
          <span className="flex items-center space-x-1.5 font-medium text-neutral-700">
            {enableVirtualKey ? (
              <>
                <ShieldCheck className="w-4 h-4 text-emerald-500" />
                <span>Gate Enabled</span>
              </>
            ) : (
              <>
                <ShieldAlert className="w-4 h-4 text-neutral-400" />
                <span>Open Access</span>
              </>
            )}
          </span>
          <button
            onClick={() => onToggleVirtualKey(!enableVirtualKey)}
            className="focus:outline-none transition-transform active:scale-95 text-neutral-700"
          >
            {enableVirtualKey ? (
              <ToggleRight className="w-8 h-8 text-emerald-500" />
            ) : (
              <ToggleLeft className="w-8 h-8 text-neutral-400" />
            )}
          </button>
        </div>
      </div>

      <div className="p-6 space-y-6">
        {/* Toggle add key form */}
        {!isAdding ? (
          <button
            onClick={() => setIsAdding(true)}
            className="inline-flex items-center space-x-1.5 text-xs font-semibold bg-neutral-900 text-white hover:bg-neutral-800 px-3.5 py-2 sm:px-4 sm:py-2.5 rounded-xl shadow-2xs transition-all duration-150 shrink-0 whitespace-nowrap cursor-pointer"
          >
            <Plus className="w-4 h-4 shrink-0" />
            <span className="hidden sm:inline">New Key</span>
            <span className="sm:hidden">New</span>
          </button>
        ) : (
          <form onSubmit={handleSubmit} className="bg-neutral-50 border border-neutral-150 rounded-xl p-5 space-y-4">
            <h4 className="text-xs font-bold uppercase tracking-wider text-neutral-600">New Key</h4>
            <div className="space-y-1">
              <label className="text-xs font-medium text-neutral-500">Key Identifier Name</label>
              <input
                type="text"
                required
                placeholder="e.g. Frontend Web App, Client Test Key"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full bg-white border border-neutral-250 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400"
              />
            </div>

            <div className="space-y-3">
              <label className="text-xs font-medium text-neutral-500 block">Allowed Providers (Scope)</label>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                <label
                  onClick={() => setScopeMode("all")}
                  className={`flex items-start space-x-2.5 border rounded-xl p-3 cursor-pointer select-none transition-all ${
                    scopeMode === "all"
                      ? "bg-neutral-900 border-neutral-900 text-white shadow-2xs"
                      : "bg-white border-neutral-200 text-neutral-700 hover:border-neutral-300"
                  }`}
                >
                  <input
                    type="radio"
                    name="scopeMode"
                    checked={scopeMode === "all"}
                    onChange={() => setScopeMode("all")}
                    className="mt-0.5 text-emerald-500 focus:ring-emerald-500"
                  />
                  <div className="flex-1">
                    <p className="text-xs font-bold">All Providers (Universal Access)</p>
                    <p className={`text-[10px] leading-relaxed mt-0.5 ${scopeMode === "all" ? "text-neutral-300" : "text-neutral-500"}`}>
                      Supports ALL models. Future provider additions or changes take effect automatically.
                    </p>
                  </div>
                </label>
                <label
                  onClick={() => setScopeMode("custom")}
                  className={`flex items-start space-x-2.5 border rounded-xl p-3 cursor-pointer select-none transition-all ${
                    scopeMode === "custom"
                      ? "bg-neutral-900 border-neutral-900 text-white shadow-2xs"
                      : "bg-white border-neutral-200 text-neutral-700 hover:border-neutral-300"
                  }`}
                >
                  <input
                    type="radio"
                    name="scopeMode"
                    checked={scopeMode === "custom"}
                    onChange={() => setScopeMode("custom")}
                    className="mt-0.5 text-emerald-500 focus:ring-emerald-500"
                  />
                  <div className="flex-1">
                    <p className="text-xs font-bold">Specific Providers (Custom Scope)</p>
                    <p className={`text-[10px] leading-relaxed mt-0.5 ${scopeMode === "custom" ? "text-neutral-300" : "text-neutral-500"}`}>
                      Restrict key access to selected providers only.
                    </p>
                  </div>
                </label>
              </div>

              {scopeMode === "custom" && (
                <div className="mt-2.5 pt-2.5 border-t border-neutral-200">
                  <p className="text-[11px] text-neutral-500 font-medium mb-2">Select allowed providers:</p>
                  <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2">
                    {providers.map((p) => (
                      <label
                        key={p.id}
                        className={`flex items-center space-x-2 border rounded-xl p-3 cursor-pointer select-none transition-all ${
                          selectedProviders.includes(p.id)
                            ? "bg-neutral-900 border-neutral-900 text-white shadow-2xs"
                            : "bg-white border-neutral-200 text-neutral-700 hover:border-neutral-300"
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={selectedProviders.includes(p.id)}
                          onChange={() => handleProviderToggle(p.id)}
                          className="hidden"
                        />
                        <div className="flex-1">
                          <p className="text-xs font-semibold">{p.name}</p>
                          <p className={`text-[10px] ${selectedProviders.includes(p.id) ? "text-neutral-300" : "text-neutral-500"}`}>
                            {p.models.length} models
                          </p>
                        </div>
                      </label>
                    ))}
                  </div>
                </div>
              )}
            </div>

            <div className="flex space-x-2 pt-2">
              <button
                type="submit"
                className="bg-neutral-900 text-white hover:bg-neutral-800 px-4 py-2 rounded-xl text-xs font-semibold transition-colors shadow-2xs cursor-pointer"
              >
                Save Key
              </button>
              <button
                type="button"
                onClick={() => {
                  setIsAdding(false);
                  setName("");
                  setSelectedProviders([]);
                }}
                className="bg-white border border-neutral-200 text-neutral-600 hover:bg-neutral-50 px-4 py-2 rounded-xl text-xs font-medium transition-colors"
              >
                Cancel
              </button>
            </div>
          </form>
        )}

        {/* Keys list */}
        <div className="overflow-x-auto">
          {keys.length === 0 ? (
            <div className="text-center py-8 text-neutral-400 text-xs">
              No virtual keys configured. Generate one above to restrict proxy routing.
            </div>
          ) : (
            <table className="w-full text-left border-collapse min-w-[650px]">
              <thead>
                <tr className="border-b border-neutral-150 text-[10px] uppercase font-bold tracking-wider text-neutral-400">
                  <th className="pb-3 pl-1">Key Name</th>
                  <th className="pb-3">Virtual API Key</th>
                  <th className="pb-3">Allowed Providers</th>
                  <th className="pb-3">Created At</th>
                  <th className="pb-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-neutral-100 text-xs text-neutral-700">
                {keys.map((k) => (
                  <tr key={k.key} className="hover:bg-neutral-50/50 transition-colors">
                    <td className="py-4 pl-1 font-semibold text-neutral-800">{k.name}</td>
                    <td className="py-4 font-mono text-neutral-600">
                      <div className="flex items-center space-x-1.5 bg-neutral-50 border border-neutral-150 rounded px-2 py-0.5 w-fit">
                        <span>{k.key.substring(0, 16)}...</span>
                        <button
                          onClick={() => handleCopy(k.key)}
                          className="text-neutral-400 hover:text-neutral-700 p-0.5 transition-colors"
                          title="Copy full key"
                        >
                          {copiedKey === k.key ? (
                            <Check className="w-3 h-3 text-emerald-500" />
                          ) : (
                            <Copy className="w-3 h-3" />
                          )}
                        </button>
                      </div>
                    </td>
                    <td className="py-4">
                      {k.providerIds.length === 0 || k.providerIds.includes("all") || k.providerIds.includes("*") ? (
                        <span className="bg-emerald-100 text-emerald-800 border border-emerald-200 px-2.5 py-0.5 rounded-full text-[10px] font-bold inline-flex items-center space-x-1">
                          <span>ALL PROVIDERS (Universal)</span>
                        </span>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {k.providerIds.map((pId) => {
                            const pName = providers.find((p) => p.id === pId)?.name || pId;
                            return (
                              <span key={pId} className="bg-neutral-800 text-white px-2 py-0.5 rounded-full text-[10px] font-semibold">
                                {pName}
                              </span>
                            );
                          })}
                        </div>
                      )}
                    </td>
                    <td className="py-4 text-neutral-400">
                      {new Date(k.createdAt).toLocaleDateString()} {new Date(k.createdAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
                    </td>
                    <td className="py-4 text-right">
                      {confirmDeleteKey === k.key ? (
                        <div className="inline-flex items-center space-x-1">
                          <button
                            onClick={() => { onDeleteKey(k.key); setConfirmDeleteKey(null); }}
                            className="bg-red-600 hover:bg-red-500 text-white px-2 py-1 rounded text-[11px] font-semibold transition-colors"
                          >
                            Confirm
                          </button>
                          <button
                            onClick={() => setConfirmDeleteKey(null)}
                            className="bg-neutral-100 hover:bg-neutral-200 text-neutral-600 px-2 py-1 rounded text-[11px] font-medium transition-colors"
                          >
                            Cancel
                          </button>
                        </div>
                      ) : (
                        <button
                          onClick={() => setConfirmDeleteKey(k.key)}
                          className="p-1.5 hover:bg-red-50 text-neutral-400 hover:text-red-500 rounded-lg transition-colors inline-flex"
                          title="Delete Key"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
