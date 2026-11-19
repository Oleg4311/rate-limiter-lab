import { defineConfig } from '@playwright/test';

// BASE_URL задаётся для проверки опубликованной версии, иначе тесты идут на локальной сборке (../site).
const live = process.env.BASE_URL;

export default defineConfig({
  testDir: 'tests',
  fullyParallel: true,
  reporter: [['list']],
  use: { baseURL: live ?? 'http://127.0.0.1:4177/' },
  webServer: live
    ? undefined
    : {
        command: 'node serve.mjs',
        url: 'http://127.0.0.1:4177/',
        reuseExistingServer: !process.env.CI,
        timeout: 60_000,
      },
});
