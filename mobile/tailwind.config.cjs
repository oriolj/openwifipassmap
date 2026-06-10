/** Compiled Tailwind+DaisyUI for the Capacitor/React app (replaces the CDN). */
module.exports = {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: {} },
  plugins: [require("daisyui")],
  daisyui: {
    themes: ["emerald"],
    logs: false,
  },
};
