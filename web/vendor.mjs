// Copies the Leaflet + markercluster dist files (and Leaflet's marker images)
// from node_modules into internal/web/static/vendor so the public web serves
// every asset same-origin — no unpkg/jsdelivr at runtime, which is what makes
// the page cacheable for offline use.
import { cpSync, mkdirSync } from "node:fs";
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

// PWA: service worker + manifest (served by Go at /sw.js and
// /manifest.webmanifest with no-cache headers — see internal/web/web.go).
cpSync(join(here, "sw.js"), join(out, "..", "sw.js"));
cpSync(join(here, "manifest.webmanifest"), join(out, "..", "manifest.webmanifest"));
console.log(`vendored leaflet assets + icons + sw → ${out}`);
