import { test, expect } from '@playwright/test';

const BASE = 'http://127.0.0.1:19443';
const DASHBOARD = `${BASE}/_uwas/dashboard`;
const API_KEY = 'e2e-test-key';

async function login(page: import('@playwright/test').Page) {
  await page.goto(`${DASHBOARD}/login`);
  await page.fill('input[type="password"]', API_KEY);
  await page.click('button[type="submit"]');
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible({ timeout: 5000 });
  await expect(page.getByRole('button', { name: /Logout/ })).toBeVisible();
}

async function openNav(page: import('@playwright/test').Page, group: string, label: string, url: RegExp) {
  await page.getByRole('button', { name: group }).click();
  await page.getByRole('link', { name: label }).click();
  await page.waitForURL(url, { timeout: 5000 });
}

test.describe('UWAS Dashboard', () => {
  test('login page loads', async ({ page }) => {
    await page.goto(`${DASHBOARD}/login`);
    await expect(page.locator('text=UWAS')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toBeVisible();
  });

  test('login with API key', async ({ page }) => {
    await login(page);
    await expect(page.getByText('Requests', { exact: true }).first()).toBeVisible({ timeout: 5000 });
  });

  test('dashboard shows stats', async ({ page }) => {
    await login(page);
    await expect(page.getByText('Requests', { exact: true }).first()).toBeVisible({ timeout: 5000 });
    await expect(page.getByText('Cache Hit', { exact: true })).toBeVisible();
  });

  test('domains page', async ({ page }) => {
    await login(page);
    await openNav(page, 'Sites', 'Domains', /\/domains/);
    await expect(page.getByRole('heading', { name: /Domains/ })).toBeVisible({ timeout: 5000 });
  });

  test('cache page', async ({ page }) => {
    await login(page);
    await openNav(page, 'Performance', 'Cache', /\/cache/);
  });

  test('logs page', async ({ page }) => {
    await login(page);
    await openNav(page, 'Performance', 'Logs', /\/logs/);
  });

  test('settings page', async ({ page }) => {
    await login(page);
    await openNav(page, 'System', 'Settings', /\/settings/);
  });

  test('topology page', async ({ page }) => {
    await login(page);
    await openNav(page, 'Sites', 'Topology', /\/topology/);
    await expect(page.locator('.react-flow')).toBeVisible({ timeout: 5000 });
  });

  test('unauthorized redirects to login', async ({ page }) => {
    await page.goto(`${DASHBOARD}/`);
    await page.waitForURL(/\/login/, { timeout: 5000 });
  });

  test('system endpoint exposes container runtime fields', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/system`, {
      headers: { Authorization: `Bearer ${API_KEY}` },
    });
    expect(resp.ok()).toBeTruthy();
    const sys = await resp.json();
    // container field must be present and a known value
    expect(['none', 'docker', 'lxc', 'kubernetes']).toContain(sys.container);
    // non_root is a boolean
    expect(typeof sys.non_root).toBe('boolean');
  });
});
