// CommonJS config (Node 19 in this environment doesn't engage Playwright's
// TS-ESM config loader; specs stay .ts — the runner transpiles those).
const { defineConfig, devices } = require('@playwright/test');

const PORT = process.env.E2E_PORT || '8099';
const baseURL = `http://localhost:${PORT}`;

module.exports = defineConfig({
  testDir: './tests',
  fullyParallel: false, // scoring/scoreboard tests share server-side solve state
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : 'list',
  use: {
    baseURL,
    trace: 'on-first-retry',
    viewport: { width: 1024, height: 768 },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'bash ./serve.sh',
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
