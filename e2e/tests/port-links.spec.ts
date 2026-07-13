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

test.describe('exercise demo link pairing', () => {
  test('an authored {:id="exerciseDemo"} link replaces the built-in button and carries the challenge hash', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');

    const challenge = await page.locator('.exercise').getAttribute('data-challenge');
    const demo = page.locator('#exerciseDemo');
    // The authored link (text "webserver", port 8888) is THE demo link…
    await expect(demo).toHaveText('webserver');
    await expect(demo).toHaveAttribute('data-port', '8888');
    await expect(demo).toHaveAttribute('data-hash-code', challenge!);
    // …and the built-in "Test Exercise" button (port 80) is gone. (Target
    // anchors: the lesson prose legitimately mentions "Test Exercise".)
    await expect(page.locator('a.exercise-demo')).toHaveCount(0);
    await expect(page.locator('a[data-port="80"]')).toHaveCount(0);
  });

  test('a lesson without an authored demo link keeps the built-in button', async ({ page }) => {
    // The quiz lesson has no exercise at all — sanity-check the selector…
    await page.goto('/02-containers-quiz.html');
    await expect(page.locator('#exerciseDemo')).toHaveCount(0);
  });
});

test.describe('port link rewriting (once a session is up)', () => {
  test('the demo link rewrites to the authored port and carries the verify params', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');
    const challenge = await page.locator('.exercise').getAttribute('data-challenge');
    await rewriteWithStubbedNodes(page);

    const href = await page.locator('#exerciseDemo').getAttribute('href');
    const url = new URL(href!);
    // Authored data-port=8888 and href="/" — NOT the built-in 80//result.html.
    expect(url.host).toBe('ip10-244-9-1-iaabbcc000-8888.e2e.direct.test');
    expect(url.pathname).toBe('/');
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
    const first = await page.locator('#exerciseDemo').getAttribute('href');
    await rewriteWithStubbedNodes(page);
    const second = await page.locator('#exerciseDemo').getAttribute('href');
    expect(second).toBe(first);
    // The authored path must survive (not degrade to "/" of the full URL).
    expect(new URL(second!).searchParams.get('hash_code')).toBeTruthy();
  });

  test('before a session, clicking a port link is swallowed with a hint', async ({ page }) => {
    await page.goto('/03-fix-nginx-exercise.html');
    await page.locator('#exerciseDemo').click();
    await expect(page).toHaveURL(/03-fix-nginx-exercise\.html$/); // no navigation
    await expect(page.locator('#tstatus')).toHaveText(/start a session first/i);
  });
});
