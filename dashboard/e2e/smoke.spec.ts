import { expect, test } from '@playwright/test'

/**
 * 冒烟测试（不需要任何凭据）。
 *
 * 第 1 条是**回归护栏**：登录按钮曾经因为 Ant Design Vue 的 `<a-form>` 缺 `:model`
 * 而完全哑火 —— 点击后零请求、零报错、按钮也不进 loading，看着像后端挂了。
 * 当时所有验收都只打了 API（curl 返回 200），没有在浏览器里真点一次表单，
 * 所以这个缺陷从开发一直活到线上。
 *
 * 这里不验「能否登录成功」（那需要真密码），只验「表单是否真的会提交」：
 * 用假密码触发一次 401，只要请求发出且错误可见，就说明表单链路是活的。
 */
test('登录表单真的会发起请求并显示错误（防「表单哑火」回归）', async ({ page }) => {
  await page.goto('/login')

  const email = page.locator('input[type=email]')
  const password = page.locator('input[type=password]')
  await expect(email).toBeVisible()
  await expect(password).toBeVisible()

  await email.fill('smoke@example.test')
  await password.fill('definitely-not-the-real-password')

  // 关键断言 1：必须发出 POST /api/v1/auth/login
  const requestPromise = page.waitForRequest(
    (request) => request.url().includes('/api/v1/auth/login') && request.method() === 'POST',
    { timeout: 15_000 },
  )
  await page.locator('form button[type=submit]').click()
  const request = await requestPromise

  // 关键断言 2：假密码必须是 401（而不是 500 / 网络错误）
  const response = await request.response()
  expect(response?.status()).toBe(401)

  // 关键断言 3：错误必须可见（否则用户只会觉得「点了没反应」）
  await expect(page.locator('.ant-alert-error').first()).toBeVisible()

  // 关键断言 4：不能卡在 loading 出不来
  await expect(page.locator('form button[type=submit]')).toBeEnabled()
})

test('SPA 深链可直达且应用已挂载', async ({ page }) => {
  for (const path of ['/nodes', '/certificates', '/alerts', '/tokens', '/settings']) {
    const response = await page.goto(path)
    expect(response?.status(), `${path} 应返回 200`).toBe(200)
    await expect(page.locator('#app'), `${path} 应挂载应用`).toBeVisible()
  }
})

test('公开分发端点：存在的可取且带 sha256，不存在的 404 且不是 HTML', async ({ request }) => {
  const binary = await request.get('/downloads/neilico-agent-linux-amd64')
  expect(binary.status()).toBe(200)
  expect(binary.headers()['x-neilico-sha256']).toBeTruthy()

  const missing = await request.get('/downloads/neilico-agent-does-not-exist')
  expect(missing.status()).toBe(404)
  expect(await missing.text()).not.toContain('<html')

  const installScript = await request.get('/install.sh')
  expect(installScript.status()).toBe(200)
  expect(await installScript.text()).toContain('#!/bin/sh')

  const powershellScript = await request.get('/install.ps1')
  expect(powershellScript.status()).toBe(200)
  expect(await powershellScript.text()).toContain('New-Service')
})

test('健康检查报告数据库可用', async ({ request }) => {
  const response = await request.get('/healthz')
  expect(response.status()).toBe(200)
  const body = await response.json()
  expect(body.status).toBe('ok')
  expect(body.db).toBe('up')
})
