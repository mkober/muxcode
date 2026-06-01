#!/usr/bin/env node
// playwright-check.js — Headless browser check for console errors and warnings.
// Usage: node scripts/playwright-check.js <url> [--timeout <ms>] [--wait <ms>]
//
// Navigates to <url>, waits for page load + optional settle time, collects
// console errors/warnings and uncaught exceptions, outputs a JSON report.
//
// Requires: npx playwright install chromium (one-time setup)
//
// Exit codes:
//   0 — no errors or warnings found
//   1 — errors or warnings detected
//   2 — navigation or browser launch failed

const { chromium } = require('playwright');

function parseArgs(argv) {
  const args = { url: null, timeout: 15000, wait: 2000 };
  let i = 2; // skip node and script path
  while (i < argv.length) {
    if (argv[i] === '--timeout' && argv[i + 1]) {
      args.timeout = parseInt(argv[i + 1], 10);
      i += 2;
    } else if (argv[i] === '--wait' && argv[i + 1]) {
      args.wait = parseInt(argv[i + 1], 10);
      i += 2;
    } else if (!argv[i].startsWith('--')) {
      args.url = argv[i];
      i++;
    } else {
      i++;
    }
  }
  return args;
}

async function main() {
  const args = parseArgs(process.argv);

  if (!args.url) {
    console.error('Usage: node playwright-check.js <url> [--timeout <ms>] [--wait <ms>]');
    process.exit(2);
  }

  const errors = [];
  const warnings = [];
  const exceptions = [];
  let browser;

  try {
    browser = await chromium.launch({ headless: true });
    const context = await browser.newContext({
      // Suppress certificate errors for localhost dev servers
      ignoreHTTPSErrors: true,
    });
    const page = await context.newPage();

    // Collect console messages
    page.on('console', (msg) => {
      const type = msg.type();
      const text = msg.text();
      const location = msg.location();
      const entry = {
        text: text,
        url: location.url || '',
        line: location.lineNumber || 0,
      };

      if (type === 'error') {
        errors.push(entry);
      } else if (type === 'warning') {
        // Filter out common non-actionable Vite/browser warnings
        if (!text.includes('[vite] connecting...') &&
            !text.includes('DevTools') &&
            !text.includes('Download the React DevTools')) {
          warnings.push(entry);
        }
      }
    });

    // Collect uncaught exceptions
    page.on('pageerror', (error) => {
      exceptions.push({
        message: error.message,
        stack: (error.stack || '').split('\n').slice(0, 5).join('\n'),
      });
    });

    // Navigate to the URL
    const response = await page.goto(args.url, {
      timeout: args.timeout,
      waitUntil: 'networkidle',
    });

    const status = response ? response.status() : 0;

    // Wait for any async errors to surface
    if (args.wait > 0) {
      await page.waitForTimeout(args.wait);
    }

    await browser.close();
    browser = null;

    // Build report
    const report = {
      url: args.url,
      status: status,
      errors: errors,
      warnings: warnings,
      exceptions: exceptions,
      total_issues: errors.length + warnings.length + exceptions.length,
      checked_at: new Date().toISOString(),
    };

    console.log(JSON.stringify(report, null, 2));
    process.exit(report.total_issues > 0 ? 1 : 0);

  } catch (err) {
    // Navigation or browser launch failed
    if (browser) {
      await browser.close().catch(() => {});
    }

    const report = {
      url: args.url,
      status: 0,
      errors: [{ text: `Browser check failed: ${err.message}`, url: '', line: 0 }],
      warnings: [],
      exceptions: [],
      total_issues: 1,
      checked_at: new Date().toISOString(),
      launch_error: true,
    };

    console.log(JSON.stringify(report, null, 2));
    process.exit(2);
  }
}

main();
