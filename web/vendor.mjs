// Copies the Leaflet + markercluster dist files (and Leaflet's marker images)
// from node_modules into internal/web/static/vendor so the public web serves
// every asset same-origin — no unpkg/jsdelivr at runtime, which is what makes
// the page cacheable for offline use. Also stamps the service worker's
// CACHE_VERSION with a hash of the shell assets, so cache invalidation is
// automatic on any shell change (no manual version bumps).
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "internal", "web", "static", "vendor");
mkdirSync(out, { recursive: true });

const files = [
  ["leaflet/dist/leaflet.css", "leaflet.css"],
  ["leaflet/dist/leaflet.js", "leaflet.js"],
  ["leaflet.markercluster/dist/leaflet.markercluster.js", "leaflet.markercluster.js"],
  ["leaflet.markercluster/dist/MarkerCluster.css", "MarkerCluster.css"],
  ["leaflet.markercluster/dist/MarkerCluster.Default.css", "MarkerCluster.Default.css"],
];
for (const [src, dst] of files) {
  cpSync(join(here, "node_modules", src), join(out, dst));
}
// leaflet.css references ./images/* for default markers and controls.
cpSync(join(here, "node_modules", "leaflet/dist/images"), join(out, "images"), { recursive: true });

// App icons (PWA manifest + og:image) live in web/icons (source-controlled).
cpSync(join(here, "icons"), join(out, "..", "icons"), { recursive: true });

// PWA: manifest + service worker (served by Go at /sw.js and
// /manifest.webmanifest with no-cache headers — see internal/web/web.go).
cpSync(join(here, "manifest.webmanifest"), join(out, "..", "manifest.webmanifest"));

// Stamp the SW cache version with a hash over the shell contents: the compiled
// CSS, every vendored file, and the worker source itself. Any change to any of
// them produces a new cache name, so old shells are dropped on activation.
const staticRoot = join(out, "..");
const hash = createHash("sha256");
for (const f of [
  join(staticRoot, "app.css"),
  join(out, "leaflet.css"),
  join(out, "leaflet.js"),
  join(out, "leaflet.markercluster.js"),
  join(out, "MarkerCluster.css"),
  join(out, "MarkerCluster.Default.css"),
  join(here, "sw.js"),
]) {
  hash.update(readFileSync(f));
}
const stamp = hash.digest("hex").slice(0, 12);
const sw = readFileSync(join(here, "sw.js"), "utf8").replace("__BUILD_HASH__", stamp);
writeFileSync(join(staticRoot, "sw.js"), sw);
console.log(`vendored leaflet assets + icons + sw (shell ${stamp}) → ${out}`);
