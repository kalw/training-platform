import { test, expect } from '@playwright/test';

// The terminal needs a real Kubernetes cluster (it boots an instance Pod and
// bridges a WebSocket to pods/exec). Gate it on E2E_CLUSTER=1 so the rest of
// the suite runs in plain CI; run it against kind/k3s to exercise the console.
test.describe('console terminal (cluster-gated)', () => {
  test.skip(!process.env.E2E_CLUSTER, 'set E2E_CLUSTER=1 with a reachable cluster to run');

  test('boot a session and round-trip a command through the terminal', async ({ page, baseURL, request }) => {
    // Boot an ephemeral instance Pod.
    const res = await request.post('/api/v1/sessions', { data: { image: 'busybox:1.36' } });
    expect(res.status()).toBe(201);
    const pod = (await res.json()).pod as string;
    expect(pod).toMatch(/^i-[a-z0-9]+$/);

    // Drive the terminal WebSocket from the browser context.
    await page.goto('/');
    const output = await page.evaluate(async ({ base, pod }) => {
      const proto = base.startsWith('https') ? 'wss' : 'ws';
      const ws = new WebSocket(`${proto}://${location.host}/terminals/${pod}`);
      ws.binaryType = 'arraybuffer';
      const chunks: string[] = [];
      await new Promise<void>((resolve, reject) => {
        ws.onopen = () => ws.send(new TextEncoder().encode('echo e2e-terminal-ok\n'));
        ws.onmessage = (e) => {
          chunks.push(typeof e.data === 'string' ? e.data : new TextDecoder().decode(e.data));
          if (chunks.join('').includes('e2e-terminal-ok')) resolve();
        };
        ws.onerror = () => reject(new Error('ws error'));
        setTimeout(() => resolve(), 8000);
      });
      ws.close();
      return chunks.join('');
    }, { base: baseURL!, pod });

    expect(output).toContain('e2e-terminal-ok');

    // Clean up the Pod.
    await request.delete(`/api/v1/sessions/${pod}`);
  });
});
