import { defineConfig, devices } from "@playwright/test";

// e2e runs the backend on a non-default port (8080 is often taken locally,
// e.g. by syncthing) and points the Vite build at it. The test reads the same
// URL from BACKEND_URL.
const BACKEND_PORT = 8744;
const BACKEND_URL = `http://localhost:${BACKEND_PORT}`;
process.env.BACKEND_URL = BACKEND_URL;

// Auto-starts the Go backend (fresh temp DB, CORS on) and the Vite dev server,
// then drives the React app + public web. Geolocation is faked to Barcelona so
// the "nearby" flows resolve deterministically.
export default defineConfig({
  testDir: "./tests",
  timeout: 45_000,
  // Generous assertion timeout so the first test doesn't flake while the Vite
  // dev server does its on-demand dependency optimization on cold start.
  expect: { timeout: 15_000 },
  fullyParallel: false,
  reporter: [["list"]],
  use: {
    baseURL: "http://localhost:5173",
    geolocation: { latitude: 41.3851, longitude: 2.1734 },
    permissions: ["geolocation"],
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        launchOptions: { args: ["--no-sandbox"] },
      },
    },
  ],
  webServer: [
    {
      command: `sh -c 'DB_PATH=$(mktemp -u /tmp/openwifipassmap-e2e-XXXX.db) DEV=1 ADDR=:${BACKEND_PORT} go run ./cmd/server'`,
      cwd: "..",
      url: `${BACKEND_URL}/api/health`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
    {
      command: "npm run dev",
      cwd: "../mobile",
      url: "http://localhost:5173",
      env: { VITE_API_BASE: BACKEND_URL },
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
});
