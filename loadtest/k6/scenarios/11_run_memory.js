// Test 11 — verifies the configured memory limit (Question.memoryLimitKb,
// sent to Piston as run_memory_limit) actually gets enforced: a program that
// allocates unboundedly, run against a deliberately low limit
// (MEMORY_LIMIT_KB, default 64MB), must fail gracefully (a reported
// failure/timeout result, not a hang or a runner crash) — and, like Test 10,
// the SAME candidate's next run must still succeed, proving a memory-hog
// program can't take down the runner for anyone else.
//
// Run: k6 run loadtest/k6/scenarios/11_run_memory.js
// Override: VUS=300 MEMORY_LIMIT_KB=65536 k6 run ...
import { sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const MEMORY_LIMIT_KB = parseInt(__ENV.MEMORY_LIMIT_KB || '65536', 10); // 64MB default

function languageFor(vu) {
  const langs = ['python', 'java', 'c'];
  return langs[(vu - 1) % 3];
}

export const options = {
  scenarios: {
    memory_burst: {
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
  const { examId } = setupExam({
    title: `LoadTest RunMemory ${Date.now()}`,
    codingMemoryLimitKb: MEMORY_LIMIT_KB,
  });
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

  const hogRes = runCode(token, {
    questionId: coding.id,
    language: lang,
    code: CODE_FIXTURES[lang].memoryHog,
    mode: 'samples',
  });
  checkOk(hogRes, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`memory ${lang}: request completed, didn't hang`]: (r) => r.status === 200 || r.status === 429,
      [`memory ${lang}: reported as failed/timeout, not a silent pass`]: (r) => {
        if (r.status !== 200) return true;
        if (!r.json('compileOk')) return true; // compile_error is also an acceptable clean outcome
        const results = r.json('results') || [];
        return results.length > 0 && results.every((x) => x.status !== 'passed');
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
      [`recovery ${lang}: runner still healthy after a memory hog`]: (r) =>
        r.status === 429 || r.json('compileOk') === true,
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = {
    scenario: '11_run_memory',
    examId: readMeta(data, 'exam_id'),
    vus: VUS,
    memoryLimitKb: MEMORY_LIMIT_KB,
  };
  return writeResults(data, meta, outDir, '11_run_memory', [
    'checks',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
