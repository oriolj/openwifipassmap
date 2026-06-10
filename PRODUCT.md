# OpenWifiPassMap

## Product purpose

A crowdsourced map of public WiFi spots (cafés, libraries, venues): network
name, password, location, notes, community 1-3★ quality ratings and speed
measurements. Everything is public by design; no encryption, no private vaults.

## Users

People out and about who need WiFi *right now*: travelers, remote workers,
students. They land on the public web page on a phone, often outdoors in
daylight, one-handed, and want the nearest decent connection in seconds.
Contributors are the same people a minute later: rate it, drop the password,
move on.

## Register

product

## Tone

Practical, friendly, zero ceremony. The map is the hero; chrome stays out of
the way. Emoji accents (📶 📍 ★) are part of the voice — lightweight, not cute.

## Brand / visual

- Tailwind + DaisyUI ("emerald" theme) via CDN on the public web (prototype
  stage; compile for production later).
- Quality palette is the one canonical signal: green #16a34a (great), amber
  #f59e0b (good), red #dc2626 (basic), gray #9ca3af (unrated). Defined in Go
  (internal/web/web.go qualityColors) and injected into the page; never fork it.
- Leaflet + OpenStreetMap tiles for the map.

## Anti-references

- Heavy SaaS dashboard chrome, cookie-cutter card grids.
- Anything that hides the map or password behind ceremony.

## Constraints

- All `data-testid` attributes are load-bearing (Playwright e2e).
- Spot fields are attacker-controllable: DOM building stays textContent-based.
- Mobile-first: the page must work as a one-handed phone tool.
