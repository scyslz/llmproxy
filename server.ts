import express from "express";
import path from "path";
import fs from "fs";
import { createServer as createViteServer } from "vite";

interface Provider {
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
  enabled: boolean;
  models: string[];
  concurrency?: number; // 0 or undefined = unlimited
  timeout?: number; // upstream request timeout in ms, 0 or undefined = no timeout
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

interface Config {
  listen?: string;
  enableVirtualKey: boolean;
  enableAdminAuth: boolean;
  adminPassword?: string;
  debug: boolean;
  logRequestBody?: boolean;
  logResponseBody?: boolean;
  maxLogSizeMB?: number;
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
  const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
  const logEntry = { timestamp, level, category, file: activeNum, message };

  systemLogs.push(logEntry);
  if (systemLogs.length > 200) {
    systemLogs.shift();
  }
  console.log(`[${level.toUpperCase()}] [${category.toUpperCase()}] ${message}`);

  // Dual-file round-robin (cyclic) disk rotation
  try {
    ensureLogDir();
    const maxSize = ((typeof cfg !== "undefined" && cfg?.maxLogSizeMB) ? cfg.maxLogSizeMB : 2) * 1024 * 1024;
    const activePath = activeNum === 1 ? LOG_FILE_1 : LOG_FILE_2;

    let size = 0;
    if (fs.existsSync(activePath)) {
      size = fs.statSync(activePath).size;
    }

    if (size >= maxSize) {
      // Rotate to the other file
      const nextNum = activeNum === 1 ? 2 : 1;
      const nextPath = nextNum === 1 ? LOG_FILE_1 : LOG_FILE_2;
      fs.writeFileSync(nextPath, "", "utf-8"); // Clear/overwrite next file
      if (typeof cfg !== "undefined" && cfg) {
        cfg.activeLogFile = nextNum;
        saveConfig(cfg);
      }
      logEntry.file = nextNum;
      fs.appendFileSync(nextPath, JSON.stringify(logEntry) + "\n", "utf-8");
    } else {
      fs.appendFileSync(activePath, JSON.stringify(logEntry) + "\n", "utf-8");
    }
  } catch (err) {}
}

function readDiskLogs(): any[] {
  try {
    ensureLogDir();
    const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
    const activePath = activeNum === 1 ? LOG_FILE_1 : LOG_FILE_2;

    const parseLogFile = (filePath: string, fileNum: number) => {
      if (!fs.existsSync(filePath)) return [];
      const content = fs.readFileSync(filePath, "utf-8");
      return content
        .split("\n")
        .filter(Boolean)
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
      return systemLogs.filter(log => !log.file || log.file === activeNum);
    }
    return activeLogs.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
  } catch {
    const activeNum = (typeof cfg !== "undefined" && cfg?.activeLogFile) ? cfg.activeLogFile : 1;
    return systemLogs.filter(log => !log.file || log.file === activeNum);
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
          concurrency: typeof p.concurrency === "number" ? p.concurrency : 0
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
      concurrency: typeof p.concurrency === "number" ? p.concurrency : 0
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
    saveConfig(cfg);
    addLog("info", `Settings updated: enableVirtualKey=${cfg.enableVirtualKey}, enableAdminAuth=${cfg.enableAdminAuth}, debug=${cfg.debug}, logRequestBody=${cfg.logRequestBody}, logResponseBody=${cfg.logResponseBody}, maxLogSizeMB=${cfg.maxLogSizeMB}`, "system");
    res.json({
      enableVirtualKey: cfg.enableVirtualKey,
      enableAdminAuth: cfg.enableAdminAuth,
      debug: cfg.debug,
      logRequestBody: cfg.logRequestBody !== undefined ? cfg.logRequestBody : true,
      logResponseBody: cfg.logResponseBody === true,
      maxLogSizeMB: cfg.maxLogSizeMB || 2,
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
    const activeNum = cfg.activeLogFile || 1;
    const diskLogs = readDiskLogs();
    res.json(diskLogs.length > 0 ? diskLogs : systemLogs.filter(log => !log.file || log.file === activeNum));
  });

  app.get("/api/logs/status", (req, res) => {
    try {
      ensureLogDir();
      let s1 = 0;
      let s2 = 0;
      if (fs.existsSync(LOG_FILE_1)) s1 = fs.statSync(LOG_FILE_1).size;
      if (fs.existsSync(LOG_FILE_2)) s2 = fs.statSync(LOG_FILE_2).size;
      const diskLogs = readDiskLogs();
      res.json({
        activeFile: cfg.activeLogFile || 1,
        file1Size: s1,
        file2Size: s2,
        maxLogSizeMB: cfg.maxLogSizeMB || 2,
        totalLogs: diskLogs.length
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
        const fallbackModel = provider.models[0];
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
    const targetUrl = `${endpointBase}${cleanSubPath}`;

    addLog(
      "info",
      `[API Proxy Forward] ${method} ${originalPath} -> ${provider.name} (${reqModel || "default"})${virtualKeyUsed ? ` [Key: ${virtualKeyUsed}]` : ""}`,
      "proxy"
    );

    const shouldLogReq = cfg.logRequestBody !== undefined ? cfg.logRequestBody : cfg.debug;
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

      // Handle stream or full body
      const shouldLogRes = cfg.logResponseBody === true;
      let resBodyBuffer = "";
      const decoder = shouldLogRes ? new TextDecoder("utf-8") : null;

      if (response.body) {
        const reader = response.body.getReader();
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          res.write(value);
          if (shouldLogRes && decoder && value) {
            resBodyBuffer += decoder.decode(value, { stream: true });
            if (resBodyBuffer.length > 50000) {
              resBodyBuffer = resBodyBuffer.substring(0, 50000) + "... [truncated]";
            }
          }
        }
        if (shouldLogRes && resBodyBuffer) {
          addLog("info", `[API Proxy Response Body] ${resBodyBuffer}`, "proxy");
        }
      }
      res.end();
      if (sem) sem.release();
    } catch (error: any) {
      if (timeoutId) clearTimeout(timeoutId);
      if (error.name === "AbortError") {
        if (abortReason === "timeout") {
          addLog("warn", `[API Proxy Timeout] Upstream timeout after ${timeoutMs}ms (${Date.now() - startTime}ms)`, "proxy");
          if (sem) sem.release();
          if (!res.headersSent) {
            res.status(504).json({ error: `Upstream timeout: provider ${provider.name} did not respond within ${timeoutMs}ms` });
          }
          return;
        }
        addLog("warn", `[API Proxy Aborted] Client closed connection (${Date.now() - startTime}ms)`, "proxy");
        if (sem) sem.release();
        return;
      }
      addLog("error", `[API Proxy Error] Forwarding failed: ${error.message} (${Date.now() - startTime}ms)`, "proxy");
      if (sem) sem.release();
      if (!res.headersSent) {
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
