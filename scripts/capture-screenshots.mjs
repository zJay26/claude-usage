import { chromium } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Published screenshots use only synthetic data, never local transcripts.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const destination = path.join(root, 'docs', 'images');
await mkdir(destination, { recursive: true });
const server = spawn(process.execPath, ['scripts/serve-static.mjs', 'dist/pages', '43219', 'claude-usage'], { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'inherit'] });
await once(server.stdout, 'data');
const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: {width:1440,height:1000}, colorScheme:'light', reducedMotion:'reduce' });
  await page.clock.setFixedTime(new Date('2026-09-23T12:30:00Z'));
  await page.goto('http://127.0.0.1:43219/claude-usage/?lang=zh-CN', {waitUntil:'networkidle'});
  await page.screenshot({path:path.join(destination,'dashboard.png'),fullPage:true});
  await page.locator('.primary-nav [data-view="daily"]').click();
  await page.screenshot({path:path.join(destination,'daily.png'),fullPage:true});
  await page.locator('.primary-nav [data-view="details"]').click();
  await page.screenshot({path:path.join(destination,'details.png'),fullPage:true});
  await page.locator('.primary-nav [data-view="overview"]').click();
  await page.locator('#sourcesButton').click();
  await page.locator('#sourceList .source-row').first().waitFor();
  await page.screenshot({path:path.join(destination,'sources.png')});
  await page.keyboard.press('Escape');
  await page.goto('http://127.0.0.1:43219/claude-usage/?lang=en', {waitUntil:'networkidle'});
  await page.screenshot({path:path.join(destination,'dashboard-en.png'),fullPage:true});
  await page.locator('#themeButton').click();
  await page.screenshot({path:path.join(destination,'dashboard-dark.png'),fullPage:true});
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:path.join(destination,'dashboard-mobile.png'),fullPage:true});
  await page.locator('#settingsButton').click();
  await page.locator('label:has([data-setting="theme"][value="light"])').click();
  await page.keyboard.press('Escape');
  await page.screenshot({path:path.join(destination,'dashboard-mobile-light.png'),fullPage:true});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)) throw new Error('Mobile page overflow');
  console.log(`Synthetic screenshots written to ${destination}`);
} finally { await browser.close(); server.kill(); await once(server,'exit'); }
