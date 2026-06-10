/** Tailwind build for the server-rendered public web (internal/web/templates).
 * The templates contain both markup and inline JS that builds DOM with class
 * strings, so scanning the .html files covers everything. */
module.exports = {
  content: ["../internal/web/templates/*.html"],
  theme: { extend: {} },
  plugins: [require("daisyui")],
  daisyui: {
    themes: ["emerald"],
    logs: false,
  },
};
