import { test, expect } from '@playwright/test';

const BASE = 'http://127.0.0.1:19443';
const DASHBOARD = `${BASE}/_uwas/dashboard`;
const API_KEY = 'e2e-test-key';

async function login(page: import('@playwright/test').Page) {
  await page.goto(`${DASHBOARD}/login`);
  await page.fill('input[type="password"]', API_KEY);
  await page.click('button[type="submit"]');
  // Wait for navigation to dashboard (with or without trailing slash)
  await page.waitForURL(/\/_uwas\/dashboard\/?$/, { timeout: 5000 });
}

test.describe('UWAS Dashboard', () => {
  test('login page loads', async ({ page }) => {
    await page.goto(`${DASHBOARD}/login`);
    await expect(page.locator('text=UWAS')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toBeVisible();
  });

  test('login with API key', async ({ page }) => {
    await login(page);
    await expect(page.locator('text=Total Requests')).toBeVisible({ timeout: 5000 });
  });

  test('dashboard shows stats', async ({ page }) => {
    await login(page);
    await expect(page.locator('text=Total Requests')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('text=Cache Hit Rate')).toBeVisible();
  });

  test('domains page', async ({ page }) => {
    await login(page);
    await page.click('a[href*="domains"]');
    await page.waitForURL(/\/domains/, { timeout: 5000 });
    await expect(page.locator('table')).toBeVisible({ timeout: 5000 });
  });

  test('cache page', async ({ page }) => {
    await login(page);
    await page.click('a[href*="cache"]');
    await page.waitForURL(/\/cache/, { timeout: 5000 });
  });

  test('logs page', async ({ page }) => {
    await login(page);
    await page.click('a[href*="logs"]');
    await page.waitForURL(/\/logs/, { timeout: 5000 });
  });

  test('settings page', async ({ page }) => {
    await login(page);
    await page.click('a[href*="settings"]');
    await page.waitForURL(/\/settings/, { timeout: 5000 });
  });

  test('topology page', async ({ page }) => {
    await login(page);
    await page.click('a[href*="topology"]');
    await page.waitForURL(/\/topology/, { timeout: 5000 });
    await expect(page.locator('.react-flow')).toBeVisible({ timeout: 5000 });
  });

  test('unauthorized redirects to login', async ({ page }) => {
    await page.goto(`${DASHBOARD}/`);
    await page.waitForURL(/\/login/, { timeout: 5000 });
  });
});
