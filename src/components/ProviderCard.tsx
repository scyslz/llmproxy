import React, { useState } from "react";
import { Provider } from "../types";
import { Layers, Plus, Trash2, Edit2, Play, CheckCircle2, Shield, Eye, EyeOff, Globe, RefreshCw, Search, Check, X, Sparkles } from "lucide-react";
import { apiFetch } from "../lib/api";

interface ProviderCardProps {
  providers: Provider[];
  onEnable: (id: string) => void;
  onCreate: (provider: Omit<Provider, "enabled">) => void;
  onUpdate: (id: string, provider: Partial<Provider>) => void;
  onDelete: (id: string) => void;
}

const PRESETS = [
  {
    id: "gemini",
    name: "Google Gemini",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
    models: ["gemini-2.5-flash", "gemini-2.5-pro", "gemini-1.5-flash"]
  },
  {
    id: "openai",
    name: "OpenAI",
    baseUrl: "https://api.openai.com",
    models: ["gpt-4o", "gpt-4o-mini", "o1-mini"]
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    baseUrl: "https://api.deepseek.com",
    models: ["deepseek-chat", "deepseek-reasoner"]
  },
  {
    id: "anthropic",
    name: "Anthropic (via Proxy)",
    baseUrl: "https://api.anthropic.com",
    models: ["claude-3-5-sonnet-latest", "claude-3-5-haiku-latest"]
  }
];

const formatTokens = (n: number): string => {
  if (n >= 1000000) return `${(n / 1000000).toFixed(n % 1000000 === 0 ? 0 : 1)}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return `${n}`;
};

export default function ProviderCard({
  providers,
  onEnable,
  onCreate,
  onUpdate,
  onDelete
}: ProviderCardProps) {
  const [isEditing, setIsEditing] = useState<string | null>(null); // id of editing, or 'new'
  const [formData, setFormData] = useState({
    id: "",
    name: "",
    baseUrl: "",
    apiKey: "",
    modelsString: "",
      concurrency: 0,
      chatEndpoint: "",
      responsesEndpoint: "",
      defaultModel: "",
      protocol: "",
      modelContextLengths: {} as Record<string, number>,
      modelMaxOutputs: {} as Record<string, number>
  });
  const [showApiKeyId, setShowApiKeyId] = useState<string | null>(null);
  const [showApiKeyForm, setShowApiKeyForm] = useState(false);

  // New states for API model fetching and keyword filtering
  const [isFetchingModels, setIsFetchingModels] = useState(false);
  const [isLookingUpContext, setIsLookingUpContext] = useState(false);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [lookupNote, setLookupNote] = useState<string | null>(null);
  const [modelKeyword, setModelKeyword] = useState("");
  const [availableRemoteModels, setAvailableRemoteModels] = useState<string[]>([]);
  const [cardSearchKeywords, setCardSearchKeywords] = useState<Record<string, string>>({});
  const [syncingCardId, setSyncingCardId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const resetModalState = () => {
    setIsFetchingModels(false);
    setIsLookingUpContext(false);
    setFetchError(null);
    setLookupNote(null);
    setModelKeyword("");
    setAvailableRemoteModels([]);
  };

  const applyPreset = (preset: typeof PRESETS[0]) => {
    resetModalState();
    setFormData({
      id: preset.id,
      name: preset.name,
      baseUrl: preset.baseUrl,
      apiKey: "",
        modelsString: preset.models.join(", "),
        concurrency: 0,
        chatEndpoint: "",
        responsesEndpoint: "",
        defaultModel: "",
        protocol: "",
        modelContextLengths: {},
        modelMaxOutputs: {}
    });
  };

  const handleEditClick = (p: Provider) => {
    resetModalState();
    setFormData({
      id: p.id || "",
      name: p.name || "",
      baseUrl: p.baseUrl || "",
      apiKey: p.apiKey || "",
      modelsString: (p.models || []).join(", "),
      concurrency: p.concurrency || 0,
        chatEndpoint: p.chatEndpoint || "",
        responsesEndpoint: p.responsesEndpoint || "",
      defaultModel: p.defaultModel || "",
      protocol: p.protocol || "",
      modelContextLengths: p.modelContextLengths || {},
      modelMaxOutputs: p.modelMaxOutputs || {}
    });
    setIsEditing(p.id);
  };

  const handleCreateClick = () => {
    resetModalState();
    setFormData({
      id: "",
      name: "",
      baseUrl: "",
      apiKey: "",
        modelsString: "",
        concurrency: 0,
        chatEndpoint: "",
        responsesEndpoint: "",
        defaultModel: "",
        protocol: "",
        modelContextLengths: {},
        modelMaxOutputs: {}
    });
    setIsEditing("new");
  };

  const handleFetchRemoteModels = async () => {
    if (!formData.baseUrl) {
      setFetchError("Please enter a Base URL first.");
      return;
    }
    setIsFetchingModels(true);
    setFetchError(null);
    try {
      const res = await apiFetch("/api/providers/fetch-remote-models", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id: formData.id || (isEditing !== "new" && isEditing ? isEditing : ""),
          baseUrl: formData.baseUrl,
          apiKey: formData.apiKey
        })
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || "Failed to fetch models");
      }
      if (data.models && data.models.length > 0) {
        setAvailableRemoteModels(data.models);
        const currentList = (formData.modelsString || "")
          .split(",")
          .map(m => (m || "").trim())
          .filter(Boolean);
        const merged = Array.from(new Set([...currentList, ...data.models]));
        const incoming = (data.contextLengths || {}) as Record<string, number>;
        setFormData(prev => ({
          ...prev,
          modelsString: merged.join(", "),
          modelContextLengths: { ...(prev.modelContextLengths || {}), ...incoming },
          modelMaxOutputs: { ...(prev.modelMaxOutputs || {}), ...((data.maxOutputs || {}) as Record<string, number>) }
        }));
      } else {
        setFetchError("No models returned from provider endpoint.");
      }
    } catch (err: any) {
      setFetchError(err.message || "Failed to fetch models from API");
    } finally {
      setIsFetchingModels(false);
    }
  };

  const handleAutoLookupContext = async () => {
    const models = currentSelectedModels;
    if (models.length === 0) {
      setFetchError("Add at least one model before looking up context lengths.");
      return;
    }
    setIsLookingUpContext(true);
    setFetchError(null);
    setLookupNote(null);
    try {
      const llmModel = (formData.defaultModel || models[0] || "").trim();
      const llm = (formData.baseUrl && formData.apiKey && llmModel)
        ? { baseUrl: formData.baseUrl, apiKey: formData.apiKey, model: llmModel }
        : undefined;
      const res = await apiFetch("/api/models/lookup-context", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ models, useLLM: true, llm })
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || "Failed to look up context lengths");
      }
      const incoming = (data.contextLengths || {}) as Record<string, number>;
      const fallbacks = (data.fallbacks || []) as string[];
      if (Object.keys(incoming).length === 0) {
        setLookupNote("No context data available; defaults applied where possible.");
        return;
      }
      setFormData(prev => ({
        ...prev,
        modelContextLengths: { ...(prev.modelContextLengths || {}), ...incoming },
        modelMaxOutputs: { ...(prev.modelMaxOutputs || {}), ...((data.maxOutputs || {}) as Record<string, number>) }
      }));
      const notes: string[] = [];
      if (fallbacks.length > 0) {
        notes.push(`Default limits applied to: ${fallbacks.join(", ")}`);
      }
      if (data.llmError) {
        notes.push(`LLM lookup unavailable, defaults used instead.`);
      }
      if (notes.length > 0) {
        setLookupNote(notes.join(" | "));
      }
    } catch (err: any) {
      setLookupNote(`Lookup failed (${err.message || "unknown error"}); no values were changed.`);
    } finally {
      setIsLookingUpContext(false);
    }
  };

  const setModelContextLength = (model: string, value: number) => {
    setFormData(prev => {
      const next = { ...(prev.modelContextLengths || {}) };
      if (value > 0) {
        next[model] = value;
      } else {
        delete next[model];
      }
      return { ...prev, modelContextLengths: next };
    });
  };

  const setModelMaxOutput = (model: string, value: number) => {
    setFormData(prev => {
      const next = { ...(prev.modelMaxOutputs || {}) };
      if (value > 0) {
        next[model] = value;
      } else {
        delete next[model];
      }
      return { ...prev, modelMaxOutputs: next };
    });
  };

  const handleQuickSyncCard = async (p: Provider) => {
    setSyncingCardId(p.id);
    try {
      const res = await apiFetch("/api/providers/fetch-remote-models", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id: p.id,
          baseUrl: p.baseUrl,
          apiKey: p.apiKey
        })
      });
      const data = await res.json();
      if (res.ok && data.models && data.models.length > 0) {
        const patch: Partial<Provider> = { models: data.models };
        if (data.contextLengths && Object.keys(data.contextLengths).length > 0) {
          patch.modelContextLengths = data.contextLengths;
        }
        if (data.maxOutputs && Object.keys(data.maxOutputs).length > 0) {
          patch.modelMaxOutputs = data.maxOutputs;
        }
        onUpdate(p.id, patch);
      } else {
        alert(data.error || "No models returned from provider endpoint.");
      }
    } catch (err: any) {
      alert(err.message || "Failed to sync models");
    } finally {
      setSyncingCardId(null);
    }
  };

  const currentSelectedModels = (formData.modelsString || "")
    .split(",")
    .map(m => (m || "").trim())
    .filter(Boolean);

  const allKnownModels = Array.from(new Set([...currentSelectedModels, ...availableRemoteModels])).sort();

  const filteredModels = allKnownModels.filter(m =>
    m.toLowerCase().includes(modelKeyword.toLowerCase())
  );

  const toggleModelSelection = (model: string) => {
    let nextList = [...currentSelectedModels];
    if (nextList.includes(model)) {
      nextList = nextList.filter(m => m !== model);
    } else {
      nextList.push(model);
    }
    setFormData(prev => ({ ...prev, modelsString: nextList.join(", ") }));
  };

  const selectAllFiltered = () => {
    const merged = Array.from(new Set([...currentSelectedModels, ...filteredModels]));
    setFormData(prev => ({ ...prev, modelsString: merged.join(", ") }));
  };

  const deselectAllFiltered = () => {
    const nextList = currentSelectedModels.filter(m => !filteredModels.includes(m));
    setFormData(prev => ({ ...prev, modelsString: nextList.join(", ") }));
  };

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault();
    const models = (formData.modelsString || "")
      .split(",")
      .map((m) => (m || "").trim())
      .filter((m) => m !== "");

    const dm = (formData.defaultModel || "").trim();
    const defaultModel = (dm && models.includes(dm)) ? dm : undefined;
    const modelContextLengths: Record<string, number> = {};
    const modelMaxOutputs: Record<string, number> = {};
    for (const m of models) {
      const n = formData.modelContextLengths?.[m];
      if (n && n > 0) modelContextLengths[m] = n;
      const o = formData.modelMaxOutputs?.[m];
      if (o && o > 0) modelMaxOutputs[m] = o;
    }

    const submissionData = {
      id: (formData.id || "").toLowerCase().trim(),
      name: (formData.name || "").trim(),
      baseUrl: (formData.baseUrl || "").trim(),
      apiKey: (formData.apiKey || "").trim(),
      models,
      concurrency: formData.concurrency || 0,
      chatEndpoint: (formData.chatEndpoint || "").trim() || undefined,
      responsesEndpoint: (formData.responsesEndpoint || "").trim() || undefined,
      defaultModel,
      protocol: formData.protocol || undefined,
      modelContextLengths,
      modelMaxOutputs
    };

    if (isEditing === "new") {
      onCreate(submissionData);
    } else if (isEditing) {
      onUpdate(isEditing, submissionData);
    }
    setIsEditing(null);
    resetModalState();
  };

  const renderEditModal = () => {
    if (!isEditing) return null;
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-neutral-900/20 backdrop-blur-sm animate-fade-in">
        <div className="bg-white rounded-2xl shadow-xl max-w-2xl w-full max-h-[90vh] flex flex-col overflow-hidden border border-neutral-200">
          <div className="flex justify-between items-center px-6 py-4 bg-neutral-50 border-b border-neutral-200 text-neutral-800 shrink-0">
            <div className="flex items-center space-x-2.5">
              <Layers className="w-5 h-5 text-neutral-700" />
              <h4 className="font-display font-semibold text-base">
                {isEditing === "new" ? "New Provider" : `Edit ${formData.name}`}
              </h4>
            </div>
            <button
              type="button"
              onClick={() => { setIsEditing(null); resetModalState(); }}
              className="text-neutral-400 hover:text-neutral-700 transition-colors p-1 rounded-lg hover:bg-neutral-100 cursor-pointer"
              title="Close modal"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          <form onSubmit={handleSave} className="p-6 overflow-y-auto space-y-5 flex-1 bg-white">
            {/* Preset Buttons */}
            {isEditing === "new" && (
              <div className="space-y-2 bg-white p-4 rounded-xl border border-neutral-200/80 shadow-2xs">
                <label className="text-[11px] font-bold uppercase tracking-wider text-neutral-500 block">Quick Presets (Auto-fill configuration)</label>
                <div className="flex flex-wrap gap-2">
                  {PRESETS.map((preset) => (
                    <button
                      key={preset.id}
                      type="button"
                      onClick={() => applyPreset(preset)}
                      className="px-3 py-1.5 bg-neutral-50 hover:bg-emerald-50 hover:text-emerald-700 border border-neutral-200 hover:border-emerald-300 text-neutral-700 rounded-xl text-xs font-semibold transition-all shadow-2xs cursor-pointer"
                    >
                      {preset.name}
                    </button>
                  ))}
                </div>
              </div>
            )}

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-neutral-700">Unique Provider ID</label>
                <input
                  type="text"
                  required
                  disabled={isEditing !== "new"}
                  placeholder="e.g. deepseek, gemini"
                  value={formData.id}
                  onChange={(e) => setFormData({ ...formData, id: e.target.value })}
                  className="w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 disabled:bg-neutral-100 disabled:text-neutral-400 font-mono shadow-2xs"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-neutral-700">Display Name</label>
                <input
                  type="text"
                  required
                  placeholder="e.g. OpenAI Production"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  className="w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 shadow-2xs"
                />
              </div>
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-semibold text-neutral-700">Protocol</label>
              <select
                value={formData.protocol}
                onChange={(e) => setFormData({ ...formData, protocol: e.target.value })}
                className="select w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 cursor-pointer shadow-2xs"
              >
                <option value="">Auto (probe on 404)</option>
                <option value="chat">Chat Completions (/v1/chat/completions)</option>
                <option value="responses">Responses (/v1/responses)</option>
              </select>
              <p className="text-[10px] text-neutral-500">Declare the provider's conversation protocol. Leave empty to auto-detect per model via a one-time 404 probe.</p>
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-semibold text-neutral-700">Base URL (OpenAI-Compatible endpoint)</label>
              <input
                type="url"
                required
                placeholder="https://api.openai.com/v1"
                value={formData.baseUrl}
                onChange={(e) => setFormData({ ...formData, baseUrl: e.target.value })}
                className="w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 font-mono shadow-2xs"
              />
            </div>

            {formData.protocol !== "responses" && (
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-neutral-700">Chat Path (optional)</label>
                <input
                  type="text"
                  placeholder="/chat/completions"
                  value={formData.chatEndpoint}
                  onChange={(e) => setFormData({ ...formData, chatEndpoint: e.target.value })}
                  className="w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 font-mono shadow-2xs"
                />
                <p className="text-[10px] text-neutral-500">Optional. Overrides the default /chat/completions path.</p>
              </div>
            )}
            {formData.protocol !== "chat" && (
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-neutral-700">Responses Path (optional)</label>
                <input
                  type="text"
                  placeholder="/responses"
                  value={formData.responsesEndpoint}
                  onChange={(e) => setFormData({ ...formData, responsesEndpoint: e.target.value })}
                  className="w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 font-mono shadow-2xs"
                />
                <p className="text-[10px] text-neutral-500">Optional. Overrides the default /responses path.</p>
              </div>
            )}

            <div className="space-y-1.5">
              <label className="text-xs font-semibold text-neutral-700">API Key / Token (Saved Server-Side)</label>
              <div className="relative">
                <input
                  type={showApiKeyForm ? "text" : "password"}
                  placeholder="Bearer Key (Leave empty to use client keys)"
                  value={formData.apiKey}
                  onChange={(e) => setFormData({ ...formData, apiKey: e.target.value })}
                  className="w-full bg-white border border-neutral-300 rounded-xl pl-3.5 pr-12 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 font-mono shadow-2xs"
                />
                <button
                  type="button"
                  onClick={() => setShowApiKeyForm(!showApiKeyForm)}
                  className="absolute inset-y-0 right-0 px-3 flex items-center text-neutral-400 hover:text-neutral-700 transition-colors cursor-pointer"
                  title={showApiKeyForm ? "Hide key" : "Show key"}
                >
                  {showApiKeyForm ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                </button>
              </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-neutral-700">Max Concurrency</label>
                <input
                  type="number"
                  min="0"
                  placeholder="0 = unlimited"
                  value={formData.concurrency}
                  onChange={(e) => setFormData({ ...formData, concurrency: parseInt(e.target.value) || 0 })}
                  className="w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 font-mono shadow-2xs"
                />
                <p className="text-[10px] text-neutral-500">Max concurrent requests to this provider. 0 = unlimited.</p>
              </div>
            </div>

            {/* Model Section with API Fetching & Keyword Filtering */}
            <div className="space-y-3 bg-white border border-neutral-200 rounded-xl p-4 shadow-2xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0">
                  <label className="text-xs font-bold text-neutral-800 block">Supported Models</label>
                  <p className="text-[11px] text-neutral-500">First model listed will act as the substitution fallback.</p>
                </div>
                <div className="flex flex-nowrap items-center gap-2 shrink-0">
                  <button
                    type="button"
                    onClick={handleFetchRemoteModels}
                    disabled={isFetchingModels || !formData.baseUrl}
                    className="inline-flex items-center space-x-1.5 text-xs font-semibold bg-emerald-600 hover:bg-emerald-500 disabled:bg-neutral-300 text-white px-3 py-1.5 rounded-lg shadow-sm transition-colors cursor-pointer whitespace-nowrap"
                    title="Fetch available models directly from provider API"
                  >
                    <RefreshCw className={`w-3.5 h-3.5 ${isFetchingModels ? "animate-spin" : ""}`} />
                    <span>{isFetchingModels ? "Fetching..." : "Fetch API"}</span>
                  </button>
                  <button
                    type="button"
                    onClick={handleAutoLookupContext}
                    disabled={isLookingUpContext || currentSelectedModels.length === 0}
                    className="inline-flex items-center space-x-1.5 text-xs font-semibold bg-indigo-600 hover:bg-indigo-500 disabled:bg-neutral-300 text-white px-3 py-1.5 rounded-lg shadow-sm transition-colors cursor-pointer whitespace-nowrap"
                    title="Look up each model's max context length: searches a public model database, then asks the model itself for anything missing"
                  >
                    <Sparkles className={`w-3.5 h-3.5 ${isLookingUpContext ? "animate-pulse" : ""}`} />
                    <span>{isLookingUpContext ? "Looking up..." : "Auto Context"}</span>
                  </button>
                </div>
              </div>

              {fetchError && (
                <div className="text-xs text-red-600 bg-red-50 border border-red-200 rounded-lg p-2.5 font-medium">
                  {fetchError}
                </div>
              )}
              {lookupNote && (
                <div className="text-xs text-neutral-600 bg-neutral-50 border border-neutral-200 rounded-lg p-2.5">
                  {lookupNote}
                </div>
              )}

              {/* Keyword Filter & Bulk Actions */}
              {allKnownModels.length > 0 && (
                <div className="space-y-2 pt-1">
                  <div className="flex items-center space-x-2">
                    <div className="relative flex-1">
                      <Search className="w-3.5 h-3.5 text-neutral-400 absolute left-3 top-1/2 -translate-y-1/2" />
                      <input
                        type="text"
                        placeholder="Filter models by keyword (e.g. flash, gpt-4, reasoner)..."
                        value={modelKeyword}
                        onChange={(e) => setModelKeyword(e.target.value)}
                        className="w-full bg-neutral-50 border border-neutral-200 rounded-lg pl-8 pr-8 py-1.5 text-xs text-neutral-800 outline-none focus:border-neutral-400 focus:bg-white"
                      />
                      {modelKeyword && (
                        <button
                          type="button"
                          onClick={() => setModelKeyword("")}
                          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-neutral-400 hover:text-neutral-600"
                        >
                          <X className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </div>
                    <button
                      type="button"
                      onClick={selectAllFiltered}
                      className="text-[11px] font-semibold bg-neutral-100 hover:bg-neutral-200 text-neutral-700 px-2.5 py-1.5 rounded-lg transition-colors whitespace-nowrap cursor-pointer"
                    >
                      Select Filtered
                    </button>
                    <button
                      type="button"
                      onClick={deselectAllFiltered}
                      className="text-[11px] font-semibold bg-neutral-100 hover:bg-neutral-200 text-neutral-700 px-2.5 py-1.5 rounded-lg transition-colors whitespace-nowrap cursor-pointer"
                    >
                      Deselect Filtered
                    </button>
                  </div>

                  {/* Model Pill Checkboxes */}
                  <div className="border border-neutral-200 rounded-lg p-2.5 max-h-36 overflow-y-auto bg-neutral-50/50 flex flex-wrap gap-1.5">
                    {filteredModels.length === 0 ? (
                      <div className="text-xs text-neutral-400 italic py-2 w-full text-center">
                        No models match keyword "{modelKeyword}"
                      </div>
                    ) : (
                      filteredModels.map((m) => {
                        const isSelected = currentSelectedModels.includes(m);
                        return (
                          <button
                            key={m}
                            type="button"
                            onClick={() => toggleModelSelection(m)}
                            className={`text-xs font-mono px-2.5 py-1 rounded-md transition-all flex items-center space-x-1.5 cursor-pointer ${
                              isSelected
                                ? "bg-neutral-900 text-white font-medium shadow-2xs"
                                : "bg-white border border-neutral-200 text-neutral-600 hover:border-neutral-400"
                            }`}
                          >
                            <span>{m}</span>
                            {isSelected ? <Check className="w-3 h-3 text-white shrink-0" /> : <Plus className="w-3 h-3 text-neutral-400 shrink-0" />}
                          </button>
                        );
                      })
                    )}
                  </div>
                </div>
              )}

              <div className="relative">
                <textarea
                  rows={2}
                  placeholder="gpt-4o, gpt-4o-mini, o1-mini"
                  value={formData.modelsString}
                  onChange={(e) => setFormData({ ...formData, modelsString: e.target.value })}
                  className="w-full bg-neutral-50 border border-neutral-300 rounded-xl px-3.5 py-2 text-sm font-mono text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 focus:bg-white transition-colors"
                />
                {(formData.modelsString || "").trim() !== "" && (
                  <button
                    type="button"
                    onClick={() => setFormData({ ...formData, modelsString: "", defaultModel: "" })}
                    className="absolute right-2 top-1.5 p-1 rounded-md text-neutral-400 hover:text-red-500 hover:bg-red-50 transition-colors cursor-pointer"
                    title="Clear all models"
                  >
                    <X className="w-3.5 h-3.5" />
                  </button>
                )}
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-neutral-700">Default Model (fallback)</label>
                <select
                  value={formData.defaultModel}
                  onChange={(e) => setFormData({ ...formData, defaultModel: e.target.value })}
                  className="select w-full bg-white border border-neutral-300 rounded-xl px-3.5 py-2 text-sm text-neutral-800 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 cursor-pointer shadow-2xs"
                >
                  <option value="">Auto (first model in list)</option>
                  {currentSelectedModels.map((m) => (
                    <option key={m} value={m}>{m}</option>
                  ))}
                </select>
                <p className="text-[10px] text-neutral-500">Used when a request model is not in the Supported Models list. Leave empty to keep the current first-model behavior.</p>
              </div>

              {/* Context & Output Limits */}
              <div className="space-y-2 border-t border-neutral-100 pt-3">
                <div className="flex items-center justify-between">
                  <label className="text-xs font-bold text-neutral-800">Context / Max Output (tokens)</label>
                  <span className="text-[10px] text-neutral-400">
                    {currentSelectedModels.filter(m => (formData.modelContextLengths?.[m] || 0) > 0).length}/{currentSelectedModels.length} set
                  </span>
                </div>
                {currentSelectedModels.length === 0 ? (
                  <p className="text-[10px] text-neutral-400 italic">Add models to set their limits.</p>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-1.5 max-h-40 overflow-y-auto pr-1">
                    {currentSelectedModels.map((m) => {
                      const val = formData.modelContextLengths?.[m] || 0;
                      const out = formData.modelMaxOutputs?.[m] || 0;
                      return (
                        <div key={m} className="flex items-center gap-1.5 bg-neutral-50 border border-neutral-200 rounded-lg px-2 py-1">
                          <span className="text-[10px] font-mono text-neutral-600 truncate flex-1" title={m}>{m}</span>
                          {val > 0 && <span className="text-[9px] text-emerald-600 font-semibold shrink-0" title="Context">{formatTokens(val)}</span>}
                          {out > 0 && <span className="text-[9px] text-sky-600 font-semibold shrink-0" title="Max output">{formatTokens(out)}</span>}
                          <input
                            type="number"
                            min="0"
                            placeholder="ctx"
                            title="Max context length"
                            value={val || ""}
                            onChange={(e) => setModelContextLength(m, parseInt(e.target.value) || 0)}
                            className="w-16 text-[10px] font-mono text-right bg-white border border-neutral-200 rounded px-1.5 py-0.5 outline-none focus:border-neutral-400"
                          />
                          <input
                            type="number"
                            min="0"
                            placeholder="out"
                            title="Max output tokens"
                            value={out || ""}
                            onChange={(e) => setModelMaxOutput(m, parseInt(e.target.value) || 0)}
                            className="w-16 text-[10px] font-mono text-right bg-white border border-neutral-200 rounded px-1.5 py-0.5 outline-none focus:border-neutral-400"
                          />
                          {(val > 0 || out > 0) && (
                            <button
                              type="button"
                              onClick={() => { setModelContextLength(m, 0); setModelMaxOutput(m, 0); }}
                              className="text-neutral-400 hover:text-red-500 shrink-0 cursor-pointer"
                              title="Clear limits"
                            >
                              <X className="w-3 h-3" />
                            </button>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
                <p className="text-[10px] text-neutral-500">Auto Context searches a public model database by model name, then asks the model itself for anything still missing, and fills the max context window and max output. You can override any value manually.</p>
              </div>

            </div>

            {/* Modal Footer */}
            <div className="flex justify-end space-x-2 pt-2 border-t border-neutral-200">
              <button
                type="button"
                onClick={() => { setIsEditing(null); resetModalState(); }}
                className="bg-white border border-neutral-300 text-neutral-700 hover:bg-neutral-100 px-5 py-2.5 rounded-xl text-xs font-semibold transition-colors cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                className="bg-neutral-900 text-white hover:bg-neutral-800 px-6 py-2.5 rounded-xl text-xs font-semibold transition-colors shadow-2xs cursor-pointer"
              >
                Save Provider
              </button>
            </div>
          </form>
        </div>
      </div>
    );
  };

  return (
    <div className="space-y-6">
      {/* Top action row */}
      <div className="flex justify-between items-center gap-3">
        <div className="min-w-0 pr-2">
          <h3 className="font-display font-semibold text-neutral-800 text-base truncate">Backend Providers</h3>
          <p className="text-xs text-neutral-500 truncate sm:whitespace-normal">Configure LLM providers and their available models</p>
        </div>
        <button
          onClick={handleCreateClick}
          className="inline-flex items-center space-x-1.5 text-xs font-semibold bg-neutral-900 text-white hover:bg-neutral-800 px-3.5 py-2 sm:px-4 sm:py-2.5 rounded-xl shadow-2xs transition-colors cursor-pointer shrink-0 whitespace-nowrap"
        >
          <Plus className="w-4 h-4 shrink-0" />
          <span className="hidden sm:inline">New Provider</span>
          <span className="sm:hidden">New</span>
        </button>
      </div>

      {/* Render Modal Dialog when editing or creating */}
      {renderEditModal()}

      {/* Compact, dense Grid of existing providers for PC (3 columns on lg, 4 on xl) */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4 sm:gap-5">
        {providers.map((p) => {
          const isActive = p.enabled;
          const hasKey = p.apiKey && p.apiKey.length > 0;
          const cardKeyword = cardSearchKeywords[p.id] || "";
          const cardFilteredModels = (p.models || []).filter(m =>
            m.toLowerCase().includes(cardKeyword.toLowerCase())
          );
          if (p.defaultModel && cardFilteredModels.includes(p.defaultModel)) {
            cardFilteredModels.sort((a, b) =>
              (a === p.defaultModel ? -1 : 0) - (b === p.defaultModel ? -1 : 0)
            );
          }

          return (
            <div
              key={p.id}
              className={`border rounded-2xl p-4 sm:p-4.5 shadow-xs transition-all flex flex-col justify-between relative ${
                isActive
                  ? "border-neutral-900 ring-1 ring-neutral-900 bg-neutral-50/60"
                  : "border-neutral-200/80 bg-white hover:border-neutral-300 hover:shadow-md"
              }`}
            >
              <div>
                {/* Top Row with Badge */}
                <div className="flex justify-between items-start mb-2.5 gap-2">
                  <div className="flex items-center space-x-2 min-w-0">
                    <span className={`w-2.5 h-2.5 rounded-full shrink-0 ${isActive ? "bg-emerald-500 animate-pulse" : "bg-neutral-300"}`}></span>
                    <h4 className="font-display font-bold text-neutral-900 text-sm truncate" title={p.name}>{p.name}</h4>
                    <span className="text-[10px] bg-neutral-100 text-neutral-600 font-extrabold px-1.5 py-0.5 rounded uppercase font-mono shrink-0">
                      {p.id}
                    </span>
                  </div>
                  {isActive && (
                    <span className="bg-emerald-50 text-emerald-700 text-[9px] font-extrabold uppercase tracking-wider px-2 py-0.5 rounded-full border border-emerald-200/60 shrink-0">
                      Active
                    </span>
                  )}
                </div>

                {/* Sub-details */}
                <div className="space-y-2.5 mb-3 text-xs">
                  <div className="flex items-center text-neutral-500 font-mono break-all bg-neutral-50 rounded-lg p-1.5 border border-neutral-150">
                    <Globe className="w-3.5 h-3.5 mr-1 text-neutral-400 shrink-0" />
                    <span className="text-[10px] select-all truncate" title={p.baseUrl}>{p.baseUrl}</span>
                    <span className="ml-1 shrink-0 px-1.5 py-0.5 rounded text-[9px] font-semibold uppercase bg-neutral-200 text-neutral-600">
                      {p.protocol || "auto"}
                    </span>
                  </div>

                  <div className="flex items-center justify-between text-neutral-500 text-[11px]">
                    <span>API Key Storage:</span>
                    <div className="flex items-center space-x-1">
                      <span className="font-semibold font-mono text-neutral-700">
                        {hasKey ? (
                          showApiKeyId === p.id ? (
                            p.apiKey
                          ) : (
                            "••••••••••••"
                          )
                        ) : (
                          <span className="text-red-500 font-semibold italic">Missing Key</span>
                        )}
                      </span>
                      {hasKey && (
                        <button
                          onClick={() => setShowApiKeyId(showApiKeyId === p.id ? null : p.id)}
                          className="text-neutral-400 hover:text-neutral-700 p-0.5 cursor-pointer"
                          title={showApiKeyId === p.id ? "Hide key" : "Show key"}
                        >
                          {showApiKeyId === p.id ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
                        </button>
                      )}
                    </div>
                  </div>

                  <div className="flex items-center justify-between text-neutral-500 text-[11px]">
                    <span>Concurrency:</span>
                    <span className="font-semibold font-mono text-neutral-700">
                      {p.concurrency && p.concurrency > 0 ? p.concurrency : "Unlimited"}
                    </span>
                  </div>

                  {/* Models Section with API Sync and Keyword Search */}
                  <div className="pt-1 border-t border-neutral-100">
                    <div className="flex items-center justify-between mb-1.5">
                      <span className="text-neutral-600 font-semibold text-[11px]">Models ({(p.models || []).length}):</span>
                      <button
                        onClick={() => handleQuickSyncCard(p)}
                        disabled={syncingCardId === p.id}
                        className="text-[10px] text-emerald-700 hover:text-emerald-800 font-semibold flex items-center space-x-1 bg-emerald-50 hover:bg-emerald-100 px-2 py-0.5 rounded-md transition-colors border border-emerald-200/50 cursor-pointer"
                        title="Fetch latest models from provider API"
                      >
                        <RefreshCw className={`w-2.5 h-2.5 ${syncingCardId === p.id ? "animate-spin" : ""}`} />
                        <span>{syncingCardId === p.id ? "Syncing..." : "Sync API"}</span>
                      </button>
                    </div>

                    {(p.models || []).length > 5 && (
                      <div className="relative mb-1.5">
                        <Search className="w-3 h-3 text-neutral-400 absolute left-2 top-1/2 -translate-y-1/2" />
                        <input
                          type="text"
                          placeholder="Filter models..."
                          value={cardKeyword}
                          onChange={(e) => setCardSearchKeywords({ ...cardSearchKeywords, [p.id]: e.target.value })}
                          className="w-full bg-neutral-50 border border-neutral-200 rounded-md pl-6 pr-6 py-0.5 text-[10px] text-neutral-700 outline-none focus:border-neutral-400 focus:bg-white transition-colors"
                        />
                        {cardKeyword && (
                          <button
                            onClick={() => setCardSearchKeywords({ ...cardSearchKeywords, [p.id]: "" })}
                            className="absolute right-1.5 top-1/2 -translate-y-1/2 text-neutral-400 hover:text-neutral-600"
                          >
                            <X className="w-3 h-3" />
                          </button>
                        )}
                      </div>
                    )}

                    <div className="flex flex-wrap gap-1 max-h-[2.6rem] overflow-y-auto pr-1">
                      {cardFilteredModels.length === 0 ? (
                        <span className="text-[10px] text-neutral-400 italic py-1">No models match "{cardKeyword}"</span>
                      ) : (
                        cardFilteredModels.map((model) => {
                          const isDefault = (p.defaultModel || (p.models || [])[0]) === model;
                          return (
                            <span
                              key={model}
                              className={`text-[10px] font-mono px-1.5 py-0.5 rounded flex items-center space-x-1 ${
                                isDefault
                                  ? "bg-neutral-900 text-white font-semibold shadow-2xs"
                                  : "bg-neutral-100 text-neutral-700 hover:bg-neutral-200"
                              }`}
                              title={isDefault ? "Default substitution fallback model" : "Supported model"}
                            >
                              <span className="truncate max-w-[140px]">{model}</span>
                              {isDefault && <span className="text-amber-400">★</span>}
                            </span>
                          );
                        })
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* Actions row */}
              <div className="border-t border-neutral-150 pt-3 flex justify-between items-center gap-2 mt-2">
                <div className="flex space-x-1.5">
                  <button
                    onClick={() => handleEditClick(p)}
                    className="p-1.5 hover:bg-neutral-100 text-neutral-500 hover:text-neutral-800 rounded-lg transition-colors inline-flex cursor-pointer"
                    title="Edit Provider"
                  >
                    <Edit2 className="w-3.5 h-3.5" />
                  </button>
                  {confirmDeleteId === p.id ? (
                    <div className="inline-flex items-center space-x-1">
                      <button
                        onClick={() => { onDelete(p.id); setConfirmDeleteId(null); }}
                        className="bg-red-600 hover:bg-red-500 text-white px-2 py-1 rounded text-[10px] font-semibold transition-colors cursor-pointer"
                      >
                        Confirm
                      </button>
                      <button
                        onClick={() => setConfirmDeleteId(null)}
                        className="bg-neutral-100 hover:bg-neutral-200 text-neutral-600 px-2 py-1 rounded text-[10px] font-medium transition-colors cursor-pointer"
                      >
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => setConfirmDeleteId(p.id)}
                      className="p-1.5 hover:bg-red-50 text-neutral-400 hover:text-red-500 rounded-lg transition-colors inline-flex cursor-pointer"
                      title="Delete Provider"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>

                {!isActive ? (
                  <button
                    onClick={() => onEnable(p.id)}
                    className="bg-white border border-neutral-300 text-neutral-800 hover:bg-neutral-50 text-[11px] font-semibold px-3 py-1.5 rounded-xl flex items-center space-x-1.5 transition-colors cursor-pointer shadow-2xs"
                  >
                    <Play className="w-3 h-3 text-neutral-800 fill-neutral-800" />
                    <span>Route Here</span>
                  </button>
                ) : (
                  <span className="inline-flex items-center space-x-1 text-[11px] text-emerald-700 font-bold bg-emerald-50 px-2.5 py-1 rounded-xl border border-emerald-200/60">
                    <CheckCircle2 className="w-3.5 h-3.5 text-emerald-500" />
                    <span>Active</span>
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

