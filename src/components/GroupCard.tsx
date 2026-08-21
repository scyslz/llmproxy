import React, { useState } from "react";
import { ProviderGroup, Provider, GroupEntry, GroupTestEntry } from "../types";
import { Layers, Plus, Trash2, Edit2, CheckCircle2, XCircle, Loader2, ChevronUp, ChevronDown, ChevronRight, Minus, X } from "lucide-react";

interface GroupCardProps {
  groups: ProviderGroup[];
  providers: Provider[];
  onCreate: (group: Omit<ProviderGroup, "createdAt">) => Promise<void>;
  onUpdate: (id: string, patch: Partial<ProviderGroup>) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  onTest: (id: string) => Promise<void>;
  testResults: GroupTestEntry[] | null;
  isTesting: boolean;
}

export default function GroupCard({
  groups,
  providers,
  onCreate,
  onUpdate,
  onDelete,
  onTest,
  testResults,
  isTesting
}: GroupCardProps) {
  const [isEditing, setIsEditing] = useState<string | null>(null);
  const [entries, setEntries] = useState<GroupEntry[]>([]);
  const [testResultMap, setTestResultMap] = useState<Record<string, GroupTestEntry[]>>({});
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const startEdit = (g?: ProviderGroup) => {
    if (g) {
      setEntries((g.entries || []).map(e => ({ ...e })));
    } else {
      setEntries([]);
    }
    setIsEditing(g ? g.id : "new");
  };

  const save = async () => {
    if (!entries.length) return;
    const id = isEditing === "new" ? "g_" + Date.now().toString(36) : isEditing!;
    const nameInput = document.getElementById("group-name-input") as HTMLInputElement;
    const name = nameInput?.value.trim() || (isEditing === "new" ? "New Group" : "");
    if (isEditing === "new") {
      await onCreate({ id, name, entries });
    } else {
      await onUpdate(id, { name, entries });
    }
    setIsEditing(null);
  };

  const addNewEntry = (pid: string) => {
    setEntries(prev => [...prev, { providerId: pid, models: [] }]);
  };

  const removeEntry = (idx: number) => {
    setEntries(prev => prev.filter((_, i) => i !== idx));
  };

  const moveEntry = (idx: number, dir: -1 | 1) => {
    setEntries(prev => {
      const next = [...prev];
      const target = idx + dir;
      if (target < 0 || target >= next.length) return prev;
      [next[idx], next[target]] = [next[target], next[idx]];
      return next;
    });
  };

  const toggleModel = (entryIdx: number, model: string) => {
    setEntries(prev => prev.map((e, i) => {
      if (i !== entryIdx) return e;
      const has = e.models?.includes(model);
      return { ...e, models: has ? (e.models || []).filter(m => m !== model) : [...(e.models || []), model] };
    }));
  };

  const handleTest = async (id: string) => {
    await onTest(id);
    try {
      const res = await fetch(`/api/groups/${id}/test`);
      if (res.ok) {
        const data = await res.json();
        setTestResultMap(prev => ({ ...prev, [id]: data }));
      }
    } catch {}
  };

  const getTestEntries = (groupId: string) => testResultMap[groupId] || [];

  const existingPids = new Set(entries.map(e => e.providerId));
  const availableForNew = providers.filter(p => !existingPids.has(p.id));

  const modalClass = "fixed inset-0 z-50 flex items-center justify-center p-4 bg-neutral-900/20 backdrop-blur-sm";
  const cardClass = "bg-white border border-neutral-200 rounded-2xl shadow-sm overflow-hidden";
  const inputClass = "w-full bg-white border border-neutral-250 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400";
  const btnPrimary = "inline-flex items-center space-x-1.5 text-xs font-semibold bg-neutral-900 text-white hover:bg-neutral-800 px-3.5 py-2 rounded-xl shadow-2xs transition-colors cursor-pointer";
  const btnSecondary = "bg-white border border-neutral-200 text-neutral-600 hover:bg-neutral-50 px-4 py-2 rounded-xl text-xs font-medium transition-colors cursor-pointer";
  const btnIcon = "p-2 text-neutral-400 hover:text-neutral-700 hover:bg-neutral-100 rounded-lg transition-colors cursor-pointer";

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex justify-between items-center gap-3">
        <div className="min-w-0 pr-2">
          <h3 className="font-display font-semibold text-neutral-800 text-base truncate">Provider Groups</h3>
          <p className="text-xs text-neutral-500 truncate sm:whitespace-normal">Ordered provider/model fallback chains</p>
        </div>
        <button onClick={() => startEdit()} className={btnPrimary}>
          <Plus className="w-4 h-4 shrink-0" />
          <span className="hidden sm:inline">New Group</span>
          <span className="sm:hidden">New</span>
        </button>
      </div>

      {/* Edit/Create Modal */}
      {isEditing && (
        <div className={modalClass}>
          <div className={`${cardClass} max-w-3xl w-full max-h-[90vh] flex flex-col overflow-hidden`}>
            <div className="flex justify-between items-center px-6 py-4 bg-neutral-50 border-b border-neutral-200 shrink-0">
              <h4 className="font-display font-semibold text-neutral-800 text-sm">{isEditing === "new" ? "New Group" : "Edit Group"}</h4>
              <button onClick={() => setIsEditing(null)} className="p-1 text-neutral-400 hover:text-neutral-700 rounded-lg hover:bg-neutral-100 cursor-pointer">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-6 space-y-4 overflow-y-auto flex-1">
              {/* Basic info */}
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1">
                  <label className="text-xs font-medium text-neutral-500">Group ID</label>
                  <input id="group-id-input" className={inputClass} defaultValue={isEditing === "new" ? "" : isEditing}
                    disabled={isEditing !== "new"} placeholder="g_primary" />
                </div>
                <div className="space-y-1">
                  <label className="text-xs font-medium text-neutral-500">Name</label>
                  <input id="group-name-input" className={inputClass} defaultValue={isEditing !== "new" ? groups.find(g => g.id === isEditing)?.name || "" : ""}
                    placeholder="Primary Chain" />
                </div>
              </div>

              {/* Existing entries chain */}
              <div className="space-y-2">
                <label className="text-xs font-medium text-neutral-500 block">
                  Chain entries (ordered by priority)
                </label>
                {entries.length === 0 && (
                  <p className="text-xs text-neutral-400 italic py-2 text-center border border-dashed border-neutral-200 rounded-xl">
                    No entries yet. Add entries below to build your chain.
                  </p>
                )}
                {entries.map((e, idx) => {
                  const p = providers.find(x => x.id === e.providerId);
                  return (
                    <div key={idx} className={`border rounded-xl overflow-hidden ${p ? "border-neutral-200" : "border-red-200"}`}>
                      <div className={`flex items-center gap-2 px-3 py-2 ${p ? "bg-neutral-50" : "bg-red-50"}`}>
                        <span className={`w-2 h-2 rounded-full shrink-0 ${p ? "bg-emerald-400" : "bg-red-400"}`} />
                        <span className="text-[10px] text-neutral-400 font-mono w-4 text-center">{idx + 1}</span>
                        {p ? (
                          <>
                            <span className="text-xs font-semibold text-neutral-800">{p.name}</span>
                            <span className="text-[10px] font-mono text-neutral-400">{e.providerId}</span>
                            {e.models?.length > 0 ? (
                              <span className="text-[10px] font-mono text-violet-600 bg-violet-50 px-1.5 py-0.5 rounded truncate max-w-[200px]" title={e.models.join(", ")}>
                                {e.models.join(", ")}
                              </span>
                            ) : (
                              <span className="text-[10px] text-neutral-400">all models</span>
                            )}
                          </>
                        ) : (
                          <span className="text-xs text-red-500 font-mono">{e.providerId} (not found)</span>
                        )}
                        <div className="flex items-center gap-1 ml-auto shrink-0">
                          <button onClick={() => moveEntry(idx, -1)} disabled={idx === 0}
                            className="p-1 hover:bg-neutral-200 rounded disabled:opacity-20 cursor-pointer">
                            <ChevronUp className="w-3 h-3" />
                          </button>
                          <button onClick={() => moveEntry(idx, 1)} disabled={idx === entries.length - 1}
                            className="p-1 hover:bg-neutral-200 rounded disabled:opacity-20 cursor-pointer">
                            <ChevronDown className="w-3 h-3" />
                          </button>
                          <button onClick={() => removeEntry(idx)} className="p-1 text-red-400 hover:bg-red-50 rounded cursor-pointer">
                            <Minus className="w-3 h-3" />
                          </button>
                        </div>
                      </div>
                      {p && p.models.length > 0 && (
                        <div className="border-t border-neutral-150 px-3 py-2 bg-white">
                          <p className="text-[10px] text-neutral-500 font-medium mb-2">Select models:</p>
                          <div className="flex flex-wrap gap-1.5">
                            <button type="button" onClick={() => toggleModel(idx, "")}
                              className={`px-2 py-1 rounded-lg text-[10px] border transition-colors cursor-pointer ${
                                (!e.models || e.models.length === 0)
                                  ? "bg-violet-600 text-white border-violet-600"
                                  : "bg-neutral-50 text-neutral-600 border-neutral-200 hover:border-neutral-400"
                              }`}>
                              All models
                            </button>
                            {p.models.map(m => {
                              const sel = e.models?.includes(m);
                              return (
                                <button key={m} type="button" onClick={() => toggleModel(idx, m)}
                                  className={`px-2 py-1 rounded-lg text-[10px] font-mono border transition-colors cursor-pointer ${
                                    sel
                                      ? "bg-violet-600 text-white border-violet-600"
                                      : "bg-neutral-50 text-neutral-600 border-neutral-200 hover:border-neutral-400"
                                  }`}>
                                  {m}
                                </button>
                              );
                            })}
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>

              {/* Add new entries section */}
              <div className="space-y-2">
                <label className="text-xs font-medium text-neutral-500 block">Add providers to chain:</label>

                {availableForNew.length === 0 ? (
                  <p className="text-xs text-neutral-400 italic py-2 text-center border border-dashed border-neutral-200 rounded-xl">
                    All providers added
                  </p>
                ) : (
                  availableForNew.map(p => (
                    <div key={p.id} className="border border-neutral-200 rounded-xl overflow-hidden">
                      <div className="flex items-center gap-2 px-3 py-2 bg-neutral-50">
                        <span className="text-xs font-semibold text-neutral-800">{p.name}</span>
                        <span className="text-[10px] font-mono text-neutral-400">{p.id}</span>
                        <button onClick={() => addNewEntry(p.id)}
                          className="ml-auto inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium text-white bg-neutral-900 hover:bg-neutral-800 transition-colors cursor-pointer">
                          <Plus className="w-3 h-3" />
                          Add
                        </button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
            <div className="flex justify-end space-x-2 px-6 py-4 bg-neutral-50 border-t border-neutral-200 shrink-0">
              <button onClick={() => setIsEditing(null)} className={btnSecondary}>Cancel</button>
              <button onClick={save} disabled={entries.length === 0}
                className="bg-neutral-900 text-white hover:bg-neutral-800 disabled:bg-neutral-300 px-4 py-2 rounded-xl text-xs font-semibold transition-colors cursor-pointer">
                {isEditing === "new" ? "Create Group" : "Save Changes"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Group cards */}
      {groups.length === 0 && !isEditing && (
        <div className={`${cardClass} p-8 text-center text-neutral-400 text-sm`}>
          <Layers className="w-8 h-8 mx-auto mb-3 text-neutral-300" />
          <p>No groups configured.</p>
          <p className="text-xs text-neutral-400 mt-1">Create a group to define provider fallback chains with per-model routing.</p>
        </div>
      )}

      {groups.map((g) => {
        const testEntriesData = getTestEntries(g.id);
        const isDeleting = confirmDeleteId === g.id;
        return (
          <div key={g.id} className={cardClass}>
            <div className="px-5 py-3.5 flex items-center justify-between gap-3 border-b border-neutral-100">
              <div className="flex items-center gap-3 min-w-0">
                <div className="bg-violet-100 border border-violet-200 p-1.5 rounded-xl shrink-0">
                  <Layers className="w-4 h-4 text-violet-600" />
                </div>
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-display font-semibold text-neutral-800 text-sm">{g.name}</span>
                    <span className="text-[10px] font-mono text-neutral-400 bg-neutral-100 px-1.5 py-0.5 rounded">{g.id}</span>
                    <span className="text-[10px] text-neutral-400">{(g.entries || []).length} entries</span>
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-0.5 shrink-0">
                <button onClick={() => handleTest(g.id)} disabled={isTesting} title="Test" className={btnIcon}>
                  {isTesting ? <Loader2 className="w-4 h-4 animate-spin" /> : <CheckCircle2 className="w-4 h-4" />}
                </button>
                <button onClick={() => startEdit(g)} title="Edit" className={btnIcon}>
                  <Edit2 className="w-4 h-4" />
                </button>
                {isDeleting ? (
                  <div className="flex items-center gap-1">
                    <button onClick={() => { onDelete(g.id); setConfirmDeleteId(null); }}
                      className="bg-red-600 hover:bg-red-500 text-white px-2 py-1 rounded text-[10px] font-semibold transition-colors cursor-pointer">
                      Confirm
                    </button>
                    <button onClick={() => setConfirmDeleteId(null)}
                      className="bg-neutral-100 hover:bg-neutral-200 text-neutral-600 px-2 py-1 rounded text-[10px] font-medium transition-colors cursor-pointer">
                      Cancel
                    </button>
                  </div>
                ) : (
                  <button onClick={() => setConfirmDeleteId(g.id)} title="Delete"
                    className="p-2 text-red-400 hover:text-red-600 hover:bg-red-50 rounded-lg transition-colors cursor-pointer">
                    <Trash2 className="w-4 h-4" />
                  </button>
                )}
              </div>
            </div>

            {/* Entry chain - compact */}
            <div className="px-5 py-3">
              <div className="flex flex-wrap items-center gap-x-1 gap-y-1">
                {g.entries?.map((e, idx) => {
                  const p = providers.find(x => x.id === e.providerId);
                  const missing = !p;
                  return (
                    <React.Fragment key={e.providerId + idx}>
                      <div className={`inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg border text-xs ${
                        missing
                          ? "border-red-200 bg-red-50"
                          : "border-neutral-200 bg-neutral-50 hover:border-neutral-300"
                      }`}>
                        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${missing ? "bg-red-400" : "bg-emerald-400"}`} />
                        <span className="font-mono font-semibold text-neutral-700">{e.providerId}</span>
                        {!missing && <span className="text-neutral-400 text-[10px]">{p.name}</span>}
                        {e.models?.length > 0 ? (
                          <span className="text-[10px] font-mono text-violet-600 bg-violet-50 px-1 rounded truncate max-w-[160px]" title={e.models.join(", ")}>
                            {e.models.join(", ")}
                          </span>
                        ) : (
                          <span className="text-[10px] text-neutral-400">all models</span>
                        )}
                        <span className="text-neutral-300 text-[10px]">#{idx + 1}</span>
                      </div>
                      {idx < (g.entries?.length || 0) - 1 && (
                        <ChevronRight className="w-3 h-3 text-neutral-300 shrink-0" />
                      )}
                    </React.Fragment>
                  );
                })}
                {g.entries?.length === 0 && (
                  <p className="text-xs text-neutral-400 italic">No entries</p>
                )}
              </div>
            </div>

            {/* Test results */}
            {testEntriesData.length > 0 && (
              <div className="mx-5 mb-4 p-3 bg-neutral-50 border border-neutral-150 rounded-xl">
                <p className="text-[10px] font-medium text-neutral-400 uppercase tracking-wider mb-2">Test Results</p>
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
                  {testEntriesData.map((tr, i) => (
                    <div key={i} className={`px-2.5 py-2 rounded-lg border text-xs ${
                      tr.ok
                        ? "border-emerald-200 bg-emerald-50 text-emerald-700"
                        : "border-red-200 bg-red-50 text-red-700"
                    }`}>
                      <div className="flex items-center gap-1.5">
                        {tr.ok ? <CheckCircle2 className="w-3 h-3 shrink-0" /> : <XCircle className="w-3 h-3 shrink-0" />}
                        <span className="font-mono font-semibold">{tr.model}</span>
                      </div>
                      <p className="text-[10px] text-neutral-500 mt-0.5">
                        {tr.providerId} {tr.durationMs}ms{tr.status ? ` · ${tr.status}` : ""}
                      </p>
                      {tr.error && <p className="text-[10px] text-red-500 truncate mt-0.5">{tr.error}</p>}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
