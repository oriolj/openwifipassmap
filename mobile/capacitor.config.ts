import type { CapacitorConfig } from "@capacitor/cli";

const config: CapacitorConfig = {
  appId: "com.oriolj.wifispots",
  appName: "WiFi Spots",
  webDir: "dist",
  server: {
    androidScheme: "https",
  },
};

export default config;
