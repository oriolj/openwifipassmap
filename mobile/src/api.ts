// Typed client for the OpenWifiPassMap backend.
//
// The API base is configurable at build time via VITE_API_BASE so the same
// bundle can target a local backend (dev) or the deployed one (release).

function resolveApiBase(): string {
  const configured = import.meta.env.VITE_API_BASE;
  if (configured) return configured;
  if (import.meta.env.DEV) return "http://localhost:8080";
  // A production / native build with no VITE_API_BASE would otherwise silently
  // call localhost on the user's device. Fail loudly so a misconfigured release
  // is caught before it ships.
  throw new Error("VITE_API_BASE must be set for production builds");
}

export const API_BASE = resolveApiBase();

const TOKEN_KEY = "openwifipassmap.token";
const USER_KEY = "openwifipassmap.user";

export interface User {
  id: string;
  username: string;
  email?: string;
  is_admin: boolean;
}

export interface Spot {
  id: string;
  venue_name: string;
  essid: string;
  password: string;
  auth_type: string;
  lat: number;
  lng: number;
  geohash: string;
  notes: string;
  ping_ms?: number;
  down_mbps?: number;
  up_mbps?: number;
  created_by: string;
  created_at: number;
  updated_at: number;
  distance_km?: number;
  last_confirmed_at?: number;
  confirmations_count: number;
  confirmed_by_me?: boolean;
  quality: number; // 0 unrated, 1-3 = rounded avg of community ratings
  ratings_count: number;
  my_rating?: number;
}

export interface ReviewInput {
  quality?: number | null;
  down_mbps?: number | null;
  up_mbps?: number | null;
  ping_ms?: number | null;
}

export interface SpotInput {
  venue_name?: string;
  essid: string;
  password?: string;
  auth_type?: string;
  lat: number;
  lng: number;
  notes?: string;
  ping_ms?: number | null;
  down_mbps?: number | null;
  up_mbps?: number | null;
}

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function getStoredUser(): User | null {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as User;
  } catch {
    // Corrupt/partial value — don't crash app startup; clear and move on.
    clearAuth();
    return null;
  }
}

function setAuth(token: string, user: User) {
  localStorage.setItem(TOKEN_KEY, token);
  localStorage.setItem(USER_KEY, JSON.stringify(user));
}

export function clearAuth() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
}

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Content-Type", "application/json");
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  const res = await fetch(`${API_BASE}${path}`, { ...init, headers });
  // A 401 while we were sending a token means the session expired/was revoked:
  // drop the stale auth and let the UI react (auto-logout).
  if (res.status === 401 && token) {
    clearAuth();
    window.dispatchEvent(new Event("openwifipassmap:unauthorized"));
  }
  if (res.status === 204) return undefined as T;

  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, (body as { error?: string }).error ?? res.statusText);
  }
  return body as T;
}

export async function register(username: string, email: string, password: string): Promise<User> {
  const r = await request<{ token: string; user: User }>("/api/auth/register", {
    method: "POST",
    body: JSON.stringify({ username, email, password }),
  });
  setAuth(r.token, r.user);
  return r.user;
}

export async function getMe(): Promise<{ user: User; spots_added: number }> {
  return request<{ user: User; spots_added: number }>("/api/me");
}

export async function forgotPassword(email: string): Promise<string> {
  const r = await request<{ message: string }>("/api/auth/forgot-password", {
    method: "POST",
    body: JSON.stringify({ email }),
  });
  return r.message;
}

export async function login(username: string, password: string): Promise<User> {
  const r = await request<{ token: string; user: User }>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
  setAuth(r.token, r.user);
  return r.user;
}

export async function logout(): Promise<void> {
  try {
    await request<void>("/api/auth/logout", { method: "POST" });
  } finally {
    clearAuth();
  }
}

export interface NearbyResponse {
  results: Spot[];
  count: number;
  capped: boolean;
}

export async function nearby(lat: number, lng: number, radiusKm = 10): Promise<NearbyResponse> {
  return request<NearbyResponse>(
    `/api/spots/nearby?lat=${lat}&lng=${lng}&radius_km=${radiusKm}`,
  );
}

export async function createSpot(input: SpotInput): Promise<Spot> {
  return request<Spot>("/api/spots", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

// confirmSpot attests that the spot's WiFi credentials still work. Server
// upserts by (spot, user) so re-clicks are idempotent and just refresh the
// timestamp. Returns the spot with refreshed confirmation stats so the caller
// can replace the optimistic update with the authoritative count.
export async function confirmSpot(id: string): Promise<Spot> {
  return request<Spot>(`/api/spots/${encodeURIComponent(id)}/confirm`, {
    method: "POST",
  });
}

export async function reviewSpot(id: string, review: ReviewInput): Promise<Spot> {
  return request<Spot>(`/api/spots/${encodeURIComponent(id)}/review`, {
    method: "POST",
    body: JSON.stringify(review),
  });
}

export async function updateSpot(id: string, input: SpotInput): Promise<Spot> {
  return request<Spot>(`/api/spots/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}

export { ApiError };
