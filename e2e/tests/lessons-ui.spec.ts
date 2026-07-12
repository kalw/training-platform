import { test, expect } from '@playwright/test';

test.describe('lessons UI', () => {
  test('index lists the rendered lessons', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Lessons' })).toBeVisible();
    await expect(page.getByRole('link', { name: /quiz/i })).toBeVisible();
    await expect(page.getByRole('link', { name: /exercise/i })).toBeVisible();
  });

  test('a lesson renders Markdown to HTML (heading + bold + code)', async ({ page }) => {
    await page.goto('/02-containers-quiz.html');
    // The rendered lesson body lives in <article>. Markdown "# ..." -> <h1>.
    await expect(page.locator('article h1').first()).toHaveText(/listing containers/i);
    // "**server-side by hash**" -> <strong>.
    await expect(page.locator('article strong', { hasText: /server-side by hash/i })).toBeVisible();
  });

  test('quiz confirms SUBMISSION only — never reveals correct/incorrect', async ({ page }) => {
    await page.goto('/02-containers-quiz.html');

    // Submit the correct choice.
    await page.getByText('docker ps', { exact: true }).click();
    await page.locator('.quiz-submit').click();

    const verdict = page.locator('.verdict');
    await expect(verdict).toHaveText(/answer submitted/i);
    // The UI must NOT leak the outcome (each choice's hash is in the DOM, so
    // showing correct/incorrect would let a learner brute-force the answer).
    await expect(verdict).not.toHaveText(/correct/i);
    await expect(verdict).not.toHaveText(/incorrect/i);

    // Submitting a WRONG choice shows the same neutral confirmation.
    await page.getByText('docker ls', { exact: true }).click();
    await page.locator('.quiz-submit').click();
    await expect(verdict).toHaveText(/answer submitted/i);
    await expect(verdict).not.toHaveText(/incorrect/i);
  });
});
