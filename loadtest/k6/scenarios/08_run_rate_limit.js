// Test 8 — N candidates each fire 2 /me/run calls back-to-back with no
// delay (300 candidates x 2 = 600 requests). RunLimiter.acquire() checks the
// per-session rate limit BEFORE touching the concurrency semaphore, so the
// second call almost always 429s via the rate limiter specifically (not the
// global concurrency queue) — the assertion is simply that the immediate
// second call never succeeds outright, proving the per-session throttle is
// actually enforced under concurrent multi-candidate load, not just in
// isolation.
//
// Run: k6 run loadtest/k6/scenarios/08_run_rate_limit.js
// Override candidate count: VUS=300 k6 run ...
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    rate_limit_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '2m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest RunRateLimit ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');
  const payload = {
    questionId: coding.id,
    language: 'python',
    code: CODE_FIXTURES.python.correctSum,
    mode: 'samples',
  };

  const res1 = runCode(token, payload);
  checkOk(res1, 'normal', { isRunEndpoint: true });

  const res2 = runCode(token, payload); // immediate, no sleep — the point of this test
  checkOk(res2, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      'run rate-limit: immediate 2nd call never succeeds': (r) => r.status !== 200,
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '08_run_rate_limit', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '08_run_rate_limit', [
    'checks',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
