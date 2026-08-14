import { defineConfig, devices } from '@playwright/test';

const localChromeChannel =
  process.env.PLAYWRIGHT_CHANNEL || (process.platform === 'win32' ? 'chrome' : undefined);

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global_setup.ts',
  timeout: 120_000,
  workers: 1,
  use: {
    baseURL: process.env.BPT_UI_URL || 'http://localhost:18080',
    trace: 'on-first-retry'
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        ...(localChromeChannel ? { channel: localChromeChannel } : {})
      }
    }
  ]
});
