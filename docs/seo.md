# State of SEO

Living tracker. Update after every SEO-touching commit.

## ✅ Implemented

| Item | Where | Notes |
|---|---|---|
| Unique `<title>` per page | all templates | share pages: `<venue> WiFi — OpenWifiPassMap` |
| Per-page `<meta description>` | landing, share | share descriptions derive from venue/network/quality/speed, so each is distinct |
| `<link rel="canonical">` | landing, share | absolute; from `PUBLIC_BASE_URL` or forwarded headers |
| Open Graph + twitter:card | landing, share | `og:url` == canonical; `og:image` = app icon (512², `summary` card) |
| Geo meta (`geo.position`, `ICBM`) | share | lat/lng of the spot |
| JSON-LD | share | `Place` + `GeoCoordinates` + Free-WiFi `amenityFeature`; marshaled in Go with `</` escaping |
| `robots.txt` | `GET /robots.txt` | allows all, disallows `/api/`, `/reset`, `/verify`, `/admin`; blocks Bytespider/Diffbot/DataForSeoBot/PetalBot; advertises sitemap |
| `sitemap.xml` | `GET /sitemap.xml` | landing + every share page, `lastmod` from spot `updated_at` |
| `noindex` on utility pages | reset, verify | magic-link pages |
| Correct status codes | share | unknown spot → real 404 |
| `<html lang>` | all | `en` |
| Server-rendered content | share | password/details visible without JS — AI crawlers see everything |
| Compiled CSS (no CDN) | all | Core Web Vitals: no render-blocking third-party fetch |

## ❌ Not implemented (priority · effort)

| Item | Priority | Effort | Notes |
|---|---|---|---|
| Search Console + Bing Webmaster verification | high | ops | per canonical host, after launch |
| Purpose-made `og:image` (1200×630 per spot, map snippet) | med | med | current 512² icon gives a small `summary` card only |
| `WebSite` JSON-LD on landing | low | low | with `potentialAction` search once site search exists |
| IndexNow push on spot create | low | low | Bing/Copilot freshness |
| llms.txt | low | low | cheap hedge, no crawler fetches it organically yet |

## 🤔 Open questions

- Should spot pages link to nearby spots (internal linking → crawl depth)?
- City-level landing pages (`/city/barcelona`) once density justifies them — big SEO surface.
