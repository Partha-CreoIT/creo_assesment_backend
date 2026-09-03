// Test 24 — TRUE concurrency, not sequential like scenario 21: each
// candidate fires 3 identical POST /sessions calls (same email) truly
// simultaneously via k6's http.batch(), from a single VU. Verifies every
// request resolves cleanly (200 or 201, never a 5xx or a lock timeout) and
// — via lib/verify's `registrations` check afterward — that exactly ONE
// exam_session row ends up existing per student despite 3 concurrent
// find-or-create attempts racing the DB's unique (exam_id, student_id) index.
//
// Run: k6 run loadtest/k6/scenarios/22_duplicate_registration_race.js
// Override candidate count: VUS=300 k6 run ...
import http from 'k6/http';
import { check } from 'k6';
import { BASELINE_THRESHOLDS, errorsUnexpected, errors5xx } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam } from '../lib/setup.js';
import { BASE_URL } from '../lib/api.js';
import { registerPayload } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    duplicate_registration_burst: {
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
  const { examId } = setupExam({ title: `LoadTest DuplicateRegistrationRace ${Date.now()}` });
  return { examId };
}

export default function () {
  const body = JSON.stringify(registerPayload(__VU));
  const req = { method: 'POST', url: `${BASE_URL}/api/v1/sessions`, body, params: { headers: { 'Content-Type': 'application/json' } } };

  // http.batch() fires all 3 requests concurrently over separate
  // connections — this is the actual race, unlike sequential calls.
  const responses = http.batch([req, req, req]);

  for (const res of responses) {
    if (res.status >= 500) errors5xx.add(1);
    if (![200, 201].includes(res.status)) errorsUnexpected.add(1);
  }
  const created = responses.filter((r) => r.status === 201).length;
  const resumed = responses.filter((r) => r.status === 200).length;

  check(responses, {
    'duplicate race: all 3 concurrent requests resolved cleanly (200/201, never 5xx)': (rs) =>
      rs.every((r) => r.status === 200 || r.status === 201),
    'duplicate race: exactly one created the session, the rest resumed it': () => created === 1 && resumed === 2,
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '22_duplicate_registration_race', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '22_duplicate_registration_race', [
    'checks',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
