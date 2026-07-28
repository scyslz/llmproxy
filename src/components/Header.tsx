import { Provider, VirtualKey } from "../types";
import { Cpu, ShieldCheck, ShieldAlert, Layers, Terminal, Play, Radio, Lock } from "lucide-react";

interface HeaderProps {
  providers: Provider[];
  virtualKeys: VirtualKey[];
  enableVirtualKey: boolean;
  enableAdminAuth?: boolean;
  activeTab: "providers" | "keys" | "playground" | "logs" | "settings";
  setActiveTab: (tab: "providers" | "keys" | "playground" | "logs" | "settings") => void;
}

export default function Header({
  providers,
  virtualKeys,
  enableVirtualKey,
  enableAdminAuth,
  activeTab,
  setActiveTab
}: HeaderProps) {
  const activeProvider = providers.find((p) => p.enabled);
  const activeKeysCount = virtualKeys.length;

  return (
    <header className="bg-white border-b border-neutral-200">
      {/* Brand Bar */}
      <div className="max-w-7xl mx-auto px-3.5 sm:px-6 py-4 flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center space-x-3">
          <div className="bg-neutral-100 border border-neutral-200 text-neutral-800 p-2.5 rounded-xl shadow-2xs">
            <Cpu className="w-6 h-6" />
          </div>
          <div>
            <h1 className="font-display font-bold text-neutral-800 text-lg tracking-tight">LLM Proxy Control Panel</h1>
            <p className="text-xs text-neutral-500 font-medium">High Performance OpenAI-Compatible Routing Engine</p>
          </div>
        </div>

        {/* Global Stats Badges */}
        <div className="flex flex-wrap items-center gap-3">
          {/* Active Provider Badge */}
          <div className="bg-neutral-50 border border-neutral-200 rounded-xl px-3.5 py-1.5 flex items-center space-x-2 text-xs">
            <Radio className="w-3.5 h-3.5 text-emerald-500" />
            <span className="text-neutral-500 font-medium">Active:</span>
            <span className="text-neutral-800 font-bold">
              {activeProvider ? activeProvider.name : "None Enabled"}
            </span>
          </div>

          {/* Admin Auth Badge */}
          {enableAdminAuth && (
            <div className="bg-neutral-50 border border-neutral-200 rounded-xl px-3.5 py-1.5 flex items-center space-x-1.5 text-xs text-neutral-700 font-semibold">
              <Lock className="w-3.5 h-3.5 text-emerald-600" />
              <span>Admin Protected</span>
            </div>
          )}

          {/* Virtual Key Gate Badge */}
          <div className="bg-neutral-50 border border-neutral-200 rounded-xl px-3.5 py-1.5 flex items-center space-x-2 text-xs text-neutral-700">
            {enableVirtualKey ? (
              <>
                <ShieldCheck className="w-3.5 h-3.5 text-emerald-600" />
                <span className="font-semibold">Security Gate: Strict</span>
              </>
            ) : (
              <>
                <ShieldAlert className="w-3.5 h-3.5 text-amber-600" />
                <span className="font-semibold">Security Gate: Open</span>
              </>
            )}
          </div>

          {/* Virtual Keys count */}
          <div className="bg-neutral-50 border border-neutral-200 rounded-xl px-3.5 py-1.5 flex items-center space-x-2 text-xs font-semibold text-neutral-700">
            <span>Keys: {activeKeysCount}</span>
          </div>
        </div>
      </div>

      {/* Navigation Tabs */}
      <div className="border-t border-neutral-100 bg-neutral-50/50">
        <div className="max-w-7xl mx-auto px-3.5 sm:px-6 flex space-x-1 overflow-x-auto no-scrollbar">
          {([
            { id: "providers", label: "Providers", icon: <Layers className="w-4 h-4" /> },
            { id: "keys", label: "Virtual Keys", icon: <ShieldCheck className="w-4 h-4" /> },
            { id: "playground", label: "Playground", icon: <Play className="w-4 h-4" /> },
            { id: "logs", label: "System Logs", icon: <Terminal className="w-4 h-4" /> },
            { id: "settings", label: "Security", icon: <Lock className="w-4 h-4" /> }
          ] as const).map((tab) => {
            const isSelected = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id as any)}
                className={`flex items-center space-x-2 px-5 py-3 text-xs font-semibold border-b-2 transition-all cursor-pointer ${
                  isSelected
                    ? "border-neutral-900 text-neutral-900 bg-white shadow-2xs font-bold"
                    : "border-transparent text-neutral-500 hover:text-neutral-800 hover:bg-neutral-100/50"
                }`}
              >
                {tab.icon}
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>
      </div>
    </header>
  );
}
