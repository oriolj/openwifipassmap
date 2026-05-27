// Location helper. On a native Capacitor device this uses the Geolocation
// plugin; in the browser (dev / web build) it falls back to the standard
// navigator.geolocation API, which is what Playwright drives in tests.

import { Capacitor } from "@capacitor/core";

export interface Coords {
  lat: number;
  lng: number;
}

export async function getCurrentPosition(): Promise<Coords> {
  if (Capacitor.isNativePlatform()) {
    const { Geolocation } = await import("@capacitor/geolocation");
    const pos = await Geolocation.getCurrentPosition({ enableHighAccuracy: true });
    return { lat: pos.coords.latitude, lng: pos.coords.longitude };
  }
  return new Promise<Coords>((resolve, reject) => {
    if (!navigator.geolocation) {
      reject(new Error("Geolocation is not supported by this browser"));
      return;
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => resolve({ lat: pos.coords.latitude, lng: pos.coords.longitude }),
      (err) => reject(new Error(err.message)),
      { enableHighAccuracy: true, timeout: 10000 },
    );
  });
}
