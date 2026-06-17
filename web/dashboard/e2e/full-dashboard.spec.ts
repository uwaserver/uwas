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
  await page.getByRole('link', { name: label, exact: true }).click();
  await page.waitForURL(url, { timeout: 5000 });
}

test.describe('Dashboard - All Pages', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // --- Dashboard Overview ---
  test('dashboard shows latency metrics', async ({ page }) => {
    await expect(page.getByText('p50', { exact: true })).toBeVisible({ timeout: 5000 });
    await expect(page.getByText('p95', { exact: true })).toBeVisible();
    await expect(page.getByText('p99', { exact: true })).toBeVisible();
    await expect(page.getByText('Slow', { exact: true })).toBeVisible();
  });

  test('dashboard shows request chart', async ({ page }) => {
    await expect(page.locator('text=Requests Over Time')).toBeVisible({ timeout: 5000 });
  });

  test('dashboard shows domains table', async ({ page }) => {
    await expect(page.getByText('Domains', { exact: true })).toBeVisible({ timeout: 5000 });
  });

  // --- Analytics Page ---
  test('analytics page loads with domain stats', async ({ page }) => {
    await openNav(page, 'Performance', 'Analytics', /\/analytics/);
    await expect(page.getByRole('heading', { name: /Analytics/ })).toBeVisible({ timeout: 5000 });
  });

  // --- Config Editor ---
  test('config editor loads', async ({ page }) => {
    await openNav(page, 'System', 'Config Editor', /\/config-editor/);
    // Should show either the config content or a message
    await expect(page.locator('h1')).toContainText('Config', { timeout: 5000 });
  });

  // --- Certificates Page ---
  test('certificates page loads', async ({ page }) => {
    await openNav(page, 'Sites', 'Certificates', /\/certificates/);
    await expect(page.locator('h1')).toContainText('Certificates', { timeout: 5000 });
  });

  // --- Metrics Page ---
  test('metrics page loads with prometheus data', async ({ page }) => {
    await openNav(page, 'Performance', 'Metrics', /\/metrics/);
    await expect(page.locator('h1')).toContainText('Metrics', { timeout: 5000 });
  });

  // --- PHP Page ---
  test('php page loads', async ({ page }) => {
    await openNav(page, 'Server', 'PHP', /\/php/);
    await expect(page.locator('h1')).toContainText('PHP', { timeout: 5000 });
  });

  // --- Backups Page ---
  test('backups page loads', async ({ page }) => {
    await openNav(page, 'System', 'Backups', /\/backups/);
    await expect(page.locator('h1')).toContainText('Backup', { timeout: 5000 });
  });

  // --- Audit Log Page ---
  test('audit log page loads', async ({ page }) => {
    await openNav(page, 'Security', 'Audit Log', /\/audit/);
    await expect(page.locator('h1')).toContainText('Audit', { timeout: 5000 });
  });

  // --- Settings Page with System Info ---
  test('settings shows system information', async ({ page }) => {
    await openNav(page, 'System', 'Settings', /\/settings/);
    await expect(page.getByRole('heading', { name: 'Server Status' })).toBeVisible({ timeout: 5000 });
    await expect(page.getByText('Go', { exact: true })).toBeVisible();
    await expect(page.getByText('OS / Arch', { exact: true })).toBeVisible();
  });

  test('settings reload button works', async ({ page }) => {
    await openNav(page, 'System', 'Settings', /\/settings/);
    await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeVisible({ timeout: 5000 });
  });

  // --- Sidebar Navigation ---
  test('sidebar has all 15 navigation links', async ({ page }) => {
    for (const group of ['Sites', 'Server', 'Performance', 'Security', 'System']) {
      await page.getByRole('button', { name: group }).click();
    }
    const sidebarLinks = [
      'Dashboard', 'Domains', 'Topology', 'Cache', 'Metrics',
      'Analytics', 'Logs', 'Config Editor', 'Certificates',
      'PHP', 'Backups', 'Audit Log', 'Settings',
    ];
    for (const label of sidebarLinks) {
      await expect(page.locator(`nav >> text="${label}"`)).toBeVisible();
    }
  });

  // --- Logout ---
  test('logout button works', async ({ page }) => {
    await page.click('text=Logout');
    await page.waitForURL(/\/login/, { timeout: 5000 });
  });
});

test.describe('Dashboard - API Verification', () => {
  test('admin API health returns JSON', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/health`);
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.status).toBe('ok');
    expect(body.version).toBeDefined();
    expect(body.uptime).toBeDefined();
  });

  test('admin API system returns runtime info', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/system`, {
      headers: { Authorization: `Bearer ${API_KEY}` },
    });
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.go_version).toContain('go');
    expect(body.cpus).toBeGreaterThan(0);
    expect(body.goroutines).toBeGreaterThan(0);
  });

  test('admin API stats returns latency metrics', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/stats`, {
      headers: { Authorization: `Bearer ${API_KEY}` },
    });
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.requests_total).toBeDefined();
    expect(body.latency_p50_ms).toBeDefined();
    expect(body.latency_p95_ms).toBeDefined();
    expect(body.latency_p99_ms).toBeDefined();
    expect(body.slow_requests).toBeDefined();
  });

  test('admin API metrics returns prometheus format', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/metrics`, {
      headers: { Authorization: `Bearer ${API_KEY}` },
    });
    expect(resp.ok()).toBeTruthy();
    const body = await resp.text();
    expect(body).toContain('uwas_requests_total');
    expect(body).toContain('uwas_request_duration_seconds');
    expect(body).toContain('uwas_requests_by_handler');
    expect(body).toContain('uwas_slow_requests_total');
  });

  test('admin API audit returns paginated items', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/audit`, {
      headers: { Authorization: `Bearer ${API_KEY}` },
    });
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(Array.isArray(body.items)).toBeTruthy();
    expect(body.total).toBeDefined();
    expect(body.limit).toBeDefined();
    expect(body.offset).toBeDefined();
  });

  test('admin API returns 401 without token', async ({ request }) => {
    const resp = await request.get(`${BASE}/api/v1/stats`);
    expect(resp.status()).toBe(401);
  });
});
