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
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
  webServer: {
    command: 'go run ./cmd/uwas serve -c test/e2e/uwas-e2e.yaml --no-banner',
    cwd: '../..',
    url: 'http://127.0.0.1:19443/api/v1/health',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
