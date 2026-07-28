export interface Provider {
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
  enabled: boolean;
  models: string[];
  concurrency?: number;
}

export interface VirtualKey {
  key: string;
  name: string;
  providerIds: string[];
  createdAt: string;
}

export interface SystemLog {
  timestamp: string;
  level: "info" | "warn" | "error";
  category?: "system" | "proxy";
  file?: number;
  message: string;
}

export interface LogStatus {
  activeFile: number;
  file1Size: number;
  file2Size: number;
  maxLogSizeMB: number;
  totalLogs: number;
}

export interface Settings {
  enableVirtualKey: boolean;
  enableAdminAuth: boolean;
  debug: boolean;
  logRequestBody?: boolean;
  logResponseBody?: boolean;
  maxLogSizeMB: number;
  activeLogFile?: number;
}

export interface Message {
  role: "user" | "assistant" | "system";
  content: string;
}
