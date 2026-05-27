# Mobile WiFi APIs — capabilities & hard limits

Research notes for the "suggest nearby spots" and "connect" features. The headline:
**iOS and Android differ a lot, and neither lets you freely scan SSIDs the way a
desktop can.** Design the app around GPS + our database, not radio scanning.

## iOS

- **Join a network programmatically**: `NEHotspotConfigurationManager.apply` with
  an `NEHotspotConfiguration(ssid:passphrase:isWEP:)`. Available since iOS 11.
  The system shows a **user-confirmation dialog**; a nil error means the config
  was *added*, not necessarily that you're connected. Does **not** work in the
  Simulator — test on a device.
  - Avoid `joinOnce` — buggy since iOS 15.
- **Scan / list nearby SSIDs**: **not possible** for normal apps. There is no
  public API to enumerate surrounding networks. (`NEHotspotHelper` exists but is
  entitlement-gated for narrow use cases.) → On iOS, "nearby" must come from
  **GPS + our spot database**, then offer to *join* a specific one.
- **Read the current SSID**: `NEHotspotNetwork.fetchCurrent(completionHandler:)`
  (iOS 14+) returns the connected SSID/BSSID **only if** the app has one of:
  precise Location permission, an active `NEHotspotConfiguration` it created, an
  active VPN config, or an active DNS settings config. (`CNCopyCurrentNetworkInfo`
  is the deprecated predecessor.)

**iOS UX**: locate via GPS → show nearby DB spots → tap "Connect" → system join
dialog. No radio scan, no SSID list.

## Android

- **Auto-connect suggestions**: `WifiNetworkSuggestion` API (Android 10 / API 29+).
  The app suggests networks the system *may* auto-connect to.
- **Bulk add (great UX)**: `Settings.ACTION_WIFI_ADD_NETWORKS` intent accepts up
  to **5** `WifiNetworkSuggestion`s via `EXTRA_WIFI_NETWORK_LIST`; the user approves
  in one sheet and you get per-network result codes back in `onActivityResult`.
  → "Import the 5 nearest spots" in one tap.
- **Scanning**: `WifiManager.startScan()` works but is **throttled** — foreground
  apps 4 scans / 2 min, background 1 / 30 min — and needs Location permission.
  Usable to highlight which DB spots are actually in radio range.
- Legacy `WifiManager.addNetwork()` is deprecated; use the suggestion API.

**Android UX**: richer — can scan (throttled) to mark in-range spots and bulk-add
suggestions for auto-connect.

## Both platforms
Precise **Location permission** is the gatekeeper for nearly all WiFi features.

## Capacitor plugins
- [`@capgo/capacitor-wifi`](https://github.com/Cap-go/capacitor-wifi) (open source):
  read current SSID, connect; iOS + Android.
- Capawesome WiFi (premium): richer feature set.
- `@capacitor/geolocation`: GPS (web build falls back to `navigator.geolocation`,
  which is what our `internal`/`src/location.ts` and the e2e tests use).

> Our current app uses GPS + the API for discovery and leaves native connect as a
> per-platform integration (`@capgo/capacitor-wifi`). The desktop CLI does real
> scan/connect via `nmcli` / `networksetup` (see [cli.md](cli.md)).

## Sources
- [NEHotspotConfiguration — Apple Developer Forums](https://developer.apple.com/forums/thread/111307)
- [NEHotspotNetwork.fetchCurrent — Apple Developer Forums](https://developer.apple.com/forums/thread/670970)
- [Wi-Fi suggestion API — Android Developers](https://developer.android.com/develop/connectivity/wifi/wifi-suggest)
- [Save networks (ACTION_WIFI_ADD_NETWORKS) — Android Developers](https://developer.android.com/develop/connectivity/wifi/wifi-save-network-passpoint-config)
- [Wi-Fi scanning overview — Android Developers](https://developer.android.com/develop/connectivity/wifi/wifi-scan)
- [@capgo/capacitor-wifi](https://github.com/Cap-go/capacitor-wifi)
