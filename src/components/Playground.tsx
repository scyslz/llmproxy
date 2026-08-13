import React, { useState, useEffect, useRef } from "react";
import { Message, VirtualKey } from "../types";
import { Send, Sparkles, AlertCircle, RefreshCw, Layers, Search, X } from "lucide-react";

interface PlaygroundProps {
  virtualKeys: VirtualKey[];
  activeProviderName: string;
  enableVirtualKey?: boolean;
  onStateChange?: (key: string, model: string) => void;
}

export default function Playground({ virtualKeys, activeProviderName, enableVirtualKey, onStateChange }: PlaygroundProps) {
  const [messages, setMessages] = useState<Message[]>([
    { role: "system", content: "You are a helpful assistant talking through the LLM Proxy." }
  ]);
  const [input, setInput] = useState("");
  const [selectedKey, setSelectedKey] = useState<string>(() => (enableVirtualKey && virtualKeys.length > 0) ? virtualKeys[0].key : "");
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>("");
  const [modelKeyword, setModelKeyword] = useState<string>("");
  const [stream, setStream] = useState(true);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  const filteredModels = models.filter(m =>
    m.toLowerCase().includes(modelKeyword.toLowerCase())
  );

  // Load models from proxy /v1/models
  const fetchModels = async () => {
    try {
      setError(null);
      const headers: Record<string, string> = {};
      const activeKey = selectedKey;
      if (activeKey) {
        headers["Authorization"] = `Bearer ${activeKey}`;
      }
      
      const res = await fetch("/v1/models", { headers });
      if (!res.ok) {
        throw new Error(`Failed to fetch models (${res.status} ${res.statusText})`);
      }
      const data = await res.json();
      const modelList = data.data?.map((m: any) => m.id) || [];
      setModels(modelList);
      if (modelList.length > 0) {
        // preserve selected model if it's still in the list, else choose first
        if (!modelList.includes(selectedModel)) {
          setSelectedModel(modelList[0]);
        }
      } else {
        setSelectedModel("");
      }
    } catch (err: any) {
      console.error(err);
      setError(`Failed to retrieve models from /v1/models: ${err.message}. Ensure at least one provider is enabled.`);
      setModels([]);
    }
  };

  // Fetch models when selectedKey or activeProviderName changes
  useEffect(() => {
    fetchModels();
  }, [selectedKey, activeProviderName, virtualKeys.length]);

  // Report selected key/model up to App for the generated curl command
  useEffect(() => {
    onStateChange?.(selectedKey, selectedModel);
  }, [selectedKey, selectedModel]);

  // Scroll to bottom on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // Adjust selectedKey when virtualKeys change or enableVirtualKey toggles
  useEffect(() => {
    if (!enableVirtualKey) {
      if (selectedKey !== "") setSelectedKey("");
      return;
    }
    // When enableVirtualKey is true:
    // Only reset if selectedKey is non-empty AND that key no longer exists in virtualKeys
    if (selectedKey !== "" && !virtualKeys.some(k => k.key === selectedKey)) {
      setSelectedKey(virtualKeys.length > 0 ? virtualKeys[0].key : "");
    }
  }, [virtualKeys, selectedKey, enableVirtualKey]);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!input.trim() || isLoading) return;

    setError(null);
    const userMessage: Message = { role: "user", content: input };
    const updatedMessages = [...messages, userMessage];
    setMessages(updatedMessages);
    setInput("");
    setIsLoading(true);

    // Add placeholder assistant message
    const assistantMessageIdx = updatedMessages.length;
    setMessages((prev) => [...prev, { role: "assistant", content: "" }]);

    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json"
      };
      const activeKey = selectedKey;
      if (activeKey) {
        headers["Authorization"] = `Bearer ${activeKey}`;
      }

      const body = {
        model: selectedModel || "default-model",
        messages: updatedMessages.filter(m => m.role !== "system"), // strip system if needed, or keep
        stream: stream
      };

      const response = await fetch("/v1/chat/completions", {
        method: "POST",
        headers,
        body: JSON.stringify(body)
      });

      if (!response.ok) {
        const errorText = await response.text();
        let parsedError = errorText;
        try {
          const parsed = JSON.parse(errorText);
          parsedError = parsed.error || errorText;
        } catch {
          // ignore
        }
        throw new Error(`API returned ${response.status}: ${parsedError}`);
      }

      if (stream) {
        if (!response.body) {
          throw new Error("No response body received for stream");
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder("utf-8");
        let buffer = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          // Keep the last incomplete line in buffer
          buffer = lines.pop() || "";

          for (const line of lines) {
            const cleanLine = line.trim();
            if (!cleanLine) continue;
            if (cleanLine === "data: [DONE]") continue;

            if (cleanLine.startsWith("data: ")) {
              try {
                const jsonStr = cleanLine.substring(6);
                const parsed = JSON.parse(jsonStr);
                const token = parsed.choices?.[0]?.delta?.content || "";
                if (token) {
                  setMessages((prev) => {
                    const next = [...prev];
                    next[assistantMessageIdx] = {
                      role: "assistant",
                      content: next[assistantMessageIdx].content + token
                    };
                    return next;
                  });
                }
              } catch (e) {
                // ignore parsing error for partial chunks
              }
            }
          }
        }
      } else {
        const result = await response.json();
        const content = result.choices?.[0]?.message?.content || JSON.stringify(result);
        setMessages((prev) => {
          const next = [...prev];
          next[assistantMessageIdx] = { role: "assistant", content };
          return next;
        });
      }
    } catch (err: any) {
      console.error(err);
      setError(err.message || "Something went wrong during completions request.");
      // Remove placeholder message
      setMessages((prev) => prev.slice(0, -1));
    } finally {
      setIsLoading(false);
    }
  };

  const clearChat = () => {
    setMessages([{ role: "system", content: "You are a helpful assistant talking through the LLM Proxy." }]);
    setError(null);
  };

  return (
    <div className="bg-white border border-neutral-200 rounded-2xl flex flex-col h-[520px] overflow-hidden shadow-sm">
      {/* Playground Header - Mobile Optimized */}
      <div className="bg-neutral-50 px-4 sm:px-5 py-3.5 border-b border-neutral-200">
        {/* Top bar: Title and Stream Toggle */}
        <div className="flex items-center justify-between pb-1">
          <div className="flex items-center space-x-2">
            <Sparkles className="w-5 h-5 text-emerald-500 shrink-0" />
            <h3 className="font-display font-semibold text-neutral-800 text-sm sm:text-base">Interactive Playground</h3>
          </div>
          <div className="flex items-center space-x-3">
            <label className="flex items-center space-x-1.5 cursor-pointer select-none text-neutral-600 bg-white border border-neutral-200 px-2.5 py-1 rounded-lg shadow-2xs hover:border-neutral-300 transition-colors">
              <input
                type="checkbox"
                checked={stream}
                onChange={(e) => setStream(e.target.checked)}
                className="rounded border-neutral-300 text-emerald-600 focus:ring-emerald-500 w-3.5 h-3.5"
              />
              <span className="text-xs font-medium">Stream</span>
            </label>
            <button
              onClick={fetchModels}
              title="Refetch available models"
              className="p-1.5 text-neutral-500 hover:text-neutral-800 hover:bg-neutral-200 rounded-lg transition-colors border border-neutral-200 bg-white shadow-2xs"
            >
              <RefreshCw className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        {/* Control Grid: Virtual Key & Model Selectors (Stacked on mobile, 2 columns on desktop when Virtual Key enabled) */}
        <div className={`grid grid-cols-1 ${enableVirtualKey ? "sm:grid-cols-2" : ""} gap-3 mt-2.5 pt-2.5 border-t border-neutral-200/80 text-xs`}>
          {/* Key selector - Only show when enableVirtualKey is enabled */}
          {enableVirtualKey ? (
            <div className="flex flex-col space-y-1">
              <label className="text-[11px] font-bold text-neutral-500 uppercase tracking-wider flex items-center justify-between">
                <span>Virtual Key (Auth)</span>
                {selectedKey ? (
                  <span className="text-[10px] text-emerald-600 font-semibold lowercase">active</span>
                ) : (
                  <span className="text-[10px] text-amber-600 font-semibold lowercase">none (direct)</span>
                )}
              </label>
              <select
                value={selectedKey}
                onChange={(e) => setSelectedKey(e.target.value)}
                className="w-full bg-white border border-neutral-300 rounded-xl px-3 py-2 sm:py-1.5 text-xs sm:text-sm text-neutral-800 font-medium outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 shadow-2xs transition-all"
              >
                <option value="">None (Direct proxy)</option>
                {virtualKeys.map((k) => (
                  <option key={k.key} value={k.key}>
                    {k.name} ({k.key.substring(0, 10)}...)
                  </option>
                ))}
              </select>
            </div>
          ) : null}

          {/* Model selector */}
          <div className="flex flex-col space-y-1">
            <div className="flex items-center justify-between text-[11px] font-bold text-neutral-500 uppercase tracking-wider">
              <div className="flex items-center space-x-1.5">
                <span>Target Model</span>
                {!enableVirtualKey && (
                  <span className="text-[10px] bg-neutral-200/80 text-neutral-600 px-1.5 py-0.5 rounded font-medium lowercase">direct proxy mode</span>
                )}
              </div>
              <span className="text-neutral-400 lowercase font-normal">{models.length} available</span>
            </div>
            
            {/* Filter input if many models */}
            {models.length > 3 && (
              <div className="relative mb-1">
                <input
                  type="text"
                  placeholder="Filter models (e.g. gpt-4o, claude, flash)..."
                  value={modelKeyword}
                  onChange={(e) => setModelKeyword(e.target.value)}
                  className="w-full bg-white border border-neutral-250 rounded-lg pl-2.5 pr-7 py-1.5 text-xs text-neutral-700 outline-none focus:border-neutral-400 focus:ring-1 focus:ring-neutral-400 placeholder-neutral-400 shadow-2xs transition-all"
                />
                {modelKeyword && (
                  <button
                    onClick={() => setModelKeyword("")}
                    className="absolute right-2 top-1/2 -translate-y-1/2 text-neutral-400 hover:text-neutral-600 p-0.5"
                  >
                    <X className="w-3 h-3" />
                  </button>
                )}
              </div>
            )}

            <select
              value={selectedModel}
              onChange={(e) => setSelectedModel(e.target.value)}
              disabled={models.length === 0}
              className="w-full bg-white border border-neutral-300 rounded-xl px-3 py-2 sm:py-1.5 text-xs sm:text-sm text-neutral-800 font-medium outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 disabled:bg-neutral-100 disabled:text-neutral-400 shadow-2xs transition-all truncate"
            >
              {models.length === 0 ? (
                <option value="">No models available</option>
              ) : filteredModels.length === 0 ? (
                <option value={selectedModel || ""}>{selectedModel ? `${selectedModel} (hidden by filter)` : `No match for "${modelKeyword}"`}</option>
              ) : (
                filteredModels.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))
              )}
            </select>
          </div>
        </div>
      </div>

      {/* Messages area */}
      <div className="flex-1 p-5 overflow-y-auto space-y-4 bg-neutral-50/50">
        {messages.map((m, idx) => {
          if (m.role === "system") return null;
          const isUser = m.role === "user";
          return (
            <div key={idx} className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
              <div
                className={`max-w-[80%] rounded-2xl px-4 py-2.5 text-sm shadow-sm ${
                  isUser
                    ? "bg-neutral-900 text-white rounded-br-none"
                    : "bg-white border border-neutral-200 text-neutral-800 rounded-bl-none"
                }`}
              >
                <div className="text-[10px] uppercase font-bold tracking-wider mb-1 opacity-60">
                  {isUser ? "User" : `Assistant (${selectedModel || "Proxy Active"})`}
                </div>
                <div className="whitespace-pre-wrap leading-relaxed">{m.content || (isLoading && idx === messages.length - 1 ? "Generating..." : "")}</div>
              </div>
            </div>
          );
        })}
        {error && (
          <div className="bg-red-50 text-red-700 border border-red-200 rounded-xl p-3 text-xs flex items-start space-x-2">
            <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* Bottom Form */}
      <form onSubmit={handleSend} className="p-4 border-t border-neutral-200 bg-white flex items-center space-x-2">
        <button
          type="button"
          onClick={clearChat}
          className="px-3 py-2 text-xs text-neutral-500 hover:text-neutral-800 hover:bg-neutral-100 rounded-xl transition-colors font-medium border border-neutral-200"
        >
          Clear Chat
        </button>
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder={models.length === 0 ? "Enable a provider to start typing..." : `Message via ${activeProviderName}...`}
          disabled={models.length === 0 || isLoading}
          className="flex-1 border border-neutral-250 rounded-xl px-4 py-2 text-sm text-neutral-800 placeholder-neutral-400 outline-none focus:border-neutral-500 focus:ring-1 focus:ring-neutral-500 disabled:bg-neutral-100 disabled:cursor-not-allowed"
        />
        <button
          type="submit"
          disabled={models.length === 0 || !input.trim() || isLoading}
          className="bg-neutral-900 text-white p-2.5 rounded-xl hover:bg-neutral-800 disabled:opacity-40 disabled:cursor-not-allowed transition-all duration-150 shadow-xs cursor-pointer"
        >
          <Send className="w-4 h-4" />
        </button>
      </form>
    </div>
  );
}
