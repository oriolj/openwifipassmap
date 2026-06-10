import { test, expect } from "@playwright/test";

const PASSWORD = "flatwhite123";
// Email is required at registration but not unique, so tests can share one.
const EMAIL = "barista@example.com";
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
  await page.getByTestId("auth-email").fill(EMAIL);
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

  // The header's contribution count (from /api/me) reflects the new spot.
  await expect(page.getByTestId("spots-added")).toContainText("1 WiFi added");

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
    data: { username, email: EMAIL, password: PASSWORD },
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

  // /api/me reports the contribution count (from a real query, not list length).
  const me = await request.get(`${BACKEND}/api/me`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(me.ok()).toBeTruthy();
  const meJson = await me.json();
  expect(meJson.spots_added).toBe(1);
  expect(meJson.user.username).toBe(username);

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

test("Public web: register → add a spot via the map pin → see it nearby + on share page", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["geolocation"]); // for the backend origin

  const username = uniqueUser();

  // Start the map at Barcelona deterministically (no reliance on geolocation
  // timing) so the dropped pin lands where we expect.
  await page.goto(`${BACKEND}/?lat=41.3851&lng=2.1734&zoom=14`);

  // Adding while logged out opens the auth modal; register inline.
  await page.getByTestId("add-wifi").click();
  await expect(page.getByTestId("auth-modal")).toBeVisible();
  await page.getByTestId("auth-username").fill(username);
  await page.getByTestId("auth-email").fill(EMAIL);
  await page.getByTestId("auth-password").fill(PASSWORD);
  await page.getByTestId("auth-register").click();
  await expect(page.getByTestId("account-button")).toContainText(username);

  // Registering mid-flow drops us straight into placement: a pin + pick bar.
  await expect(page.getByTestId("pick-bar")).toBeVisible();
  await page.getByTestId("pick-continue").click();

  // Fill the spot form and save.
  await expect(page.getByTestId("add-modal")).toBeVisible();
  await page.getByTestId("add-venue").fill("Web Café");
  await page.getByTestId("add-essid").fill("WebCafe-Guest");
  await page.getByTestId("add-password").fill("latteart");
  await page.getByTestId("add-notes").fill("terrace seating");
  await page.getByTestId("add-save").click();

  await expect(page.getByTestId("toast")).toContainText("added");

  // It reloads into the in-view list. Scope to this venue's card.
  const card = page.getByTestId("spot").filter({ hasText: "Web Café" });
  await expect(card).toBeVisible({ timeout: 10_000 });

  // The account menu surfaces the contribution count.
  await page.getByTestId("account-button").click();
  await expect(page.getByTestId("my-spot-count")).toContainText("1 WiFi added");

  // And the credentials render on its shareable page.
  const link = card.getByRole("link", { name: "View & share" });
  const href = await link.getAttribute("href");
  await page.goto(`${BACKEND}${href}`);
  await expect(page.getByTestId("essid")).toHaveText("WebCafe-Guest");
  await expect(page.getByTestId("password")).toHaveText("latteart");
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
    data: { username: owner, email: EMAIL, password: PASSWORD },
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
    data: { username: visitor, email: EMAIL, password: PASSWORD },
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
    data: { username, email: EMAIL, password: PASSWORD },
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

test("Web: quality stars, speed, and the quality/speed filters", async ({ page, request, context }) => {
  await context.grantPermissions(["geolocation"]);
  const username = uniqueUser();
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username, email: EMAIL, password: PASSWORD },
  });
  const token = (await reg.json()).token as string;

  // A great/fast spot and a basic/slow one, close together.
  for (const s of [
    { venue_name: "Fast Fibre Café", essid: "Fast-Guest", auth_type: "wpa2", lat: 41.3851, lng: 2.1734, quality: 3, down_mbps: 150 },
    { venue_name: "Slow Corner", essid: "Slow-Guest", auth_type: "wpa2", lat: 41.3853, lng: 2.1736, quality: 1, down_mbps: 5 },
  ]) {
    const r = await request.post(`${BACKEND}/api/spots`, {
      headers: { Authorization: `Bearer ${token}` },
      data: s,
    });
    expect(r.status()).toBe(201);
  }

  await page.goto(`${BACKEND}/?lat=41.3851&lng=2.1734&zoom=15`);
  const fast = page.getByTestId("spot").filter({ hasText: "Fast Fibre Café" });
  const slow = page.getByTestId("spot").filter({ hasText: "Slow Corner" });
  await expect(fast).toBeVisible({ timeout: 10_000 });
  await expect(slow).toBeVisible();

  // Quality stars + speed render on the card.
  await expect(fast.getByTestId("spot-quality")).toHaveText("★★★");
  await expect(fast.getByTestId("spot-speed")).toContainText("150");

  // Filter to ★★★ only → the basic spot drops out.
  await page.getByTestId("filter-quality").selectOption("3");
  await expect(fast).toBeVisible();
  await expect(slow).toHaveCount(0);

  // Reset quality; filter to 100+ Mbps → still only the fast one (150 vs 5).
  await page.getByTestId("filter-quality").selectOption("0");
  await page.getByTestId("filter-speed").selectOption("100");
  await expect(fast).toBeVisible();
  await expect(slow).toHaveCount(0);

  // Share page surfaces the quality stars.
  const href = await fast.getByRole("link", { name: "View & share" }).getAttribute("href");
  await page.goto(`${BACKEND}${href}`);
  await expect(page.getByTestId("quality")).toHaveText("★★★");
});

test("Web: another user reviews a spot (rating + speed); owner edits their own spot", async ({
  page,
  request,
  context,
}) => {
  await context.grantPermissions(["geolocation"]);

  // Owner creates a spot rated ★ with a slow measurement.
  const owner = uniqueUser();
  const ownerReg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: owner, email: EMAIL, password: PASSWORD },
  });
  const ownerJson = await ownerReg.json();
  const ownerTok = ownerJson.token as string;
  const ownerId = ownerJson.user.id as string;
  const created = await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${ownerTok}` },
    data: { venue_name: "Review Café", essid: "Review-Guest", auth_type: "wpa2", lat: 41.39, lng: 2.18, quality: 1, down_mbps: 5 },
  });
  expect(created.status()).toBe(201);
  const spotId = (await created.json()).id as string;

  // A second user logs into the web (seed localStorage) and reviews it ★★★ + 200 Mbps.
  const rater = uniqueUser();
  const raterReg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: rater, email: EMAIL, password: PASSWORD },
  });
  const raterJson = await raterReg.json();
  await page.goto(`${BACKEND}/?lat=41.39&lng=2.18&zoom=16`);
  await page.evaluate(
    ([t, u, id]) => {
      localStorage.setItem("owpm_token", t);
      localStorage.setItem("owpm_user", u);
      localStorage.setItem("owpm_user_id", id);
    },
    [raterJson.token, rater, raterJson.user.id],
  );
  await page.reload();

  const card = page.getByTestId("spot").filter({ hasText: "Review Café" });
  await expect(card).toBeVisible({ timeout: 10_000 });
  // Not the rater's spot — no Edit button.
  await expect(card.getByTestId("spot-edit")).toHaveCount(0);

  await card.getByTestId("spot-rate").click();
  await expect(page.getByTestId("review-modal")).toBeVisible();
  await page.getByTestId("review-quality").selectOption("3");
  await page.getByTestId("review-down").fill("200");
  await page.getByTestId("review-save").click();
  await expect(page.getByTestId("toast")).toContainText("review saved");

  // Aggregates: avg(1,3)=2 stars, 2 ratings, latest speed 200.
  await expect(card.getByTestId("spot-quality")).toHaveText("★★☆", { timeout: 10_000 });
  await expect(card.getByTestId("spot-ratings-count")).toHaveText("(2)");
  await expect(card.getByTestId("spot-speed")).toContainText("200");

  // Owner signs in on the web and edits the venue name (facts only).
  await page.evaluate(
    ([t, u, id]) => {
      localStorage.setItem("owpm_token", t);
      localStorage.setItem("owpm_user", u);
      localStorage.setItem("owpm_user_id", id);
    },
    [ownerTok, owner, ownerId],
  );
  await page.reload();
  const ownCard = page.getByTestId("spot").filter({ hasText: "Review Café" });
  await expect(ownCard).toBeVisible({ timeout: 10_000 });
  await ownCard.getByTestId("spot-edit").click();
  await expect(page.getByTestId("add-modal")).toBeVisible();
  await page.getByTestId("add-venue").fill("Renamed Café");
  await page.getByTestId("add-save").click();
  await expect(page.getByTestId("toast")).toContainText("updated");
  await expect(page.getByTestId("spot").filter({ hasText: "Renamed Café" })).toBeVisible({ timeout: 10_000 });

  // Editing didn't clobber the community aggregates.
  const after = await (await request.get(`${BACKEND}/api/spots/${spotId}`)).json();
  expect(after.quality).toBe(2);
  expect(after.ratings_count).toBe(2);
  expect(after.down_mbps).toBe(200);
});

test("React app: rate own spot and edit it inline", async ({ page }) => {
  const username = uniqueUser();

  await page.goto("/");
  await page.getByTestId("auth-username").fill(username);
  await page.getByTestId("auth-email").fill(EMAIL);
  await page.getByTestId("auth-password").fill(PASSWORD);
  await page.getByTestId("auth-submit").click();
  await expect(page.getByTestId("user-badge")).toHaveText(username);

  await page.getByTestId("add-tab").click();
  await page.getByTestId("add-venue").fill("Parity Café");
  await page.getByTestId("add-essid").fill("Parity-Guest");
  await page.getByTestId("add-password").fill("beans1234");
  await page.getByTestId("add-submit").click();
  await expect(page.getByTestId("add-status")).toContainText("Saved");

  await page.getByTestId("nearby-tab").click();
  await page.getByTestId("locate-btn").click();
  const card = page.getByTestId("spot-card").filter({ hasText: "Parity Café" });
  await expect(card).toBeVisible();

  // Rate the spot ★★★ with a speed test.
  await card.getByTestId("spot-rate").click();
  await card.getByTestId("rate-quality").selectOption("3");
  await card.getByTestId("rate-down").fill("120");
  await card.getByTestId("rate-save").click();
  await expect(card.getByTestId("spot-quality")).toHaveText("★★★");
  await expect(card.getByTestId("spot-ratings-count")).toHaveText("(1)");
  await expect(card.getByTestId("spot-speed")).toContainText("120");

  // Edit the venue name (facts-only form on own spots).
  await card.getByTestId("spot-edit").click();
  await card.getByTestId("edit-venue").fill("Parity Café Renamed");
  await card.getByTestId("edit-save").click();
  await expect(
    page.getByTestId("spot-card").filter({ hasText: "Parity Café Renamed" }),
  ).toBeVisible();
});

test("Email verification page + admin reports authz", async ({ page, request }) => {
  // A bad verification token is rejected on the API and surfaced on the page.
  const bad = await request.post(`${BACKEND}/api/auth/verify-email`, {
    data: { token: "not-a-real-token" },
  });
  expect(bad.status()).toBe(400);

  await page.goto(`${BACKEND}/verify?token=not-a-real-token`);
  await expect(page.getByTestId("verify-error")).toContainText("invalid or has expired");

  // /api/reports: 401 anonymous, 403 for a regular (non-admin) user.
  expect((await request.get(`${BACKEND}/api/reports`)).status()).toBe(401);
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: uniqueUser(), email: EMAIL, password: PASSWORD },
  });
  const token = (await reg.json()).token as string;
  const asUser = await request.get(`${BACKEND}/api/reports`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(asUser.status()).toBe(403);
});

test("Share page: logged-in user rates from a shared link", async ({ page, request, context }) => {
  await context.grantPermissions(["geolocation"]);
  const owner = uniqueUser();
  const ownerReg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: owner, email: EMAIL, password: PASSWORD },
  });
  const ownerTok = (await ownerReg.json()).token as string;
  const created = await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${ownerTok}` },
    data: { venue_name: "Shared Café", essid: "Shared-Guest", auth_type: "wpa2", lat: 41.4, lng: 2.19, quality: 1 },
  });
  const spotId = (await created.json()).id as string;

  const rater = uniqueUser();
  const raterReg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: rater, email: EMAIL, password: PASSWORD },
  });
  const raterTok = (await raterReg.json()).token as string;

  await page.goto(`${BACKEND}/s/${spotId}`);
  await page.evaluate((t) => localStorage.setItem("owpm_token", t), raterTok);
  await page.getByTestId("share-rate").click();
  await page.getByTestId("share-rate-quality").selectOption("3");
  await page.getByTestId("share-rate-down").fill("90");
  await page.getByTestId("share-rate-save").click();
  await expect(page.getByTestId("share-rate-done")).toBeVisible();

  // After the auto-reload the server-rendered stars show the new average.
  await expect(page.getByTestId("quality")).toHaveText("★★☆", { timeout: 10_000 });
});

test("Password reset: email required to register, forgot is non-enumerating, bad token rejected", async ({
  request,
}) => {
  const username = uniqueUser();

  // Registration now requires a valid email.
  const noEmail = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username, password: PASSWORD },
  });
  expect(noEmail.status()).toBe(400);

  const withEmail = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username, email: EMAIL, password: PASSWORD },
  });
  expect(withEmail.status()).toBe(200);

  // forgot-password returns the same generic 200 for a known and an unknown
  // address — no way to probe which emails are registered.
  for (const email of [EMAIL, `nobody-${username}@example.com`]) {
    const res = await request.post(`${BACKEND}/api/auth/forgot-password`, { data: { email } });
    expect(res.status()).toBe(200);
    expect((await res.json()).message).toContain("reset link");
  }

  // A bogus token cannot set a password.
  const bad = await request.post(`${BACKEND}/api/auth/reset-password`, {
    data: { token: "not-a-real-token", password: "brandnewpw9" },
  });
  expect(bad.status()).toBe(400);

  // The reset page renders for a magic link.
  const page = await request.get(`${BACKEND}/reset?token=whatever`);
  expect(page.ok()).toBeTruthy();
  expect(await page.text()).toContain("Choose a new password");
});
