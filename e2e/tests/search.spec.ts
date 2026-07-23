import { test, expect } from "@playwright/test";

const PASSWORD = "flatwhite123";
const EMAIL = "barista@example.com";
const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8744";

function uniqueUser() {
  return `barista_${Date.now().toString(36)}`;
}

// Nominatim jsonv2-shaped fixture near Girona, matching what the backend's
// /api/geocode proxy trims and returns (see internal/api/geocode.go).
const GIRONA = {
  name: "Girona",
  display_name: "Girona, Gironès, Catalonia, Spain",
  lat: 41.9794,
  lng: 2.8214,
  bbox: [41.94, 42.02, 2.77, 2.87],
};
const GIRONA_RESTAURANT = {
  name: "El Celler",
  display_name: "El Celler, Girona, Catalonia, Spain",
  lat: 41.98,
  lng: 2.822,
};

async function stubGeocode(page: import("@playwright/test").Page, results: unknown[]) {
  await page.route("**/api/geocode*", (route) => route.fulfill({ json: { results } }));
}

// One shared account for the whole file (register consumes the same tight,
// globally-shared auth rate-limit bucket as every other e2e spec) — these
// tests only need a valid token to seed spots, not isolated identities.
let token: string;
test.beforeAll(async ({ request }) => {
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username: uniqueUser(), email: EMAIL, password: PASSWORD },
  });
  token = (await reg.json()).token as string;
});

test("Public web: search an address centers the map and reloads spots there", async ({
  page,
  request,
  context,
}) => {
  await context.grantPermissions(["geolocation"]);

  await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      venue_name: "Girona Café",
      essid: "Girona-Guest",
      password: "vinaigre",
      auth_type: "wpa2",
      lat: GIRONA.lat,
      lng: GIRONA.lng,
    },
  });

  await stubGeocode(page, [GIRONA]);

  // Start the map far from Girona so a real hit (not a coincidental default
  // viewport) is what surfaces the spot.
  await page.goto(`${BACKEND}/?lat=0&lng=0&zoom=3`);
  await page.getByTestId("search-input").fill("Girona");
  await page.getByTestId("search-submit").click();

  const card = page.getByTestId("spot").filter({ hasText: "Girona Café" });
  await expect(card).toBeVisible({ timeout: 10_000 });

  // flyToBounds → moveend → syncURL: the URL now reflects the new center.
  await expect(page).toHaveURL(/lat=41\.9/);
});

test("Public web: multiple matches show a pick list; choosing one centers the map", async ({
  page,
  request,
  context,
}) => {
  await context.grantPermissions(["geolocation"]);

  await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      venue_name: "El Celler WiFi",
      essid: "Celler-Guest",
      password: "reserva",
      auth_type: "wpa2",
      lat: GIRONA_RESTAURANT.lat,
      lng: GIRONA_RESTAURANT.lng,
    },
  });

  await stubGeocode(page, [GIRONA, GIRONA_RESTAURANT]);

  await page.goto(`${BACKEND}/?lat=0&lng=0&zoom=3`);
  await page.getByTestId("search-input").fill("Girona");
  await page.getByTestId("search-submit").click();

  const results = page.getByTestId("search-result");
  await expect(results).toHaveCount(2);
  await results.filter({ hasText: "El Celler" }).click();

  const card = page.getByTestId("spot").filter({ hasText: "El Celler WiFi" });
  await expect(card).toBeVisible({ timeout: 10_000 });
});

test("React app: search a place runs the nearby query from its coordinates", async ({
  page,
  request,
}) => {
  // Seed a spot near Girona via the API directly — the faked geolocation is
  // Barcelona (~100 km away), and the app's "add" flow uses that geolocation
  // for new spots, so only the address search (not "Find WiFi near me")
  // should surface this spot.
  await request.post(`${BACKEND}/api/spots`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      venue_name: "Girona Mobile Café",
      essid: "GironaMobile-Guest",
      password: "beans1234",
      auth_type: "wpa2",
      lat: GIRONA.lat,
      lng: GIRONA.lng,
    },
  });

  await stubGeocode(page, [GIRONA]);

  await page.goto("/");
  await page.getByTestId("nearby-tab").click();
  await page.getByTestId("search-input").fill("Girona");
  await page.getByTestId("search-submit").click();

  await expect(page.getByTestId("status")).toContainText("near Girona", { timeout: 10_000 });
  const card = page.getByTestId("spot-card").filter({ hasText: "Girona Mobile Café" });
  await expect(card).toBeVisible();
});

test("Geocode proxy: missing query is rejected", async ({ request }) => {
  const res = await request.get(`${BACKEND}/api/geocode`);
  expect(res.status()).toBe(400);
});
