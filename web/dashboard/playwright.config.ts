import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  retries: 0,
  use: {
    headless: true,
    viewport: { width: 1280, height: 720 },
  },
  projects: [
    // Use the system-installed Chrome (channel: 'chrome') instead of
    // Playwright's bundled Chromium. This avoids downloading from
    // cdn.playwright.dev which is unreachable from some CI runners.
    { name: 'chromium', use: { browserName: 'chromium', channel: 'chrome' } },
  ],
  webServer: {
    // Use the pre-built binary if UWAS_BIN is set (CI), otherwise fall back to
    // `go run` for local development (recompiles on each launch).
    command: process.env.UWAS_BIN
      ? `${process.env.UWAS_BIN} serve -c test/e2e/uwas-e2e.yaml --no-banner`
      : 'go run ./cmd/uwas serve -c test/e2e/uwas-e2e.yaml --no-banner',
    cwd: '../..',
    url: 'http://127.0.0.1:19443/api/v1/health',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
