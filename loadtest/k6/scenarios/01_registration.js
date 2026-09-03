// Test 1 — N candidates register against a freshly created, freshly
// activated exam, as close to simultaneously as k6 allows (a synchronized
// burst, not a paced scenario — see loadtest/README.md's VU convention).
//
// Run: k6 run loadtest/k6/scenarios/01_registration.js
// Override candidate count: VUS=300 k6 run ...  (default 10, for smoke tests)
//
// setup() creates its own exam (see lib/setup.js) so this is fully isolated
// and safe to re-run with the same student001@test.com.. emails every time.
import { sleep } from 'k6';
import { registerCandidate } from '../lib/api.js';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam } from '../lib/setup.js';
import { registerPayload } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    registration_burst: {
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
  const { examId } = setupExam({ title: `LoadTest Registration ${Date.now()}` });
  return { examId, vus: VUS };
}

export default function (data) {
  const n = __VU; // 1..VUS, per-vu-iterations pre-allocates all VUs together
  const res = registerCandidate(registerPayload(n));
  checkOk(res, 'registration', {
    extraChecks: {
      'registration: 201 (new session)': (r) => r.status === 201,
      'registration: resumed is false': (r) => r.json('resumed') === false,
      'registration: token present': (r) => !!r.json('token'),
      'registration: assigned to our exam': (r) => String(r.json('exam.id')) === String(data.examId),
    },
  });
  sleep(0); // no think-time — this is a deliberate synchronized burst
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '01_registration', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '01_registration', [
    'checks',
    'success_registration',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
