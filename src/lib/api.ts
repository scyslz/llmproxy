const ADMIN_TOKEN_KEY = "llm_proxy_admin_token";

export function getAdminToken(): string | null {
  return localStorage.getItem(ADMIN_TOKEN_KEY);
}

export function setAdminToken(token: string | null) {
  if (token) {
    localStorage.setItem(ADMIN_TOKEN_KEY, token);
  } else {
    localStorage.removeItem(ADMIN_TOKEN_KEY);
  }
}

export async function apiFetch(url: string, options: RequestInit = {}): Promise<Response> {
  const headers = new Headers(options.headers || {});
  const token = getAdminToken();
  if (token && !headers.has("x-admin-token")) {
    headers.set("x-admin-token", token);
  }
  const response = await fetch(url, { ...options, headers });
  
  if (response.status === 401 && url.startsWith("/api/") && !url.startsWith("/api/admin/")) {
    window.dispatchEvent(new CustomEvent("admin-unauthorized"));
  }
  return response;
}
