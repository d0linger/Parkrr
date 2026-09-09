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
  // Seriell auf CI: mit zwei Workern nahm EINE verklemmte Phase (Pool-Engpass
  // waehrend eines schweren Nachbartests) gleich die ganze Strecke eines Workers
  // mit — export, icons und truncation fielen im Block, in der Wiederholung
  // wieder, und im naechsten Lauf war es ein anderer Block. Ein Worker kostet
  // rund zwei Minuten und macht die Laeufe vergleichbar.
  workers: process.env.CI ? 1 : undefined,
  use: {
    baseURL: process.env.PARKRR_BASE_URL || 'http://localhost:8080',
  },
});
