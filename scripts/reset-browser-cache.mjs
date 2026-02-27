#!/usr/bin/env node

import { chromium } from 'playwright';

const LOCALHOST_URL = 'http://127.0.0.1:5173';

async function clearBrowserCache() {
  let browser;
  try {
    console.log('🌐 Launching browser to clear cache...');
    browser = await chromium.launch();
    const context = await browser.newContext();
    const page = await context.newPage();

    console.log(`📍 Navigating to ${LOCALHOST_URL}...`);
    await page.goto(LOCALHOST_URL, { waitUntil: 'networkidle' }).catch(() => {
      // Site might not be up, but we can still clear storage
      console.log('⚠️  Could not load page (site may be down), clearing storage anyway...');
    });

    // Clear localStorage and sessionStorage
    console.log('🧹 Clearing localStorage and sessionStorage...');
    await page.evaluate(() => {
      localStorage.clear();
      sessionStorage.clear();
    });

    // Clear cookies
    console.log('🍪 Clearing cookies...');
    await context.clearCookies();

    console.log('✅ Browser cache cleared successfully!');
    await browser.close();
    process.exit(0);
  } catch (error) {
    console.error('❌ Error clearing browser cache:', error.message);
    if (browser) await browser.close();
    process.exit(1);
  }
}

clearBrowserCache();
