import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server for the Capacitor web build. In native builds Capacitor loads the
// compiled dist/ bundle instead.
export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
  },
});
