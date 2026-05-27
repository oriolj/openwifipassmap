import type { CapacitorConfig } from "@capacitor/cli";

const config: CapacitorConfig = {
  appId: "com.oriolj.openwifipassmap",
  appName: "OpenWifiPassMap",
  webDir: "dist",
  server: {
    androidScheme: "https",
  },
};

export default config;
