// Test 23 — N candidates register, then immediately re-register with the
// SAME email (simulating a browser refresh mid-exam), concurrently across
// all candidates. Verifies: resumed:true, same token, same endsAt, same
// assigned set, same startedAt — the resumed session is genuinely the same
// one, not a fresh one.
//
// Run: k6 run loadtest/k6/scenarios/21_resume.js
// Override candidate count: VUS=300 k6 run ...
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam } from '../lib/setup.js';
import { registerCandidate, getMe } from '../lib/api.js';
import { registerPayload } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    resume_burst: {
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
  const { examId } = setupExam({ title: `LoadTest Resume ${Date.now()}` });
  return { examId };
}

export default function () {
  const payload = registerPayload(__VU);

  const res1 = registerCandidate(payload);
  const ok1 = checkOk(res1, 'registration', {
    extraChecks: {
      'first register: 201 (new session)': (r) => r.status === 201,
      'first register: resumed is false': (r) => r.json('resumed') === false,
    },
  });
  if (!ok1) return;
  const token1 = res1.json('token');
  const me1 = getMe(token1);
  checkOk(me1, 'normal');

  const res2 = registerCandidate(payload); // simulated refresh — same email
  checkOk(res2, 'registration', {
    extraChecks: {
      'resume: 200 (not a new session)': (r) => r.status === 200,
      'resume: resumed is true': (r) => r.json('resumed') === true,
      'resume: same token as before': (r) => r.json('token') === token1,
      // Compare parsed instants, not raw strings: endsAt is serialized in
      // UTC ("...Z") on the fresh-registration path but in the server's
      // local offset ("...+05:30") on the resume path — same underlying
      // instant, different (both valid) RFC3339 representations. A naive
      // string comparison would wrongly flag every resume as "different."
      'resume: same endsAt as before (same instant, format may differ)': (r) =>
        new Date(r.json('endsAt')).getTime() === new Date(res1.json('endsAt')).getTime(),
    },
  });

  const me2 = getMe(res2.json('token'));
  checkOk(me2, 'normal', {
    extraChecks: {
      'resume: same assigned set': (r) => r.json('setLabel') === me1.json('setLabel'),
      'resume: same startedAt (clock did not reset)': (r) => r.json('startedAt') === me1.json('startedAt'),
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '21_resume', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '21_resume', [
    'checks',
    'success_registration',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
