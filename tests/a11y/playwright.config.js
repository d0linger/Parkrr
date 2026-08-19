// @ts-check
const { defineConfig } = require('@playwright/test');

// Base URL of a running Parkrr instance. Start the app (e.g. docker compose up)
// and point PARKRR_BASE_URL at it, or rely on the default dev port.
module.exports = defineConfig({
  testDir: '.',
  timeout: 30000,
  // These are stateful integration tests hitting one shared backend, so heavy planner/seed tests can
  // transiently time out under parallel workers. Retry to absorb that contention (each test passes on
  // its own), and cap workers on CI where the runner is smaller.
  retries: 2,
  workers: process.env.CI ? 2 : undefined,
  use: {
    baseURL: process.env.PARKRR_BASE_URL || 'http://localhost:8080',
  },
});
