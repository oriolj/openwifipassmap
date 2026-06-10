import { test, expect } from "@playwright/test";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8744";
const PASSWORD = "flatwhite123";
const EMAIL = "pwa@example.com";

test("PWA: manifest + service worker served correctly, SW registers", async ({ page, request }) => {
  const manifest = await request.get(`${BACKEND}/manifest.webmanifest`);
  expect(manifest.ok()).toBeTruthy();
  expect(manifest.headers()["content-type"]).toContain("application/manifest+json");
  const m = await manifest.json();
  expect(m.short_name.length).toBeLessThanOrEqual(12);
  expect(m.icons.some((i: { purpose?: string }) => i.purpose === "maskable")).toBeTruthy();

  const sw = await request.get(`${BACKEND}/sw.js`);
  expect(sw.ok()).toBeTruthy();
  expect(sw.headers()["content-type"]).toContain("javascript");
  expect(sw.headers()["cache-control"]).toContain("no-cache");

  await page.goto(`${BACKEND}/?lat=41.38&lng=2.17&zoom=14`);
  // SW registers (localhost counts as a secure context).
  const swState = await page.evaluate(async () => {
    const reg = await navigator.serviceWorker.ready;
    return { scope: reg.scope, active: !!reg.active };
  });
  expect(swState.active).toBeTruthy();
  expect(swState.scope).toBe(`${BACKEND}/`);
});

test("PWA: offline pack downloads and serves the map without network", async ({
  page,
  request,
  context,
}) => {
  // Seed two spots near the test viewport.
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: `pwa_${Date.now().toString(36)}`, email: EMAIL, password: PASSWORD },
  });
  const token = (await reg.json()).token as string;
  for (const s of [
    { venue_name: "Offline Café", essid: "Off-Guest", auth_type: "wpa2", lat: 41.3801, lng: 2.1701, quality: 2 },
    { venue_name: "Pack Bar", essid: "Pack-Guest", auth_type: "wpa2", lat: 41.3805, lng: 2.1705 },
  ]) {
    await request.post(`${BACKEND}/api/spots`, {
      headers: { Authorization: `Bearer ${token}` },
      data: s,
    });
  }

  await page.goto(`${BACKEND}/?lat=41.3803&lng=2.1703&zoom=15`);
  await expect(page.getByTestId("spot").first()).toBeVisible({ timeout: 10_000 });
  // Make sure the shell is fully precached before we cut the network.
  await page.evaluate(() => navigator.serviceWorker.ready);

  // Download the offline pack and wait for confirmation.
  await page.getByTestId("offline-pack").click();
  await expect(page.getByTestId("toast")).toContainText("Offline pack saved", { timeout: 15_000 });

  // Kill the network: the page must reload from the SW cache and the spots
  // must come from the IndexedDB pack.
  await context.setOffline(true);
  await page.reload();
  await expect(page.getByTestId("spot").filter({ hasText: "Offline Café" })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId("status")).toContainText("Offline");
  await expect(page.getByTestId("status")).toContainText("from your pack");
  await context.setOffline(false);
});
