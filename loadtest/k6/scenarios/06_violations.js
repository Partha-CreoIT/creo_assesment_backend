// Test 6 — two things in one synchronized burst:
//   1. "kind coverage": the first 8 VUs each report exactly one violation
//      kind once, asserting the strike/logged-only classification is
//      correct for every kind (models.StrikeKinds / LoggedKinds).
//   2. "three-strike auto-submit": every remaining VU sends exactly 3
//      strike-kind violations and asserts violationCount/autoSubmitted at
//      each step, then confirms the session is auto-finalized
//      (submitKind: auto_violation) — this is where most VUs land at real
//      scale (300), which also stress-tests the auto-submit path under
//      concurrency, not just its correctness for one candidate.
//
// Run: k6 run loadtest/k6/scenarios/06_violations.js
// Override candidate count: VUS=300 k6 run ...  (needs VUS >= 8 for full kind coverage)
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { reportViolation, getMe } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

const STRIKE_KINDS = ['tab_hidden', 'window_blur', 'fullscreen_exit'];
const LOGGED_KINDS = ['copy', 'paste', 'cut', 'contextmenu', 'reload'];
const ALL_KINDS = [...STRIKE_KINDS, ...LOGGED_KINDS]; // 8 kinds total

export const options = {
  scenarios: {
    violations_burst: {
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
  const { examId } = setupExam({ title: `LoadTest Violations ${Date.now()}`, maxViolations: 3 });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

function kindCoverage(token, kind) {
  const isStrike = STRIKE_KINDS.includes(kind);
  const res = reportViolation(token, { kind, meta: 'k6 kind-coverage test' });
  checkOk(res, 'normal', {
    extraChecks: {
      [`violation ${kind}: strike flag is ${isStrike}`]: (r) => r.json('strike') === isStrike,
      [`violation ${kind}: violationCount is ${isStrike ? 1 : 0}`]: (r) => r.json('violationCount') === (isStrike ? 1 : 0),
      [`violation ${kind}: not auto-submitted`]: (r) => r.json('autoSubmitted') === false,
    },
  });
}

function threeStrikeAutoSubmit(token) {
  for (let i = 0; i < 3; i++) {
    const kind = STRIKE_KINDS[i];
    const expectAutoSubmit = i === 2; // maxViolations: 3
    const res = reportViolation(token, { kind, meta: 'k6 three-strike test' });
    checkOk(res, 'normal', {
      extraChecks: {
        [`3-strike round ${i + 1}: violationCount is ${i + 1}`]: (r) => r.json('violationCount') === i + 1,
        [`3-strike round ${i + 1}: autoSubmitted is ${expectAutoSubmit}`]: (r) =>
          r.json('autoSubmitted') === expectAutoSubmit,
      },
    });
  }
  const meRes = getMe(token);
  checkOk(meRes, 'normal', {
    extraChecks: {
      '3-strike: session no longer active': (r) => r.json('status') !== 'active',
      '3-strike: submitKind is auto_violation': (r) => r.json('submitKind') === 'auto_violation',
    },
  });
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  if (__VU <= ALL_KINDS.length) {
    kindCoverage(token, ALL_KINDS[__VU - 1]);
  } else {
    threeStrikeAutoSubmit(token);
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '06_violations', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '06_violations', [
    'checks',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
