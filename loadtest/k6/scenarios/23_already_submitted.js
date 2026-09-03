// Test 25 — N candidates register, submit, then immediately try to
// register again with the same email. Every one must get 409
// "this email has already attempted the exam" — never a new session.
//
// Run: k6 run loadtest/k6/scenarios/23_already_submitted.js
// Override candidate count: VUS=300 k6 run ...
import { check } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam } from '../lib/setup.js';
import { registerCandidate, submitExam } from '../lib/api.js';
import { registerPayload } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    already_submitted_burst: {
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
  const { examId } = setupExam({ title: `LoadTest AlreadySubmitted ${Date.now()}` });
  return { examId };
}

export default function () {
  const payload = registerPayload(__VU);

  const res1 = registerCandidate(payload);
  const ok1 = checkOk(res1, 'registration', { extraChecks: { 'register: 201': (r) => r.status === 201 } });
  if (!ok1) return;
  const token = res1.json('token');

  checkOk(submitExam(token), 'normal', {
    extraChecks: { 'submit: status is grading': (r) => r.json('status') === 'grading' },
  });

  const res2 = registerCandidate(payload); // same email, after submission
  check(res2, {
    'already attempted: 409': (r) => r.status === 409,
    'already attempted: correct error message': (r) => (r.json('error') || '').includes('already attempted'),
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '23_already_submitted', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '23_already_submitted', [
    'checks',
    'success_registration',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
