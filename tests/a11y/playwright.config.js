// @ts-check
const { defineConfig } = require('@playwright/test');

// Base URL of a running Parkrr instance. Start the app (e.g. docker compose up)
// and point PARKRR_BASE_URL at it, or rely on the default dev port.
module.exports = defineConfig({
  testDir: '.',
  timeout: 30000,
  use: {
    baseURL: process.env.PARKRR_BASE_URL || 'http://localhost:8080',
  },
});
