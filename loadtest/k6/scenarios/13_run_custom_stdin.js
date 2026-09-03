// Test 13 — N candidates each exercise one custom-stdin shape (rotating
// through empty / one-line / multi-line / large-but-reasonable / malformed
// / deliberately-oversized), verifying: valid input produces the correct
// output, malformed input fails gracefully without hanging, and oversized
// input (>10000 chars) is REJECTED with 400 rather than silently accepted —
// i.e. custom stdin can't be used to bypass the size limit.
//
// Run: k6 run loadtest/k6/scenarios/13_run_custom_stdin.js
// Override candidate count: VUS=300 k6 run ...
import { check } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

const VARIANTS = [
  { kind: 'empty', stdin: '' },
  { kind: 'one_line', stdin: '5 7', expectedSum: 12 },
  { kind: 'multi_line', stdin: '3 4\n99 99\n', expectedSum: 7 }, // program only reads the first line
  { kind: 'large_reasonable', stdin: '8 9\n' + 'x'.repeat(4000), expectedSum: 17 },
  { kind: 'malformed', stdin: 'abc def' },
  { kind: 'oversized', stdin: 'x'.repeat(10001) }, // server caps custom stdin at 10000 chars
];

function variantFor(vu) {
  return VARIANTS[(vu - 1) % VARIANTS.length];
}

export const options = {
  scenarios: {
    custom_stdin_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '3m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest CustomStdin ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;
  const variant = variantFor(__VU);

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');

  const runPayload = {
    questionId: coding.id,
    language: 'python',
    code: CODE_FIXTURES.python.correctSum,
    mode: 'custom',
    stdin: variant.stdin,
  };

  if (variant.kind === 'oversized') {
    // Deliberately NOT routed through checkOk/classify: a 400 here is the
    // correct, expected outcome (the limit held), not a failure — running it
    // through the happy-path classifier would wrongly count it as an error.
    const res = runCode(token, runPayload);
    check(res, {
      'oversized stdin: rejected with 400, limit not bypassed': (r) => r.status === 400,
    });
    return;
  }

  const res = runCode(token, runPayload);
  checkOk(res, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`${variant.kind}: completed without hanging`]: (r) => r.status === 200 || r.status === 429,
      [`${variant.kind}: correct behavior`]: (r) => {
        if (r.status !== 200) return true;
        if (variant.expectedSum === undefined) return r.json('timedOut') === false; // malformed/empty: just must not hang
        return r.json('stdout').trim() === String(variant.expectedSum);
      },
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '13_run_custom_stdin', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '13_run_custom_stdin', [
    'checks',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
