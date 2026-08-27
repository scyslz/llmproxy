export interface Provider {
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
  enabled: boolean;
  models: string[];
  concurrency?: number;
  chatPath?: string;
  responsesPath?: string;
  defaultModel?: string;
  // protocol 声明该 provider 接受的对话协议；空字符串表示由代理自动探测。
  protocol?: "chat" | "responses";
  // modelProtocols 按 (model) 覆盖 protocol，由 404 探测成功后自动写入。
  modelProtocols?: Record<string, string>;
}

export interface VirtualKey {
  key: string;
  name: string;
  groupId?: string;
  providerIds: string[];
  createdAt: string;
}

export interface GroupEntry {
  providerId: string;
  models: string[];
}

export interface ProviderGroup {
  id: string;
  name: string;
  entries: GroupEntry[];
  createdAt: string;
}

export interface GroupHealthEntry {
  providerId: string;
  model: string;
  available: boolean;
  failCount: number;
  cooldownMs: number;
  remainingMs: number;
}

export interface GroupTestEntry {
  providerId: string;
  model: string;
  ok: boolean;
  status: number;
  durationMs: number;
  error?: string;
}

export interface SystemLog {
  id?: number;
  timestamp: string;
  level: "info" | "warn" | "error";
  category?: "system" | "proxy";
  message: string;
  requestId?: string;
}

export interface LogStatus {
  totalSize: number;
  maxLogSizeMB: number;
  totalLogs: number;
}

export interface RequestLog {
  id: string;
  timestamp: string;
  keyName: string;
  keyId: string;
  model: string;
  provider: string;
  path: string;
  method: string;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  totalTokens: number;
  status: number;
  durationMs: number;
  stream: boolean;
  error?: string;
  requestId?: string;
  hasDetail?: boolean;
}

export interface RequestLogStats {
  count: number;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  totalTokens: number;
}

export interface Settings {
  enableVirtualKey: boolean;
  enableAdminAuth: boolean;
  logDetail: "off" | "basic" | "error" | "all";
  logBody: boolean;
  maxLogSizeMB: number;
  activeLogFile?: number;
}

export interface Message {
  role: "user" | "assistant" | "system";
  content: string;
}
