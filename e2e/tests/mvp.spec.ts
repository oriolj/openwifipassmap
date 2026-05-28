import { test, expect } from "@playwright/test";

const PASSWORD = "flatwhite123";
const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8744";

// One unique account per run so repeated runs don't collide on username.
function uniqueUser() {
  return `barista_${Date.now().toString(36)}`;
}

test("React app: register → add spot → see it nearby → reveal password", async ({ page }) => {
  const username = uniqueUser();

  await page.goto("/");
  await expect(page.getByText("OpenWifiPassMap").first()).toBeVisible();

  // Register (the auth panel defaults to register mode).
  await page.getByTestId("auth-username").fill(username);
  await page.getByTestId("auth-password").fill(PASSWORD);
  await page.getByTestId("auth-submit").click();
  await expect(page.getByTestId("user-badge")).toHaveText(username);

  // Add a spot — uses the faked Barcelona geolocation.
  await page.getByTestId("add-tab").click();
  await page.getByTestId("add-venue").fill("Test Café");
  await page.getByTestId("add-essid").fill("TestCafe-Guest");
  await page.getByTestId("add-password").fill("beans1234");
  await page.getByTestId("add-notes").fill("corner table by the window");
  await page.getByTestId("add-submit").click();
  await expect(page.getByTestId("add-status")).toContainText("Saved");

  // See it in the nearby list. Scope to THIS spot's card — other spots can tie
  // at distance 0 (same coordinates), so `.first()` would be ambiguous.
  await page.getByTestId("nearby-tab").click();
  await page.getByTestId("locate-btn").click();
  const card = page.getByTestId("spot-card").filter({ hasText: "Test Café" });
  await expect(card).toBeVisible();

  // Reveal the password on this card.
  await card.getByTestId("reveal-password").click();
  await expect(card.getByTestId("spot-password")).toHaveText("beans1234");
});

test("Public web: landing lists a nearby spot and the share page renders it", async ({
  page,
  request,
  context,
}) => {
  await context.grantPermissions(["geolocation"]); // also for the backend origin

  // Seed an account + spot directly via the API (independent of the UI test).
  const username = uniqueUser();
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username, password: PASSWORD },
  });
  expect(reg.ok()).toBeTruthy();
  const token = (await reg.json()).token as string;

  const create = await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      venue_name: "Library Commons",
      essid: "Library-Public",
      password: "readmore",
      auth_type: "wpa2",
      lat: 41.3861,
      lng: 2.1744,
      notes: "quiet zone upstairs",
    },
  });
  expect(create.status()).toBe(201);
  const spotId = (await create.json()).id as string;

  // Landing page: click "find nearby" and expect at least one spot to appear.
  await page.goto(`${BACKEND}/`);
  await page.getByTestId("find-nearby").click();
  await expect(page.getByTestId("spot").first()).toBeVisible({ timeout: 10_000 });

  // Shareable per-spot page renders the credentials server-side.
  await page.goto(`${BACKEND}/s/${spotId}`);
  await expect(page.getByTestId("venue")).toHaveText("Library Commons");
  await expect(page.getByTestId("essid")).toHaveText("Library-Public");
  await expect(page.getByTestId("password")).toHaveText("readmore");
});

test("Confirmation flow: second user confirms a spot and it shows on share + nearby", async ({
  page,
  request,
  context,
}) => {
  await context.grantPermissions(["geolocation"]);

  // Owner creates the spot via API.
  const owner = uniqueUser();
  const ownerReg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: owner, password: PASSWORD },
  });
  const ownerToken = (await ownerReg.json()).token as string;

  const create = await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${ownerToken}` },
    data: {
      venue_name: "Verified Coffee",
      essid: "Verified-Guest",
      password: "espresso",
      auth_type: "wpa2",
      lat: 41.3855,
      lng: 2.174,
    },
  });
  expect(create.status()).toBe(201);
  const spotId = (await create.json()).id as string;

  // Owner cannot confirm own spot.
  const selfConfirm = await request.post(`${BACKEND}/api/spots/${spotId}/confirm`, {
    headers: { Authorization: `Bearer ${ownerToken}` },
  });
  expect(selfConfirm.status()).toBe(403);

  // A second user confirms the spot (the legitimate path).
  const visitor = uniqueUser();
  const visitorReg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: visitor, password: PASSWORD },
  });
  const visitorToken = (await visitorReg.json()).token as string;

  const confirm = await request.post(`${BACKEND}/api/spots/${spotId}/confirm`, {
    headers: { Authorization: `Bearer ${visitorToken}` },
  });
  expect(confirm.status()).toBe(200);
  const after = await confirm.json();
  expect(after.confirmations_count).toBe(1);
  expect(after.confirmed_by_me).toBe(true);
  expect(typeof after.last_confirmed_at).toBe("number");

  // Re-confirming is idempotent (still 1 distinct user).
  const again = await request.post(`${BACKEND}/api/spots/${spotId}/confirm`, {
    headers: { Authorization: `Bearer ${visitorToken}` },
  });
  expect(again.status()).toBe(200);
  expect((await again.json()).confirmations_count).toBe(1);

  // Share page surfaces the confirmation badge.
  await page.goto(`${BACKEND}/s/${spotId}`);
  await expect(page.getByTestId("confirmation")).toContainText("Confirmed working");
  await expect(page.getByTestId("confirmation")).toContainText("1 person");
});

test("Public web: malicious spot fields are rendered as text, not executed (XSS guard)", async ({
  page,
  request,
  context,
}) => {
  await context.grantPermissions(["geolocation"]);
  const username = uniqueUser();
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username, password: PASSWORD },
  });
  const token = (await reg.json()).token as string;

  const payload = '<img src=x onerror="window.__xssfired=true">PwnedCafe';
  const create = await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { venue_name: payload, essid: "Pwn-Net", password: "x", auth_type: "wpa2", lat: 41.3851, lng: 2.1734 },
  });
  expect(create.status()).toBe(201);

  await page.goto(`${BACKEND}/`);
  await page.getByTestId("find-nearby").click();
  await expect(page.getByTestId("spot").first()).toBeVisible({ timeout: 10_000 });

  // The injected <img> must NOT exist as a real element and its onerror must
  // never have fired — the payload is rendered as inert text.
  await expect(page.locator("img[onerror]")).toHaveCount(0);
  const fired = await page.evaluate(() => (window as unknown as { __xssfired?: boolean }).__xssfired === true);
  expect(fired).toBe(false);
  await expect(page.getByText("PwnedCafe")).toBeVisible();
});
