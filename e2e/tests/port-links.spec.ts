import { test, expect } from '@playwright/test';

// Exposed-port links & exercise demo pairing — the writing-tutorials.md
// contract. No cluster needed: the rewrite runs client-side from the nodes[]
// state, so the tests stub a booted node and call rewritePortLinks() (the
// exact production function) directly. serve.sh runs the server with
// --router-host e2e.direct.test.

// Stub one booted node per terminal, then run the production rewrite.
async function rewriteWithStubbedNodes(page) {
  await page.evaluate(async () => {
    // @ts-ignore — top-level lesson-page script bindings
    nodes.forEach((n, i) => { n.pod = `i-aabbcc00${i}`; n.ip = `10.244.9.${i + 1}`; });
    // @ts-ignore
    await rewritePortLinks();
  });
}

test.describe('exercise demo button', () => {
  test('the "Test Exercise" button always renders and adopts the authored mark routing', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');

    const challenge = await page.locator('.exercise').getAttribute('data-challenge');
    const btn = page.locator('a.exercise-demo');
    // The submit button is present, carries the challenge hash, and adopted
    // the authored port 8888 (not the port-80 default).
    await expect(btn).toHaveCount(1);
    await expect(btn).toHaveText('Test Exercise');
    await expect(btn).toHaveAttribute('data-hash-code', challenge!);
    await expect(btn).toHaveAttribute('data-port', '8888');
    await expect(page.locator('a[data-port="80"]')).toHaveCount(0);
    // The authored inline "webserver" link survives as a plain preview link
    // (its own port link, no hash_code).
    const inline = page.locator('#exerciseDemo');
    await expect(inline).toHaveText('webserver');
    await expect(inline).toHaveAttribute('data-port', '8888');
    await expect(inline).not.toHaveAttribute('data-hash-code', /.*/);
  });

  test('a lesson with no exercise has no demo button', async ({ page }) => {
    await page.goto('/02-containers-quiz.html');
    await expect(page.locator('a.exercise-demo')).toHaveCount(0);
  });
});

test.describe('port link rewriting (once a session is up)', () => {
  test('the demo button rewrites to the adopted port/path and carries the verify params', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');
    const challenge = await page.locator('.exercise').getAttribute('data-challenge');
    await rewriteWithStubbedNodes(page);

    const href = await page.locator('a.exercise-demo').getAttribute('href');
    const url = new URL(href!);
    // Adopted data-port=8888 and the authored result-page path — NOT the
    // built-in 80//result.html.
    expect(url.host).toBe('ip10-244-9-1-iaabbcc000-8888.e2e.direct.test');
    expect(url.pathname).toBe('/03-fix-nginx-result.html');
    expect(url.searchParams.get('hash_code')).toBe(challenge);
    expect(url.searchParams.get('lessonsDomain')).toContain('http://localhost:');
  });

  test('data-host-prefix and data-protocol are honoured', async ({ page }) => {
    await page.goto('/01-docker-basics.html');
    await rewriteWithStubbedNodes(page);

    const href = await page.locator('a[data-host-prefix="app"]').getAttribute('href');
    // {:data-protocol="https:"} + {:data-host-prefix="app"} + port 8080.
    expect(href).toBe('https://app-ip10-244-9-1-iaabbcc000-8080.e2e.direct.test/');
  });

  test('re-running the rewrite is idempotent (reload/reattach path)', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');
    await rewriteWithStubbedNodes(page);
    const first = await page.locator('a.exercise-demo').getAttribute('href');
    await rewriteWithStubbedNodes(page);
    const second = await page.locator('a.exercise-demo').getAttribute('href');
    expect(second).toBe(first);
    // The adopted path must survive (not degrade to "/" of the full URL).
    expect(new URL(second!).pathname).toBe('/03-fix-nginx-result.html');
    expect(new URL(second!).searchParams.get('hash_code')).toBeTruthy();
  });

  test('before a session, clicking a plain port link is swallowed with a hint', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');
    // The inline authored link is a plain port link (no hash_code): it can't
    // go anywhere until a session is up, so the click is swallowed.
    await page.locator('#exerciseDemo').click();
    await expect(page).toHaveURL(/03-fix-nginx-exercise\.html$/); // no navigation
    await expect(page.locator('#tstatus')).toHaveText(/start a session first/i);
  });

  // This lesson declares exercise_expect:, so its button is graded
  // server-side — it never navigates, and reports next to the exercise.
  test('before a session, the server-verified button reports in the exercise verdict', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');
    const btn = page.locator('a.exercise-demo[data-verify]');
    await expect(btn).toHaveCount(1);
    await btn.click();
    await expect(page).toHaveURL(/03-fix-nginx-exercise\.html$/); // no navigation
    await expect(page.locator('.exercise .verdict')).toHaveText(/start a session first/i);
  });
});
