// Test 10 — N candidates run an infinite loop (rotating python/java/c),
// verifying: the request completes instead of hanging, the timeout is
// reported cleanly (not a silent failure/crash), and — after clearing the
// per-session rate limit — the SAME candidate's next run succeeds normally,
// proving the runner slot was released and Piston didn't get stuck.
//
// This test is inherently slow at scale: the fixture's timeLimitMs is 3000,
// so every candidate's first call alone takes ~3s, and at 300 candidates
// against the default RUN_CONCURRENCY=4 the queueing compounds significantly
// on top of that (300 candidates x 2 calls each, 4 concurrent slots) — a
// multi-minute run is expected, not a failure of the test.
//
// Run: k6 run loadtest/k6/scenarios/10_run_timeout.js
// Override: VUS=300 k6 run ...
import { sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

function languageFor(vu) {
  const langs = ['python', 'java', 'c'];
  return langs[(vu - 1) % 3];
}

export const options = {
  scenarios: {
    timeout_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '10m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest RunTimeout ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;
  const lang = languageFor(__VU);

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');

  const timeoutRes = runCode(token, {
    questionId: coding.id,
    language: lang,
    code: CODE_FIXTURES[lang].infiniteLoop,
    mode: 'samples',
  });
  checkOk(timeoutRes, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`timeout ${lang}: request completed, didn't hang`]: (r) => r.status === 200 || r.status === 429,
      [`timeout ${lang}: reported as a timeout result`]: (r) => {
        if (r.status !== 200) return true; // throttled — not what this assertion is checking
        const results = r.json('results') || [];
        return results.length > 0 && results.some((x) => x.status === 'timeout');
      },
    },
  });

  sleep(2.2); // clear RUN_RATE_LIMIT_SEC's default 2s window before checking recovery

  const recoverRes = runCode(token, {
    questionId: coding.id,
    language: lang,
    code: CODE_FIXTURES[lang].correctSum,
    mode: 'samples',
  });
  checkOk(recoverRes, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`recovery ${lang}: runner still healthy after a timeout`]: (r) =>
        r.status === 429 || r.json('compileOk') === true,
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '10_run_timeout', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '10_run_timeout', [
    'checks',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
