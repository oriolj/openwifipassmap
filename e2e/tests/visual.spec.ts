import { test } from "@playwright/test";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8744";

// Not assertions — this spec captures screenshots so the rendered UI can be
// eyeballed (CDN CSS loaded, layout sane). Output lands in e2e/screenshots/.
test("capture screenshots", async ({ page, request }) => {
  const username = `shot_${Date.now().toString(36)}`;
  const reg = await request.post(`${BACKEND}/api/auth/register`, {
    data: { username, password: "flatwhite123" },
  });
  const token = (await reg.json()).token as string;

  const spots = [
    { venue_name: "Blue Bottle Coffee", essid: "BlueBottle-Guest", password: "coffee4all", lat: 41.3851, lng: 2.1734, notes: "ask the barista" },
    { venue_name: "City Library", essid: "Library-Public", password: "readmore", lat: 41.3861, lng: 2.1744, notes: "quiet zone upstairs" },
  ];
  let lastId = "";
  for (const s of spots) {
    const r = await request.post(`${BACKEND}/api/spots`, {
      headers: { Authorization: `Bearer ${token}` },
      data: { ...s, auth_type: "wpa2" },
    });
    lastId = (await r.json()).id as string;
  }

  // Seed the same token into the React app's localStorage so it shows as logged in.
  await page.goto("/");
  await page.evaluate(
    ([t, u]) => {
      localStorage.setItem("wifispots.token", t);
      localStorage.setItem("wifispots.user", JSON.stringify({ id: "x", username: u, is_admin: false }));
    },
    [token, username],
  );
  await page.reload();
  await page.getByTestId("locate-btn").click();
  await page.getByTestId("spot-card").first().waitFor();
  await page.getByTestId("reveal-password").first().click();
  await page.screenshot({ path: "screenshots/app-nearby.png", fullPage: true });

  await page.getByTestId("add-tab").click();
  await page.screenshot({ path: "screenshots/app-add.png", fullPage: true });

  await page.goto(`${BACKEND}/s/${lastId}`);
  await page.getByTestId("spot-card").waitFor();
  await page.screenshot({ path: "screenshots/web-share.png", fullPage: true });
});
