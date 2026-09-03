// Test 5 — N candidates all POST /me/heartbeat at the same time, then again
// every few seconds (ROUNDS bursts, ROUND_GAP_SEC apart). StudentAuth does a
// DB lookup on every request, so this specifically stresses connection-pool
// headroom under a request that's cheap per-call but hits the DB every time.
//
// Run: k6 run loadtest/k6/scenarios/05_heartbeat_storm.js
// Override: VUS=300 ROUNDS=5 ROUND_GAP_SEC=3 k6 run ...
import { sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { heartbeat } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const ROUNDS = parseInt(__ENV.ROUNDS || '3', 10);
const ROUND_GAP_SEC = parseFloat(__ENV.ROUND_GAP_SEC || '3');

export const options = {
  scenarios: {
    heartbeat_storm: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '5m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest HeartbeatStorm ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  for (let round = 0; round < ROUNDS; round++) {
    const res = heartbeat(token);
    checkOk(res, 'heartbeat', {
      extraChecks: { 'heartbeat: status active': (r) => r.json('status') === 'active' },
    });
    if (round < ROUNDS - 1) sleep(ROUND_GAP_SEC);
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '05_heartbeat_storm', examId: readMeta(data, 'exam_id'), vus: VUS, rounds: ROUNDS };
  return writeResults(data, meta, outDir, '05_heartbeat_storm', [
    'checks',
    'success_heartbeat',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
