import { defineConfig } from '@playwright/test'

// 目标默认是常驻部署；CI 里用 E2E_BASE_URL 覆盖。
const baseURL = process.env.E2E_BASE_URL || 'http://192.168.123.90:13000'

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL,
    // 用系统已安装的 Chrome，避免在 CI 里再下载浏览器。
    // 若某环境没有 Chrome，可删掉这一行并执行 `npx playwright install chromium`。
    channel: 'chrome',
    viewport: { width: 1920, height: 1080 },
    ignoreHTTPSErrors: true,
  },
})
