import express from "express";
import path from "path";
import fs from "fs";
import { createServer as createViteServer } from "vite";
import { DatabaseSync } from "node:sqlite";

interface Provider {
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
  enabled: boolean;
  models: string[];
  concurrency?: number; // 0 or undefined = unlimited
  timeout?: number; // upstream request timeout in ms, 0 or undefined = no timeout
  openaiEndpoint?: string; // optional path (e.g. /chat/completions), preferred over derived /v1 + subpath
  defaultModel?: string; // optional fallback model used when request model is not in the models list
}

class Semaphore {
  private current = 0;
  private queue: (() => void)[] = [];

  constructor(private readonly max: number) {}

  async acquire(): Promise<void> {
    if (this.max <= 0) return;
    if (this.current < this.max) {
      this.current++;
      return;
    }
    return new Promise<void>((resolve) => {
      this.queue.push(() => {
        this.current++;
        resolve();
      });
    });
  }

  release(): void {
    if (this.max <= 0) return;
    this.current--;
    if (this.queue.length > 0) {
      const next = this.queue.shift()!;
      next();
    }
  }

  get available(): number {
    return this.max <= 0 ? Infinity : this.max - this.current;
  }
}

const providerSemaphores = new Map<string, Semaphore>();

function getSemaphore(providerId: string, maxConcurrency: number): Semaphore {
  let sem = providerSemaphores.get(providerId);
  if (!sem) {
    sem = new Semaphore(maxConcurrency);
    providerSemaphores.set(providerId, sem);
  }
  return sem;
}

interface VirtualKey {
  key: string;
  name: string;
  providerIds: string[];
  createdAt: string;
}

interface RequestLog {
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
}

interface Config {
  listen?: string;
  enableVirtualKey: boolean;
  enableAdminAuth: boolean;
  adminPassword?: string;
  debug: boolean;
  logRequestBody?: boolean;
  logResponseBody?: boolean;
  maxLogSizeMB?: number;
  maxRequestLogs?: number;
  activeLogFile?: number;
  providers: Provider[];
  keys: VirtualKey[];
}

const CONFIG_FILE = path.join(process.cwd(), "config", "config.json");

// Default Configuration
const DEFAULT_CONFIG: Config = {
  listen: ":3000",
  enableVirtualKey: false,
  enableAdminAuth: false,
  adminPassword: "admin",
  debug: true,
  logRequestBody: true,
  logResponseBody: false,
  maxLogSizeMB: 2,
  maxRequestLogs: 10000,
  activeLogFile: 1,
  providers: [
    {
      id: "gemini",
      name: "Google Gemini",
      baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
      apiKey: process.env.GEMINI_API_KEY || "",
      enabled: true,
      models: ["gemini-2.5-flash", "gemini-2.5-pro", "gemini-1.5-flash"],
      concurrency: 0,
      timeout: 120000
    },
    {
      id: "openai",
      name: "OpenAI",
      baseUrl: "https://api.openai.com",
      apiKey: "",
      enabled: false,
      models: ["gpt-4o", "gpt-4o-mini", "o1-mini"],
      concurrency: 0,
      timeout: 120000
    },
    {
      id: "deepseek",
      name: "DeepSeek",
      baseUrl: "https://api.deepseek.com",
      apiKey: "",
      enabled: false,
      models: ["deepseek-chat", "deepseek-reasoner"],
      concurrency: 0,
      timeout: 120000
    }
  ],
  keys: [
    {
      key: "sk-proxy-demo-key",
      name: "Demo Virtual Key",
      providerIds: ["gemini", "openai", "deepseek"],
      createdAt: new Date().toISOString()
    }
  ]
};

const LOGS_DIR = path.join(process.cwd(), "logs");
const LOG_FILE_1 = path.join(LOGS_DIR, "proxy-1.log");
const LOG_FILE_2 = path.join(LOGS_DIR, "proxy-2.log");

function ensureLogDir() {
  try {
    if (!fs.existsSync(LOGS_DIR)) {
      fs.mkdirSync(LOGS_DIR, { recursive: true });
    }
    if (!fs.existsSync(LOG_FILE_1)) fs.writeFileSync(LOG_FILE_1, "", "utf-8");
    if (!fs.existsSync(LOG_FILE_2)) fs.writeFileSync(LOG_FILE_2, "", "utf-8");
  } catch (err) {}
}

// Log storage for the live dashboard logs
const systemLogs: Array<{ timestamp: string; level: "info" | "warn" | "error"; category?: "system" | "proxy"; file?: number; message: string }> = [];

function addLog(
  level: "info" | "warn" | "error",
  message: string,
  category: "system" | "proxy" = "system"
) {
  const timestamp = new Date().toISOString();
  let activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
  const logEntry = { timestamp, level, category, file: activeNum, message };

  systemLogs.push(logEntry);
  if (systemLogs.length > 200) {
    systemLogs.shift();
  }
  console.log(`[${level.toUpperCase()}] [${category.toUpperCase()}] ${message}`);

  // Unified size check: rotate BOTH the JSON and SQLite files together when either exceeds the limit
  try {
    ensureLogDir();
    const maxSize = ((typeof cfg !== "undefined" && cfg?.maxLogSizeMB) ? cfg.maxLogSizeMB : 2) * 1024 * 1024;
    const activeJsonPath = activeNum === 1 ? LOG_FILE_1 : LOG_FILE_2;
    let jsonSize = 0;
    if (fs.existsSync(activeJsonPath)) {
      jsonSize = fs.statSync(activeJsonPath).size;
    }
    let dbSize = 0;
    try { dbSize = fs.statSync(getSystemDbPath(activeNum)).size; } catch {}
    if (maxSize > 0 && (jsonSize >= maxSize || dbSize >= maxSize)) {
      rotateLogStorage();
      activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : activeNum;
      logEntry.file = activeNum;
    }
  } catch (err) {}

  // Persistent SQLite dual-file storage (primary read source for the dashboard)
  try {
    if (!sysDb) initSystemLogDb();
    if (sysDb) {
      const stmt = sysLogInsertStmt || (sysLogInsertStmt = sysDb.prepare(
        "INSERT INTO system_logs (time, level, category, file, message) VALUES (?, ?, ?, ?, ?)"
      ));
      stmt.run(new Date(timestamp).getTime(), level, category || null, logEntry.file || null, message);
      const row = sysDb.prepare("SELECT COUNT(*) AS c FROM system_logs").get() as any;
      const count = row ? row.c : 0;
      if (count > SYSTEM_LOG_LIMIT) {
        sysDb.prepare(`
          DELETE FROM system_logs WHERE id IN (
            SELECT id FROM system_logs ORDER BY id DESC LIMIT -1 OFFSET ?
          )
        `).run(SYSTEM_LOG_LIMIT);
      }
    }
  } catch (err) {
    console.error("Failed to write system log to DB:", err);
  }

  // Dual-file JSON disk storage (audit backup)
  try {
    ensureLogDir();
    const activePath = activeNum === 1 ? LOG_FILE_1 : LOG_FILE_2;
    fs.appendFileSync(activePath, JSON.stringify(logEntry) + "\n", "utf-8");
  } catch (err) {}
}

function readDiskLogs(limit?: number): any[] {
  try {
    ensureLogDir();
    const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
    const activePath = activeNum === 1 ? LOG_FILE_1 : LOG_FILE_2;

    const parseLogFile = (filePath: string, fileNum: number) => {
      if (!fs.existsSync(filePath)) return [];
      const content = fs.readFileSync(filePath, "utf-8");
      const lines = content.split("\n").filter(Boolean);
      const tail = limit ? lines.slice(-limit) : lines;
      return tail
        .map(line => {
          try {
            const parsed = JSON.parse(line);
            return { ...parsed, file: fileNum, isActiveFile: fileNum === activeNum };
          } catch {
            return null;
          }
        })
        .filter(Boolean);
    };

    const activeLogs = parseLogFile(activePath, activeNum);
    if (activeLogs.length === 0) {
      const memLogs = systemLogs.filter(log => !log.file || log.file === activeNum);
      return limit && memLogs.length > limit ? memLogs.slice(-limit) : memLogs;
    }
    const sorted = activeLogs.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
    return limit && sorted.length > limit ? sorted.slice(-limit) : sorted;
  } catch {
    const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
    const memLogs = systemLogs.filter(log => !log.file || log.file === activeNum);
    return limit && memLogs.length > limit ? memLogs.slice(-limit) : memLogs;
  }
}

// System logs (SQLite-backed): dual-file round-robin storage mirroring the JSON log files
const SYSTEM_DB_FILE_1 = path.join(LOGS_DIR, "system_logs.db");
const SYSTEM_DB_FILE_2 = path.join(LOGS_DIR, "system_logs-2.db");
const SYSTEM_LOG_LIMIT = 50000;
let sysDb: DatabaseSync | null = null;
let sysLogInsertStmt: any = null;

function getSystemDbPath(num: number): string {
  return num === 2 ? SYSTEM_DB_FILE_2 : SYSTEM_DB_FILE_1;
}

function importDiskLogsToSqlite() {
  if (!sysDb) return;
  const parseFile = (filePath: string, fileNum: number) => {
    if (!fs.existsSync(filePath)) return [];
    const content = fs.readFileSync(filePath, "utf-8");
    return content.split("\n").filter(Boolean).map(line => {
      try { return { ...JSON.parse(line), file: fileNum }; } catch { return null; }
    }).filter(Boolean) as any[];
  };
  const all = [...parseFile(LOG_FILE_1, 1), ...parseFile(LOG_FILE_2, 2)]
    .sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
  const existing = new Set<string>();
  const rows = sysDb.prepare("SELECT time, message FROM system_logs").all() as any[];
  for (const r of rows) existing.add(`${r.time}:${r.message}`);
  const insert = sysDb.prepare(
    "INSERT INTO system_logs (time, level, category, file, message) VALUES (?, ?, ?, ?, ?)"
  );
  let added = 0;
  for (const l of all) {
    const t = new Date(l.timestamp).getTime();
    if (existing.has(`${t}:${l.message}`)) continue;
    try {
      insert.run(t, l.level, l.category || null, l.file || null, l.message);
      added++;
    } catch {}
  }
  if (added > 0) {
    console.log(`[INFO] [SYSTEM] Imported ${added} system log entries from disk.`);
  }
}

function openSystemLogDb(num: number) {
  try {
    ensureLogDir();
    if (sysDb) { try { sysDb.close(); } catch {} }
    sysDb = null;
    sysLogInsertStmt = null;
    sysDb = new DatabaseSync(getSystemDbPath(num));
    sysDb.exec(`
      CREATE TABLE IF NOT EXISTS system_logs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        time INTEGER NOT NULL,
        level TEXT NOT NULL,
        category TEXT,
        file INTEGER,
        message TEXT
      );
      CREATE INDEX IF NOT EXISTS idx_syslog_time ON system_logs(time);
      CREATE INDEX IF NOT EXISTS idx_syslog_level ON system_logs(level);
    `);
    importDiskLogsToSqlite();
    sysDb.prepare(`
      DELETE FROM system_logs WHERE id NOT IN (
        SELECT id FROM system_logs ORDER BY id DESC LIMIT ?
      )
    `).run(SYSTEM_LOG_LIMIT);
  } catch (err: any) {
    console.error("Failed to initialize system log database:", err);
    try { if (sysDb) sysDb.close(); } catch {}
    sysDb = null;
  }
}

function initSystemLogDb() {
  const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
  openSystemLogDb(activeNum);
}

function rotateLogStorage() {
  const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
  const nextNum = activeNum === 1 ? 2 : 1;
  try {
    ensureLogDir();
    // Rotate JSON file
    const nextJsonPath = nextNum === 1 ? LOG_FILE_1 : LOG_FILE_2;
    fs.writeFileSync(nextJsonPath, "", "utf-8");
    // Rotate SQLite file
    if (sysDb) { try { sysDb.close(); } catch {} }
    sysDb = null;
    sysLogInsertStmt = null;
    const nextDbPath = getSystemDbPath(nextNum);
    if (fs.existsSync(nextDbPath)) fs.writeFileSync(nextDbPath, "", "utf-8");
    if (typeof cfg !== "undefined" && cfg) {
      cfg.activeLogFile = nextNum;
      saveConfig(cfg);
    }
    openSystemLogDb(nextNum);
  } catch (err: any) {
    console.error("Failed to rotate log storage:", err);
  }
}

function systemLogRowToApi(row: any) {
  return {
    id: row.id,
    timestamp: new Date(row.time).toISOString(),
    level: row.level,
    category: row.category || "system",
    file: row.file,
    message: row.message
  };
}

// ---------------------------------------------------------------------------
// Request logs (SQLite-backed): captures per-request token usage, key & model
// ---------------------------------------------------------------------------
const REQUEST_DB_FILE = path.join(LOGS_DIR, "requests.db");
let db: DatabaseSync | null = null;
let requestLogsCount = 0;

function initRequestLogDb() {
  try {
    ensureLogDir();
    db = new DatabaseSync(REQUEST_DB_FILE);
    db.exec(`
      CREATE TABLE IF NOT EXISTS request_logs (
        id TEXT PRIMARY KEY,
        time INTEGER NOT NULL,
        key_name TEXT,
        key_id TEXT,
        model TEXT,
        provider TEXT,
        path TEXT,
        method TEXT,
        prompt_tokens INTEGER NOT NULL DEFAULT 0,
        completion_tokens INTEGER NOT NULL DEFAULT 0,
        cached_tokens INTEGER NOT NULL DEFAULT 0,
        total_tokens INTEGER NOT NULL DEFAULT 0,
        status INTEGER NOT NULL DEFAULT 0,
        duration_ms INTEGER NOT NULL DEFAULT 0,
        stream INTEGER NOT NULL DEFAULT 0,
        error TEXT
      );
      CREATE INDEX IF NOT EXISTS idx_req_time ON request_logs(time);
      CREATE INDEX IF NOT EXISTS idx_req_key ON request_logs(key_name);
      CREATE INDEX IF NOT EXISTS idx_req_model ON request_logs(model);
    `);
    const row = db.prepare("SELECT COUNT(*) AS c FROM request_logs").get() as any;
    requestLogsCount = row ? row.c : 0;
    addLog("info", `Request log database ready at ${REQUEST_DB_FILE} (${requestLogsCount} existing entries).`);
  } catch (err: any) {
    console.error("Failed to initialize request log database:", err);
    db = null;
  }
}

function maskKey(key: string): string {
  if (!key) return "";
  if (key.length <= 8) return key;
  return `${key.substring(0, 8)}****${key.substring(key.length - 4)}`;
}

function addRequestLog(log: RequestLog) {
  if (!db) return;
  try {
    const insert = db.prepare(`
      INSERT INTO request_logs
        (id, time, key_name, key_id, model, provider, path, method,
         prompt_tokens, completion_tokens, cached_tokens, total_tokens,
         status, duration_ms, stream, error)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `);
    insert.run(
      log.id,
      new Date(log.timestamp).getTime(),
      log.keyName || null,
      log.keyId || null,
      log.model || null,
      log.provider || null,
      log.path || null,
      log.method || null,
      log.promptTokens || 0,
      log.completionTokens || 0,
      log.cachedTokens || 0,
      log.totalTokens || 0,
      log.status || 0,
      log.durationMs || 0,
      log.stream ? 1 : 0,
      log.error || null
    );
    requestLogsCount++;
    const max = (typeof cfg !== "undefined" && cfg?.maxRequestLogs) ? cfg.maxRequestLogs : 10000;
    if (requestLogsCount > max) {
      const excess = requestLogsCount - max;
      db.prepare("DELETE FROM request_logs WHERE id IN (SELECT id FROM request_logs ORDER BY time ASC LIMIT ?)").run(excess);
      requestLogsCount = max;
    }
  } catch (err) {
    console.error("Failed to write request log:", err);
  }
}

function dbRowToRequestLog(row: any): RequestLog {
  return {
    id: row.id,
    timestamp: new Date(row.time).toISOString(),
    keyName: row.key_name || "",
    keyId: row.key_id || "",
    model: row.model || "",
    provider: row.provider || "",
    path: row.path || "",
    method: row.method || "",
    promptTokens: row.prompt_tokens || 0,
    completionTokens: row.completion_tokens || 0,
    cachedTokens: row.cached_tokens || 0,
    totalTokens: row.total_tokens || 0,
    status: row.status || 0,
    durationMs: row.duration_ms || 0,
    stream: row.stream === 1,
    error: row.error || undefined
  };
}

// Incremental SSE parser that captures model + usage from streaming chunks
class SseUsageParser {
  private buffer = "";
  usage: { promptTokens: number; completionTokens: number; cachedTokens: number } | null = null;
  model = "";

  push(text: string) {
    this.buffer += text;
    let idx: number;
    while ((idx = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.substring(0, idx).trim();
      this.buffer = this.buffer.substring(idx + 1);
      if (!line.startsWith("data:")) continue;
      const data = line.substring(5).trim();
      if (!data || data === "[DONE]") continue;
      try {
        const obj = JSON.parse(data);
        if (obj.model) this.model = obj.model;
        if (obj.usage) {
          this.usage = {
            promptTokens: obj.usage.prompt_tokens ?? 0,
            completionTokens: obj.usage.completion_tokens ?? 0,
            cachedTokens: obj.usage.prompt_tokens_details?.cached_tokens ?? 0
          };
        }
      } catch {}
    }
  }
}

// In-memory admin session storage
const adminSessions = new Set<string>();

// Brute-force protection: track failed login attempts
const failedLoginAttempts: number[] = [];
const MAX_FAILED_ATTEMPTS = 5;
const FAILED_WINDOW_MS = 10 * 60 * 1000; // 10 minutes

// Helper function to extract API key or token from Authorization header
function extractApiKey(authHeader?: string): string {
  if (!authHeader) return "";
  if (authHeader.startsWith("Bearer ")) {
    return authHeader.substring(7).trim();
  }
  return authHeader.trim();
}

// Load configuration
function loadConfig(): Config {
  try {
    if (fs.existsSync(CONFIG_FILE)) {
      const data = fs.readFileSync(CONFIG_FILE, "utf-8");
      const parsed = JSON.parse(data);
      // Backwards/forwards compatibility with Go casing if needed, but let's normalize to camelCase
      const config = { ...DEFAULT_CONFIG, ...parsed };
      if (typeof config.enableAdminAuth !== "boolean") {
        config.enableAdminAuth = false;
      }
      if (!config.adminPassword) {
        config.adminPassword = "admin";
      }
      // Ensure API keys are loaded if not set in file but present in env
      config.providers = config.providers.map((p: any) => {
        // Map snake_case to camelCase if coming from Go or elsewhere
        const mapped: Provider = {
          id: p.id || p.ID || "",
          name: p.name || p.Name || "",
          baseUrl: p.baseUrl || p.base_url || p.BaseURL || "",
          apiKey: p.apiKey || p.api_key || p.APIKey || "",
          enabled: typeof p.enabled === "boolean" ? p.enabled : p.Enabled || false,
          models: p.models || p.Models || [],
          concurrency: typeof p.concurrency === "number" ? p.concurrency : 0,
          openaiEndpoint: p.openaiEndpoint || p.openai_endpoint || "",
          defaultModel: p.defaultModel || p.default_model || ""
        };
        if (mapped.id === "gemini" && !mapped.apiKey && process.env.GEMINI_API_KEY) {
          mapped.apiKey = process.env.GEMINI_API_KEY;
        }
        return mapped;
      });
      return config;
    }
  } catch (error) {
    console.error("Failed to load config.json, using defaults", error);
  }
  return { ...DEFAULT_CONFIG };
}

// Save configuration
function saveConfig(config: Config) {
  try {
    fs.writeFileSync(CONFIG_FILE, JSON.stringify(config, null, 2), "utf-8");
  } catch (error) {
    console.error("Failed to save config.json", error);
  }
}

let cfg = loadConfig();

async function startServer() {
  const app = express();
  initRequestLogDb();
  initSystemLogDb();

  let host = "0.0.0.0";
  let port = 3000;
  if (cfg.listen) {
    const parts = cfg.listen.split(":");
    if (parts.length === 2) {
      host = parts[0] || "0.0.0.0";
      port = parseInt(parts[1], 10) || port;
    } else if (parts.length === 1) {
      port = parseInt(parts[0], 10) || port;
    }
  }

  // Middleware
  app.use(express.json({ limit: "10mb" }));
  app.use((req, res, next) => {
    // Add CORS headers for testing
    res.setHeader("Access-Control-Allow-Origin", "*");
    res.setHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS");
    res.setHeader("Access-Control-Allow-Headers", "Content-Type, Authorization");
    if (req.method === "OPTIONS") {
      res.sendStatus(200);
    } else {
      next();
    }
  });

  // 0. Admin Authentication & Gate Verification
  app.post("/api/admin/login", (req, res) => {
    const { password } = req.body;
    if (!cfg.enableAdminAuth) {
      return res.json({ success: true, token: "no-auth-required", enableAdminAuth: false });
    }

    // Brute-force protection: check failed attempts within window
    const now = Date.now();
    while (failedLoginAttempts.length > 0 && failedLoginAttempts[0] < now - FAILED_WINDOW_MS) {
      failedLoginAttempts.shift();
    }
    if (failedLoginAttempts.length >= MAX_FAILED_ATTEMPTS) {
      addLog("warn", `Login blocked: too many failed attempts (${MAX_FAILED_ATTEMPTS} in 10 minutes). Restart required.`);
      return res.status(429).json({ error: "Too many failed login attempts. Service restart required." });
    }

    const targetPassword = cfg.adminPassword || "admin";
    if (password === targetPassword) {
      failedLoginAttempts.length = 0;
      const token = "admin_sk_" + Math.random().toString(36).substring(2, 15) + Date.now().toString(36);
      adminSessions.add(token);
      addLog("info", "Admin login successful.");
      return res.json({ success: true, token, enableAdminAuth: true });
    }
    failedLoginAttempts.push(now);
    addLog("warn", `Failed admin login attempt (${failedLoginAttempts.length}/${MAX_FAILED_ATTEMPTS} in 10 minutes).`);
    return res.status(401).json({ error: "Invalid admin password" });
  });

  app.post("/api/admin/logout", (req, res) => {
    const token = (req.headers["x-admin-token"] as string) || extractApiKey(req.headers.authorization);
    if (token) {
      adminSessions.delete(token);
    }
    addLog("info", "Admin logged out.");
    res.json({ success: true });
  });

  app.get("/api/admin/status", (req, res) => {
    const token = (req.headers["x-admin-token"] as string) || extractApiKey(req.headers.authorization);
    const isAuthenticated = !cfg.enableAdminAuth || adminSessions.has(token);
    res.json({
      enableAdminAuth: cfg.enableAdminAuth,
      isAuthenticated
    });
  });

  // Middleware to verify Admin Authentication for management interfaces (/api/*)
  // Notice: Proxy interfaces (/v1/*) are NOT intercepted here; they are managed by API keys/Virtual keys.
  app.use("/api", (req, res, next) => {
    if (req.path === "/admin/login" || req.path === "/admin/status" || req.path === "/admin/logout") {
      return next();
    }
    if (!cfg.enableAdminAuth) {
      return next();
    }
    const token = (req.headers["x-admin-token"] as string) || extractApiKey(req.headers.authorization);
    if (!token || !adminSessions.has(token)) {
      return res.status(401).json({ error: "Unauthorized: Admin authentication required for management API", requireLogin: true });
    }
    next();
  });

  // 1. Providers API
  app.get("/api/providers", (req, res) => {
    res.json(cfg.providers);
  });

  app.post("/api/providers", (req, res) => {
    const p = req.body;
    if (!p.id || !p.name || !p.baseUrl) {
      return res.status(400).json({ error: "Missing required fields (id, name, baseUrl)" });
    }
    const newProvider: Provider = {
      id: p.id,
      name: p.name,
      baseUrl: p.baseUrl,
      apiKey: p.apiKey || "",
      enabled: false,
      models: p.models || [],
      concurrency: typeof p.concurrency === "number" ? p.concurrency : 0,
      openaiEndpoint: p.openaiEndpoint || ""
    };
    cfg.providers.push(newProvider);
    saveConfig(cfg);
    addLog("info", `Created provider: ${newProvider.name} (${newProvider.id})`);
    res.status(201).json(newProvider);
  });

  app.get("/api/providers/:id", (req, res) => {
    const provider = cfg.providers.find(p => p.id === req.params.id);
    if (!provider) {
      return res.status(404).json({ error: "Provider not found" });
    }
    res.json(provider);
  });

  app.put("/api/providers/:id", (req, res) => {
    const idx = cfg.providers.findIndex(p => p.id === req.params.id);
    if (idx === -1) {
      return res.status(404).json({ error: "Provider not found" });
    }
    const updated = {
      ...cfg.providers[idx],
      ...req.body,
      id: req.params.id, // prevent id change
      enabled: cfg.providers[idx].enabled // preserve enabled status
    };
    cfg.providers[idx] = updated;
    saveConfig(cfg);
    addLog("info", `Updated provider: ${updated.name} (${updated.id})`);
    res.json(updated);
  });

  app.delete("/api/providers/:id", (req, res) => {
    const idx = cfg.providers.findIndex(p => p.id === req.params.id);
    if (idx === -1) {
      return res.status(404).json({ error: "Provider not found" });
    }
    const removed = cfg.providers.splice(idx, 1)[0];
    saveConfig(cfg);
    addLog("info", `Deleted provider: ${removed.name} (${removed.id})`);
    res.json({ deleted: true });
  });

  app.post("/api/providers/:id/enable", (req, res) => {
    const id = req.params.id;
    const found = cfg.providers.find(p => p.id === id);
    if (!found) {
      return res.status(404).json({ error: "Provider not found" });
    }
    cfg.providers.forEach(p => {
      p.enabled = (p.id === id);
    });
    saveConfig(cfg);
    addLog("info", `Enabled provider: ${found.name} (${found.id}), others disabled.`);
    res.json({ enabled: id });
  });

  app.post("/api/providers/fetch-remote-models", async (req, res) => {
    const { id, baseUrl, apiKey: bodyApiKey } = req.body;
    if (!baseUrl) {
      return res.status(400).json({ error: "Base URL is required to fetch models" });
    }

    let apiKey = bodyApiKey || "";
    if (!apiKey && id) {
      const existing = cfg.providers.find(p => p.id === id);
      if (existing && existing.apiKey) {
        apiKey = existing.apiKey;
      }
    }
    if (!apiKey && (id === "gemini" || baseUrl.includes("googleapis.com"))) {
      apiKey = process.env.GEMINI_API_KEY || "";
    }

    try {
      const cleanUrl = baseUrl.trim().replace(/\/+$/, "");
      let targetUrl = `${cleanUrl}/models`;
      
      // Handle Google Gemini specific endpoint
      if (id === "gemini" || cleanUrl.includes("googleapis.com")) {
        targetUrl = `https://generativelanguage.googleapis.com/v1beta/models?key=${apiKey}`;
      } else if (!cleanUrl.endsWith("/v1") && !cleanUrl.endsWith("/api") && !cleanUrl.endsWith("/models") && !cleanUrl.endsWith("/openai")) {
        targetUrl = `${cleanUrl}/v1/models`;
      }

      const headers: Record<string, string> = {
        "Accept": "application/json"
      };
      if (apiKey && !targetUrl.includes("key=")) {
        headers["Authorization"] = `Bearer ${apiKey}`;
      }

      addLog("info", `Fetching models from remote upstream: ${targetUrl}`);
      let response = await fetch(targetUrl, { headers });
      
      if (!response.ok && targetUrl.endsWith("/v1/models")) {
        const fallbackUrl = `${cleanUrl}/models`;
        addLog("info", `Fallback fetching models from: ${fallbackUrl}`);
        const fallbackRes = await fetch(fallbackUrl, { headers });
        if (fallbackRes.ok) {
          response = fallbackRes;
          targetUrl = fallbackUrl;
        }
      } else if (!response.ok && targetUrl.endsWith("/models") && !targetUrl.includes("googleapis.com") && !cleanUrl.endsWith("/v1")) {
        const fallbackUrl = `${cleanUrl}/v1/models`;
        addLog("info", `Fallback fetching models from: ${fallbackUrl}`);
        const fallbackRes = await fetch(fallbackUrl, { headers });
        if (fallbackRes.ok) {
          response = fallbackRes;
          targetUrl = fallbackUrl;
        }
      }

      if (!response.ok) {
        const errText = await response.text().catch(() => "");
        throw new Error(`Upstream API error: ${response.status} ${response.statusText}${errText ? ` - ${errText.slice(0, 100)}` : ""}`);
      }

      const data = await response.json();
      let modelList: string[] = [];

      if (Array.isArray(data.data)) {
        modelList = data.data
          .map((m: any) => typeof m === "string" ? m : m.id || m.name)
          .filter(Boolean);
      } else if (Array.isArray(data.models)) {
        modelList = data.models
          .map((m: any) => {
            const name = typeof m === "string" ? m : m.name || m.id;
            return name ? name.replace(/^models\//, "") : null;
          })
          .filter(Boolean);
      } else if (Array.isArray(data)) {
        modelList = data
          .map((m: any) => typeof m === "string" ? m : m.id || m.name)
          .filter(Boolean);
      } else if (data && typeof data === "object") {
        const keys = Object.keys(data);
        for (const k of keys) {
          if (Array.isArray(data[k])) {
            modelList = data[k]
              .map((m: any) => typeof m === "string" ? m : m.id || m.name)
              .filter(Boolean);
            if (modelList.length > 0) break;
          }
        }
      }

      modelList = Array.from(new Set(modelList)).sort();

      if (modelList.length === 0) {
        return res.status(200).json({
          models: [],
          warning: "API reachable but returned no recognized model IDs in response.",
          raw: typeof data === "object" ? Object.keys(data) : null
        });
      }

      addLog("info", `Successfully fetched ${modelList.length} models from ${targetUrl}`);
      res.json({
        models: modelList,
        count: modelList.length,
        url: targetUrl
      });
    } catch (err: any) {
      addLog("error", `Failed to fetch models: ${err.message}`);
      res.status(500).json({ error: err.message });
    }
  });

  // 2. Global Settings & Virtual Keys API
  app.get("/api/settings", (req, res) => {
    res.json({
      enableVirtualKey: cfg.enableVirtualKey,
      enableAdminAuth: cfg.enableAdminAuth,
      debug: cfg.debug,
      logRequestBody: cfg.logRequestBody !== undefined ? cfg.logRequestBody : true,
      logResponseBody: cfg.logResponseBody === true,
      maxLogSizeMB: cfg.maxLogSizeMB || 2,
      maxRequestLogs: cfg.maxRequestLogs || 10000,
      activeLogFile: cfg.activeLogFile || 1
    });
  });

  app.put("/api/settings", (req, res) => {
    if (typeof req.body.enableVirtualKey === "boolean") {
      cfg.enableVirtualKey = req.body.enableVirtualKey;
    }
    if (typeof req.body.enableAdminAuth === "boolean") {
      const prev = cfg.enableAdminAuth;
      cfg.enableAdminAuth = req.body.enableAdminAuth;
      if (prev !== cfg.enableAdminAuth) {
        adminSessions.clear();
      }
    }
    if (typeof req.body.adminPassword === "string" && req.body.adminPassword.trim() !== "") {
      cfg.adminPassword = req.body.adminPassword.trim();
      adminSessions.clear();
      addLog("info", "Admin password updated. All existing sessions cleared.", "system");
    }
    if (typeof req.body.debug === "boolean") {
      cfg.debug = req.body.debug;
    }
    if (typeof req.body.logRequestBody === "boolean") {
      cfg.logRequestBody = req.body.logRequestBody;
    }
    if (typeof req.body.logResponseBody === "boolean") {
      cfg.logResponseBody = req.body.logResponseBody;
    }
    if (typeof req.body.maxLogSizeMB === "number" && req.body.maxLogSizeMB > 0) {
      cfg.maxLogSizeMB = req.body.maxLogSizeMB;
    }
    if (typeof req.body.maxRequestLogs === "number" && req.body.maxRequestLogs > 0) {
      cfg.maxRequestLogs = req.body.maxRequestLogs;
    }
    saveConfig(cfg);
    addLog("info", `Settings updated: enableVirtualKey=${cfg.enableVirtualKey}, enableAdminAuth=${cfg.enableAdminAuth}, debug=${cfg.debug}, logRequestBody=${cfg.logRequestBody}, logResponseBody=${cfg.logResponseBody}, maxLogSizeMB=${cfg.maxLogSizeMB}`, "system");
    res.json({
      enableVirtualKey: cfg.enableVirtualKey,
      enableAdminAuth: cfg.enableAdminAuth,
      debug: cfg.debug,
      logRequestBody: cfg.logRequestBody !== undefined ? cfg.logRequestBody : true,
      logResponseBody: cfg.logResponseBody === true,
      maxLogSizeMB: cfg.maxLogSizeMB || 2,
      maxRequestLogs: cfg.maxRequestLogs || 10000,
      activeLogFile: cfg.activeLogFile || 1
    });
  });

  app.get("/api/keys", (req, res) => {
    res.json(cfg.keys || []);
  });

  app.post("/api/keys", (req, res) => {
    const { name, providerIds } = req.body;
    if (!name) {
      return res.status(400).json({ error: "Key name is required" });
    }
    const randomHex = Array.from({ length: 16 }, () => Math.floor(Math.random() * 16).toString(16)).join("");
    const newKey: VirtualKey = {
      key: `sk-proxy-${randomHex}`,
      name,
      providerIds: providerIds || [],
      createdAt: new Date().toISOString()
    };
    cfg.keys = cfg.keys || [];
    cfg.keys.push(newKey);
    saveConfig(cfg);
    addLog("info", `Created virtual key: ${name} (${newKey.key.substring(0, 12)}...)`, "system");
    res.status(201).json(newKey);
  });

  app.delete("/api/keys/:key", (req, res) => {
    cfg.keys = cfg.keys || [];
    const idx = cfg.keys.findIndex(k => k.key === req.params.key);
    if (idx === -1) {
      return res.status(404).json({ error: "Virtual key not found" });
    }
    const removed = cfg.keys.splice(idx, 1)[0];
    saveConfig(cfg);
    addLog("info", `Deleted virtual key: ${removed.name}`, "system");
    res.json({ deleted: true });
  });

  // 3. System Logs API (for Live dashboard logs)
  app.get("/api/logs", (req, res) => {
    const q = req.query as any;
    const limit = Math.min(parseInt(q.limit, 10) || 15, 500);
    const clauses: string[] = [];
    const params: any[] = [];
    const sinceId = parseInt(q.since, 10);
    if (!Number.isNaN(sinceId) && sinceId > 0) { clauses.push("id > ?"); params.push(sinceId); }
    if (q.level) { clauses.push("level = ?"); params.push(q.level); }
    if (q.category) { clauses.push("category = ?"); params.push(q.category); }

    if (sysDb) {
      try {
        const where = clauses.length > 0 ? " WHERE " + clauses.join(" AND ") : "";
        const totalRow = sysDb.prepare(`SELECT COUNT(*) AS c FROM system_logs${where}`).get(...params) as any;
        const total = totalRow ? totalRow.c : 0;
        let rows: any[];
        if (!Number.isNaN(sinceId) && sinceId > 0) {
          rows = sysDb.prepare(`SELECT * FROM system_logs${where} ORDER BY id ASC LIMIT ?`).all(...params, limit) as any[];
        } else {
          rows = (sysDb.prepare(`SELECT * FROM system_logs${where} ORDER BY id DESC LIMIT ?`).all(...params, limit) as any[]).reverse();
        }
        res.json({ logs: rows.map(systemLogRowToApi), total });
        return;
      } catch (err: any) {
        // fall through to disk fallback on error
      }
    }

    // Fallback: read from disk/memory
    const activeNum = cfg.activeLogFile || 1;
    const diskLogs = readDiskLogs(limit);
    const allLogs = diskLogs.length > 0 ? diskLogs : systemLogs.filter(log => !log.file || log.file === activeNum);
    let filtered = allLogs;
    if (q.level) filtered = filtered.filter(l => l.level === q.level);
    if (q.category) filtered = filtered.filter(l => (l.category || "system") === q.category);
    res.json({ logs: filtered, total: filtered.length });
  });

  app.get("/api/logs/status", (req, res) => {
    try {
      ensureLogDir();
      let s1 = 0;
      let s2 = 0;
      if (fs.existsSync(LOG_FILE_1)) s1 = fs.statSync(LOG_FILE_1).size;
      if (fs.existsSync(LOG_FILE_2)) s2 = fs.statSync(LOG_FILE_2).size;
      const activeNum = cfg.activeLogFile || 1;
      let totalLogs = systemLogs.length;
      if (sysDb) {
        try {
          const row = sysDb.prepare("SELECT COUNT(*) AS c FROM system_logs").get() as any;
          totalLogs = row ? row.c : 0;
        } catch {}
      }
      res.json({
        activeFile: activeNum,
        file1Size: s1,
        file2Size: s2,
        maxLogSizeMB: cfg.maxLogSizeMB || 2,
        totalLogs
      });
    } catch {
      res.json({
        activeFile: cfg.activeLogFile || 1,
        file1Size: 0,
        file2Size: 0,
        maxLogSizeMB: cfg.maxLogSizeMB || 2,
        totalLogs: systemLogs.length
      });
    }
  });

  app.post("/api/logs/clear", (req, res) => {
    systemLogs.length = 0;
    try {
      ensureLogDir();
      fs.writeFileSync(LOG_FILE_1, "", "utf-8");
      fs.writeFileSync(LOG_FILE_2, "", "utf-8");
      if (sysDb) {
        sysDb.prepare("DELETE FROM system_logs").run();
        sysDb.prepare("DELETE FROM sqlite_sequence WHERE name = 'system_logs'").run();
      }
      const activeNum = cfg.activeLogFile || 1;
      const standbyPath = getSystemDbPath(activeNum === 1 ? 2 : 1);
      if (fs.existsSync(standbyPath)) fs.writeFileSync(standbyPath, "", "utf-8");
    } catch {}
    addLog("info", "All system and proxy logs cleared.", "system");
    res.json({ success: true });
  });

  app.post("/api/logs/rotate", (req, res) => {
    const nextFileNum = (cfg.activeLogFile || 1) === 1 ? 2 : 1;
    cfg.activeLogFile = nextFileNum;
    try {
      ensureLogDir();
      const nextPath = nextFileNum === 1 ? LOG_FILE_1 : LOG_FILE_2;
      fs.writeFileSync(nextPath, "", "utf-8");
    } catch {}
    saveConfig(cfg);
    addLog("info", `Manual log rotation triggered. Active log file switched to proxy-${nextFileNum}.log`, "system");
    res.json({ success: true, activeFile: nextFileNum });
  });

  // Request logs API (SQLite-backed usage tracking)
  const buildRequestLogFilters = (q: any) => {
    const clauses: string[] = [];
    const params: any[] = [];
    if (q.key) { clauses.push("key_name LIKE ?"); params.push(`%${q.key}%`); }
    if (q.model) { clauses.push("model LIKE ?"); params.push(`%${q.model}%`); }
    if (q.provider) { clauses.push("provider = ?"); params.push(q.provider); }
    if (q.from) { clauses.push("time >= ?"); params.push(new Date(q.from).getTime()); }
    if (q.to) { clauses.push("time <= ?"); params.push(new Date(q.to).getTime()); }
    return { clauses, params };
  };

  app.get("/api/request-logs", (req, res) => {
    if (!db) return res.json([]);
    const q = req.query as any;
    const { clauses, params } = buildRequestLogFilters(q);
    let sql = "SELECT * FROM request_logs";
    if (clauses.length > 0) sql += " WHERE " + clauses.join(" AND ");
    const limit = Math.min(parseInt(q.limit, 10) || 500, 1000);
    const offset = Math.max(parseInt(q.offset, 10) || 0, 0);
    sql += " ORDER BY time DESC LIMIT ? OFFSET ?";
    params.push(limit, offset);
    try {
      const rows = db.prepare(sql).all(...params) as any[];
      res.json(rows.map(dbRowToRequestLog));
    } catch (err: any) {
      res.status(500).json({ error: err.message });
    }
  });

  app.get("/api/request-logs/stats", (req, res) => {
    if (!db) return res.json({ count: 0, promptTokens: 0, completionTokens: 0, cachedTokens: 0, totalTokens: 0 });
    const q = req.query as any;
    const { clauses, params } = buildRequestLogFilters(q);
    let sql = `
      SELECT COUNT(*) AS count,
             COALESCE(SUM(prompt_tokens), 0) AS promptTokens,
             COALESCE(SUM(completion_tokens), 0) AS completionTokens,
             COALESCE(SUM(cached_tokens), 0) AS cachedTokens,
             COALESCE(SUM(total_tokens), 0) AS totalTokens
      FROM request_logs`;
    if (clauses.length > 0) sql += " WHERE " + clauses.join(" AND ");
    try {
      const row = db.prepare(sql).get(...params) as any;
      res.json(row);
    } catch (err: any) {
      res.status(500).json({ error: err.message });
    }
  });

  app.post("/api/request-logs/clear", (req, res) => {
    try {
      if (db) {
        db.prepare("DELETE FROM request_logs").run();
        requestLogsCount = 0;
      }
    } catch {}
    addLog("info", "All request usage logs cleared.", "system");
    res.json({ success: true });
  });

  // 4. Models handler (/v1/models)
  app.get("/v1/models", (req, res) => {
    let allowedProviders = cfg.providers;

    const authHeader = req.headers.authorization;
    const apiKey = extractApiKey(authHeader);
    const vKey = apiKey ? cfg.keys.find(k => k.key === apiKey) : undefined;

    if (cfg.enableVirtualKey) {
      if (cfg.keys.length === 0) {
        addLog("warn", `Virtual key mode enabled but no keys configured. Proceeding with default model list.`, "proxy");
      } else if (!vKey) {
        addLog("warn", `Unauthorized /v1/models request. Invalid or missing key.`, "proxy");
        return res.status(401).json({ error: "Unauthorized: Invalid virtual key" });
      }
    }

    // Apply virtual key provider permissions if a valid virtual key is used
    if (vKey) {
      if (vKey.providerIds && vKey.providerIds.length > 0 && !vKey.providerIds.includes("all") && !vKey.providerIds.includes("*")) {
        allowedProviders = cfg.providers.filter(p => vKey.providerIds.includes(p.id));
      }
    }

    const enabledModels = allowedProviders
      .filter(p => p.enabled)
      .flatMap(p => p.models);

    const uniqueModels = Array.from(new Set(enabledModels));

    res.json({
      object: "list",
      data: uniqueModels.map(m => ({ id: m, object: "model", created: Date.now(), owned_by: "proxy" }))
    });
  });

  // 5. General Forward Handler (Post/Get/Put proxy to selected Provider)
  // Standard OpenAI paths like `/v1/chat/completions` or `/v1/embeddings`
  app.all("/v1/*", async (req, res) => {
    const startTime = Date.now();
    const originalPath = req.url;
    const method = req.method;

    addLog("info", `[API Proxy] ${method} ${originalPath} initiated`, "proxy");

    // Determine target providers allowed by Virtual Key
    let allowedProviders = cfg.providers;
    let virtualKeyUsed = "";

    const authHeader = req.headers.authorization;
    const apiKey = extractApiKey(authHeader);
    const vKey = apiKey ? cfg.keys.find(k => k.key === apiKey) : undefined;

    if (cfg.enableVirtualKey) {
      if (cfg.keys.length === 0) {
        addLog("warn", `Virtual key mode enabled but no keys configured. Proceeding with default routing.`, "proxy");
      } else if (!vKey) {
        addLog("error", `Virtual key validation failed for request to ${originalPath}`, "proxy");
        return res.status(401).json({ error: "Unauthorized: Invalid or missing virtual key" });
      }
    }

    if (vKey) {
      virtualKeyUsed = vKey.name;
      if (vKey.providerIds && vKey.providerIds.length > 0 && !vKey.providerIds.includes("all") && !vKey.providerIds.includes("*")) {
        allowedProviders = cfg.providers.filter(p => vKey.providerIds.includes(p.id));
      }
    }

    // Select active provider
    const provider = allowedProviders.find(p => p.enabled);
    if (!provider) {
      addLog("error", `No active provider enabled or allowed for request to ${originalPath}`, "proxy");
      return res.status(503).json({ error: "Service Unavailable: No provider enabled/authorized" });
    }

    // Prepare body & resolve model
    let reqBody = req.body;
    let reqModel = "";

    if (method === "POST" || method === "PUT") {
      reqModel = reqBody?.model || "";

      // Model resolution check
      let modelOK = provider.models.includes(reqModel);
      if (!modelOK && provider.models.length > 0) {
        const fallbackModel = provider.defaultModel || provider.models[0];
        addLog(
          "warn",
          `Model '${reqModel}' not supported by provider '${provider.name}'. Substituting with fallback '${fallbackModel}'.`,
          "proxy"
        );
        reqBody = { ...reqBody, model: fallbackModel };
        reqModel = fallbackModel;
      }
    }

    // Build downstream request URL safely
    const cleanBaseUrl = provider.baseUrl.trim().replace(/\/+$/, "");
    let endpointBase = cleanBaseUrl;
    if (
      !cleanBaseUrl.endsWith("/v1") &&
      !cleanBaseUrl.endsWith("/openai") &&
      !cleanBaseUrl.endsWith("/v1beta") &&
      !cleanBaseUrl.endsWith("/api") &&
      !cleanBaseUrl.endsWith("/v4") &&
      !cleanBaseUrl.endsWith("/v2") &&
      !cleanBaseUrl.endsWith("/v3")
    ) {
      endpointBase = `${cleanBaseUrl}/v1`;
    }

    const idx = originalPath.indexOf("/v1");
    const subPath = idx >= 0 ? originalPath.substring(idx + 3) : originalPath;
    const cleanSubPath = subPath.startsWith("/") ? subPath : `/${subPath}`;

    // Prefer the configured OpenAI endpoint path (e.g. /chat/completions) when set
    const configuredEp = provider.openaiEndpoint?.trim();
    const targetUrl = configuredEp
      ? `${cleanBaseUrl}${configuredEp.startsWith("/") ? configuredEp : `/${configuredEp}`}`
      : `${endpointBase}${cleanSubPath}`;

    addLog(
      "info",
      `[API Proxy Forward] ${method} ${originalPath} -> ${provider.name} (${reqModel || "default"})${virtualKeyUsed ? ` [Key: ${virtualKeyUsed}]` : ""}`,
      "proxy"
    );

    const shouldLogReq = cfg.logRequestBody !== undefined ? cfg.logRequestBody : cfg.debug;
    if (cfg.debug) {
      const reqHeaders = Object.entries(req.headers)
        .map(([k, v]) => `${k}: ${k.toLowerCase() === "authorization" ? "[redacted]" : v}`)
        .join(" | ");
      addLog("info", `[API Proxy Request Headers] ${reqHeaders}`, "proxy");
    }
    if (shouldLogReq && (method === "POST" || method === "PUT")) {
      addLog("info", `[API Proxy Request Body] ${JSON.stringify(reqBody)}`, "proxy");
    }

    const concurrencyLimit = provider.concurrency || 0;
    const sem = concurrencyLimit > 0 ? getSemaphore(provider.id, concurrencyLimit) : null;

    if (sem) {
      await sem.acquire();
    }

    const controller = new AbortController();
    let abortReason: "timeout" | "client_close" | null = null;
    const timeoutMs = provider.timeout || 0;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    try {
      // Setup headers
      const headers = new Headers();
      headers.set("Content-Type", "application/json");
      if (provider.apiKey) {
        headers.set("Authorization", `Bearer ${provider.apiKey}`);
      }

      timeoutId = timeoutMs > 0 ? setTimeout(() => {
        abortReason = "timeout";
        controller.abort();
      }, timeoutMs) : null;

      res.on("close", () => {
        if (!res.writableEnded) {
          abortReason = "client_close";
          controller.abort();
        }
      });

      const response = await fetch(targetUrl, {
        method,
        headers,
        body: (method === "POST" || method === "PUT") ? JSON.stringify(reqBody) : undefined,
        signal: controller.signal
      });
      if (timeoutId) clearTimeout(timeoutId);

      addLog("info", `[API Proxy Complete] Status ${response.status} ${response.statusText} (${Date.now() - startTime}ms) -> ${provider.name}`, "proxy");

      // Pass along response headers except transfer-encoding/content-encoding
      res.status(response.status);
      response.headers.forEach((value, key) => {
        const lowerKey = key.toLowerCase();
        if (lowerKey !== "transfer-encoding" && lowerKey !== "content-encoding" && lowerKey !== "content-length") {
          res.setHeader(key, value);
        }
      });

      // Handle stream or full body, capturing token usage from the response
      const isStream = (response.headers.get("content-type") || "").includes("text/event-stream");
      const shouldLogRes = cfg.logResponseBody === true;
      let resBodyBuffer = "";
      const decoder = new TextDecoder("utf-8");
      const usageParser = new SseUsageParser();
      let nonStreamBody = "";

      if (response.body) {
        const reader = response.body.getReader();
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          res.write(value);
          const text = decoder.decode(value, { stream: true });
          if (isStream) {
            usageParser.push(text);
          } else {
            nonStreamBody += text;
          }
          if (shouldLogRes) {
            resBodyBuffer += text;
          }
        }
        if (shouldLogRes && resBodyBuffer) {
          addLog("info", `[API Proxy Response Body] ${resBodyBuffer}`, "proxy");
        }
        if (cfg.debug) {
          const respHeaders: string[] = [];
          response.headers.forEach((value, key) => {
            if (key.toLowerCase() !== "set-cookie") respHeaders.push(`${key}: ${value}`);
          });
          addLog("info", `[API Proxy Response Headers] ${respHeaders.join(" | ")}`, "proxy");
        }
      }
      res.end();
      if (sem) sem.release();

      // Extract usage (prompt/completion/cached tokens) and resolved model
      let promptTokens = 0, completionTokens = 0, cachedTokens = 0, totalTokens = 0;
      let resModel = reqModel;
      if (isStream) {
        promptTokens = usageParser.usage?.promptTokens ?? 0;
        completionTokens = usageParser.usage?.completionTokens ?? 0;
        cachedTokens = usageParser.usage?.cachedTokens ?? 0;
        if (usageParser.model) resModel = usageParser.model;
      } else {
        try {
          const json = JSON.parse(nonStreamBody);
          const usage = json?.usage;
          if (usage) {
            promptTokens = usage.prompt_tokens ?? 0;
            completionTokens = usage.completion_tokens ?? 0;
            cachedTokens = usage.prompt_tokens_details?.cached_tokens ?? 0;
          }
          if (json?.model) resModel = json.model;
        } catch {}
      }
      totalTokens = promptTokens + completionTokens;

      addRequestLog({
        id: crypto.randomUUID(),
        timestamp: new Date().toISOString(),
        keyName: virtualKeyUsed,
        keyId: maskKey(apiKey),
        model: resModel,
        provider: provider.name,
        path: originalPath,
        method,
        promptTokens,
        completionTokens,
        cachedTokens,
        totalTokens,
        status: response.status,
        durationMs: Date.now() - startTime,
        stream: isStream
      });
    } catch (error: any) {
      if (timeoutId) clearTimeout(timeoutId);
      if (error.name === "AbortError") {
        if (abortReason === "timeout") {
          addLog("warn", `[API Proxy Timeout] Upstream timeout after ${timeoutMs}ms (${Date.now() - startTime}ms)`, "proxy");
          addRequestLog({
            id: crypto.randomUUID(),
            timestamp: new Date().toISOString(),
            keyName: virtualKeyUsed,
            keyId: maskKey(apiKey),
            model: reqModel,
            provider: provider.name,
            path: originalPath,
            method,
            promptTokens: 0, completionTokens: 0, cachedTokens: 0, totalTokens: 0,
            status: 504,
            durationMs: Date.now() - startTime,
            stream: false,
            error: "upstream timeout"
          });
          if (sem) sem.release();
          if (res.headersSent) {
            res.destroy(error);
          } else {
            res.status(504).json({ error: `Upstream timeout: provider ${provider.name} did not respond within ${timeoutMs}ms` });
          }
          return;
        }
        addLog("warn", `[API Proxy Aborted] Client closed connection (${Date.now() - startTime}ms)`, "proxy");
        addRequestLog({
          id: crypto.randomUUID(),
          timestamp: new Date().toISOString(),
          keyName: virtualKeyUsed,
          keyId: maskKey(apiKey),
          model: reqModel,
          provider: provider.name,
          path: originalPath,
          method,
          promptTokens: 0, completionTokens: 0, cachedTokens: 0, totalTokens: 0,
          status: 0,
          durationMs: Date.now() - startTime,
          stream: false,
          error: "client closed connection"
        });
        if (sem) sem.release();
        return;
      }
      addLog("error", `[API Proxy Error] Forwarding failed: ${error.message} (${Date.now() - startTime}ms)`, "proxy");
      addRequestLog({
        id: crypto.randomUUID(),
        timestamp: new Date().toISOString(),
        keyName: virtualKeyUsed,
        keyId: maskKey(apiKey),
        model: reqModel,
        provider: provider.name,
        path: originalPath,
        method,
        promptTokens: 0, completionTokens: 0, cachedTokens: 0, totalTokens: 0,
        status: 502,
        durationMs: Date.now() - startTime,
        stream: false,
        error: error.message
      });
      if (sem) sem.release();
      if (res.headersSent) {
        res.destroy(error);
      } else {
        res.status(502).json({ error: `Provider gateway error: ${error.message}` });
      }
    }
  });

  // Vite development vs production serving
  if (process.env.NODE_ENV !== "production") {
    const vite = await createViteServer({
      server: { middlewareMode: true },
      appType: "spa"
    });
    app.use(vite.middlewares);
  } else {
    const distPath = path.join(process.cwd(), "dist");
    app.use(express.static(distPath));
    app.get("*", (req, res) => {
      res.sendFile(path.join(distPath, "index.html"));
    });
  }

  app.listen(port, host, () => {
    addLog("info", `LLM Proxy System listening on http://${host}:${port}`);
  });
}

startServer();
