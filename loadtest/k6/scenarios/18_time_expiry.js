// Test 20 — N candidates register against a short exam (durationMin=1, the
// shortest controllable via the API) and then deliberately do NOTHING —
// simulating "walked away and left the exam active." A single watcher VU
// polls the admin sessions endpoint, logging a
// `CENSUS t+Ns active=X total=Y` line every CENSUS_INTERVAL_SEC, until every
// session has left `active` on its own — proving the sweeper
// (services.StartSweeper, not any client action) auto-submits them.
//
// This test inherently takes ~90-120s (1 min exam duration + the default
// 30s ANSWER_GRACE_SEC + up to a 30s sweeper tick lag) — that wait is
// expected, not a failure.
//
// Run: k6 run loadtest/k6/scenarios/18_time_expiry.js
// Override candidate count: VUS=300 k6 run ...
import { check, sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { listSessions } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '5', 10);
const EXAM_DURATION_MIN = parseInt(__ENV.EXAM_DURATION_MIN || '1', 10); // API minimum
const MAX_WAIT_SEC = parseFloat(__ENV.MAX_WAIT_SEC || '150');
const CENSUS_INTERVAL_SEC = parseFloat(__ENV.CENSUS_INTERVAL_SEC || '10');

const TEST_START_MS = Date.now();

export const options = {
  scenarios: {
    time_expiry_watch: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      maxDuration: `${Math.ceil(MAX_WAIT_SEC + 30)}s`,
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({
    title: `LoadTest TimeExpiry ${Date.now()}`,
    durationMin: EXAM_DURATION_MIN,
  });
  const candidates = registerCandidates(VUS);
  return { examId, adminToken, candidates };
}

export default function (data) {
  const deadline = Date.now() + MAX_WAIT_SEC * 1000;
  let allSwept = false;

  while (Date.now() < deadline) {
    const res = listSessions(data.adminToken, data.examId);
    checkOk(res, 'normal');
    const items = res.json('items') || [];
    const stillActive = items.filter((i) => i.status === 'active').length;
    const elapsedSec = Math.round((Date.now() - TEST_START_MS) / 1000);
    console.log(`CENSUS t+${elapsedSec}s active=${stillActive} total=${items.length}`);

    if (stillActive === 0 && items.length === data.candidates.length) {
      allSwept = true;
      const wrongKind = items.filter((i) => i.submitKind !== 'auto_time').length;
      check(items, {
        'time expiry: every session swept as auto_time (no client ever submitted)': () => wrongKind === 0,
      });
      break;
    }
    sleep(CENSUS_INTERVAL_SEC);
  }

  check(allSwept, { 'time expiry: every candidate eventually left active on its own': (d) => d === true });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '18_time_expiry', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '18_time_expiry', [
    'checks',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
