import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';

const row = (page: Page, algo: string) => page.locator(`[data-testid=table] tr[data-algo="${algo}"]`);

test.beforeEach(async ({ page }) => {
  await page.goto('./');
  await expect(row(page, 'gcra')).toBeVisible({ timeout: 20_000 }); // WebAssembly загрузился и посчитал первый сценарий
});

test('пачки на границе окна: fixed-window вдвое превышает лимит, sliding-log нет', async ({ page }) => {
  await expect(row(page, 'fixed-window')).toContainText('×2,00');
  await expect(row(page, 'sliding-log')).toContainText('×1,00');
  await expect(row(page, 'fixed-window')).toContainText('600'); // принято 600 из 600
  await expect(page.getByTestId('chart').locator('svg')).toHaveAttribute('role', 'img');
});

test('token-bucket и gcra всегда показывают одинаковые числа', async ({ page }) => {
  for (const preset of ['Изъян fixed-window', 'Пачки', 'Случайный поток', 'Нарастающая нагрузка']) {
    await page.getByRole('button', { name: preset }).click();
    await page.waitForTimeout(250); // ввод обрабатывается с небольшой задержкой
    expect(await row(page, 'token-bucket').innerText(), preset).toBe((await row(page, 'gcra').innerText()).replace('gcra', 'token-bucket'));
  }
});

test('пресет меняет трафик и описание', async ({ page }) => {
  await page.getByRole('button', { name: 'Случайный поток' }).click();
  await expect(page.locator('#pattern')).toHaveValue('poisson');
  await expect(page.locator('#about')).toContainText('Пуассон');
  await expect(page.getByTestId('chart')).toContainText('Случайный поток');
});

test('результат воспроизводим при том же seed', async ({ page }) => {
  await page.getByRole('button', { name: 'Случайный поток' }).click();
  await page.waitForTimeout(250);
  const first = await page.getByTestId('table').innerText();
  await page.locator('#seed').fill('99');
  await page.waitForTimeout(250);
  const other = await page.getByTestId('table').innerText();
  await page.locator('#seed').fill('7');
  await page.waitForTimeout(250);
  expect(await page.getByTestId('table').innerText()).toBe(first);
  expect(other).not.toBe(first);
});

test('неверный ввод даёт понятную ошибку, исправление её снимает', async ({ page }) => {
  await page.locator('#limit').fill('0');
  await expect(page.locator('#err')).toBeVisible();
  await expect(page.locator('#err')).toContainText('limit');
  await page.locator('#limit').fill('100');
  await expect(page.locator('#err')).toBeHidden();
});

test('нет горизонтальной прокрутки на узком экране', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
});

for (const scheme of ['light', 'dark'] as const) {
  test(`доступность (axe), ${scheme === 'light' ? 'светлая' : 'тёмная'} тема`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    const check = async (label: string) => {
      const res = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
      const bad = res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
      expect(bad.map((v) => `${v.id}: ${v.nodes[0]?.html}`), label).toEqual([]);
    };
    await check('пресет по умолчанию');
    await page.getByRole('button', { name: 'Случайный поток' }).click();
    await page.waitForTimeout(250);
    await check('случайный поток');
    await page.locator('#limit').fill('0');
    await expect(page.locator('#err')).toBeVisible();
    await check('состояние ошибки');
  });
}
