// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds shop and gift
// cards, this week's tickets, a trip with boarding passes and the family's ID papers):
//
//   DATA_DIR=/tmp/wallet-demo DEMO_DATA=true ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME=Amar PORT=18308 ./pocket-wallet
//   CDP=http://127.0.0.1:9335 BASE=http://127.0.0.1:18308 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18308'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1200, frozen = true) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  // headless Chrome freezes CSS fade-ins: show everything at once
  if (frozen) await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
async function idOf(page, name) {
  return page.evaluate(n => fetch('/api/v1/search?q=' + encodeURIComponent(n)).then(r => r.json()).then(l => l[0].id), name)
}

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/', 1800)
  const html = await page.content()
  assert.ok(html.includes('Coming up') && html.includes('Konzum Klub') && html.includes('Runs out soon'), 'the wallet shows passes, cards and what runs out')
  await shot(page, 'desktop-wallet')
  const flight = await idOf(page, 'OS 784')
  await go(page, `/cards/${flight}`, 1500)
  assert.ok((await page.content()).includes('14C'), 'a boarding pass shows its seat')
  await shot(page, 'desktop-pass')
  await go(page, '/passes')
  await shot(page, 'desktop-passes')
  await go(page, '/ids')
  assert.ok((await page.content()).includes('•••• 029'), 'document numbers are hidden')
  await shot(page, 'desktop-ids')
  const gift = await idOf(page, 'BBI Centar')
  await go(page, `/cards/${gift}`)
  await shot(page, 'desktop-gift')
  for (const p of ['/cards', '/archive', '/settings/apps', '/settings/notifications', '/settings/general', '/settings/data', '/settings/ai', '/settings/about']) await go(page, p, 400)
  // the add dialog, with the barcode preview
  await go(page, '/cards')
  await page.click('text=Add a card')
  await page.fill('input[placeholder="Konzum Klub"]', 'Tesco Clubcard')
  await page.fill('#card-form input.mono', withEAN('500000123456'))
  await page.click('text=No barcode')
  await page.click('.sel-opt:has-text("EAN-13")')
  await page.waitForTimeout(1200)
  assert.ok(await page.isVisible('.barcode-preview img'), 'the form draws the barcode')
  // headless Chrome cannot capture the drawn preview at 2×; this one is taken at 1×
  if (shots) await page.screenshot({ path: dir + 'desktop-add.png', scale: 'css' })
  await ctx.close()
}

function withEAN(d) {
  let sum = 0
  for (let i = d.length - 1; i >= 0; i--) sum += Number(d[i]) * ((d.length - 1 - i) % 2 === 0 ? 3 : 1)
  return d + ((10 - sum % 10) % 10)
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/', 1800)
  await shot(page, 'phone-wallet')
  await page.click('.ctile-main >> nth=0')
  await page.waitForTimeout(1200)
  assert.ok(await page.isVisible('.show .code-view img'), 'a tap shows the code full screen')
  await shot(page, 'phone-show')
  await page.keyboard.press('Escape')
  const flight = await idOf(page, 'OS 784')
  await go(page, `/cards/${flight}?show=1`, 1500)
  await shot(page, 'phone-boarding')
  await go(page, '/passes', 1000)
  await shot(page, 'phone-passes')
  await page.click('.fab')
  await page.waitForTimeout(400)
  await shot(page, 'phone-add')
  await ctx.close()
}
{
  const { ctx, page } = await session({ phone: true, dark: true })
  await go(page, '/cards', 1500)
  await shot(page, 'phone-cards-dark')
  const pass = await idOf(page, 'Passport')
  await go(page, `/cards/${pass}`, 1000)
  await shot(page, 'phone-passport-dark')
  await ctx.close()
}

await browser.close()
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('ok')
