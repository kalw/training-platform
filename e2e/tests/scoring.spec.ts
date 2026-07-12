import { test, expect, request, APIRequestContext } from '@playwright/test';
import { createHash } from 'crypto';

// The salt serve.sh renders the site with. Flags are sha256(answer + salt);
// the browser never sees the salt — but a *forged* client can compute the
// flag for a known answer, which is exactly the point these tests make: the
// scoring channel VERIFIES outcomes, it does not PREVENT a determined client
// from submitting a correct answer / proof directly, bypassing the UI.
const SALT = 'e2e-salt';
const flagHash = (answer: string) => createHash('sha256').update(answer + SALT).digest('hex');

async function challengeHashes(api: APIRequestContext) {
  const res = await api.get('/api/v1/challenges');
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  const list: Array<{ hash: string; name: string; value: number }> = body.data;
  const quiz = list.find((c) => c.name.startsWith('quiz'))!;
  const exercise = list.find((c) => c.name.startsWith('exercise'))!;
  expect(quiz).toBeTruthy();
  expect(exercise).toBeTruthy();
  return { quiz, exercise, list };
}

test('challenge list never exposes flags', async ({ playwright, baseURL }) => {
  const api = await request.newContext({ baseURL });
  const res = await api.get('/api/v1/challenges');
  const raw = await res.text();
  expect(raw.toLowerCase()).not.toContain('flag');
  const { list } = await challengeHashes(api);
  for (const c of list) expect(c).not.toHaveProperty('flags');
  await api.dispose();
});

test('forged QUIZ attempt: correct hash posted directly is graded correct', async ({ playwright, baseURL }) => {
  const api = await request.newContext({ baseURL });
  const { quiz } = await challengeHashes(api);

  const correct = await api.post('/api/v1/challenges/attempt', {
    data: { challenge_hash: quiz.hash, submission: flagHash('docker ps') },
  });
  expect((await correct.json()).data.status).toBe('correct');

  const wrong = await api.post('/api/v1/challenges/attempt', {
    data: { challenge_hash: quiz.hash, submission: flagHash('docker ls') },
  });
  expect((await wrong.json()).data.status).toBe('incorrect');

  const unknown = await api.post('/api/v1/challenges/attempt', {
    data: { challenge_hash: 'deadbeef', submission: 'x' },
  });
  expect(unknown.status()).toBe(404);
  await api.dispose();
});

test('forged EXERCISE attempt: a screenshot proof is graded by perceptual hash', async ({ page, baseURL }) => {
  const api = await request.newContext({ baseURL });
  const { exercise } = await challengeHashes(api);

  // Capture the expected result page exactly as a learner's browser would —
  // the reference flag was computed from this same page at build time, so the
  // dHash matches within threshold.
  await page.goto('/03-fix-nginx-result.html');
  const shot = await page.screenshot({ clip: { x: 0, y: 0, width: 1024, height: 768 } });
  const proof = 'data:image/png;base64,' + shot.toString('base64');

  const ok = await api.post('/api/v1/challenges/attempt', {
    data: { challenge_hash: exercise.hash, submission: proof },
  });
  expect((await ok.json()).data.status).toBe('correct');

  // A blank/garbage capture must not match.
  const bad = await api.post('/api/v1/challenges/attempt', {
    data: { challenge_hash: exercise.hash, submission: 'data:image/png;base64,Zm9v' },
  });
  expect((await bad.json()).data.status).toBe('incorrect');
  await api.dispose();
});

test('scoreboard reflects the forged solves', async ({ playwright, baseURL }) => {
  const api = await request.newContext({ baseURL });
  const board = await (await api.get('/api/v1/scoreboard')).json();
  const names: string[] = board.data.map((s: any) => s.challenge_name);
  expect(names.some((n) => n.startsWith('quiz'))).toBeTruthy();
  expect(names.some((n) => n.startsWith('exercise'))).toBeTruthy();
  await api.dispose();

  // And the HTML scoreboard page renders them.
  const page = await (await request.newContext()).get(`${baseURL}/scoreboard`);
  expect((await page.text()).toLowerCase()).toContain('recorded solves');
});
