// DB connection-exhaustion test — heartbeat storm + autosave + admin
// monitoring polling + normal paper-load traffic, ALL running concurrently
// against the same Postgres pool (SetMaxOpenConns(20) in
// internal/database/database.go). The question this answers isn't just
// "did it work" — it's whether the 20-connection pool saturates under
// combined pressure and, if so, whether that produces candidate-visible
// latency or errors. Pair with a high-frequency monitor poll:
//   INTERVAL_SEC=1 loadtest/monitor/poll.sh loadtest/results/connpressure.csv <server_pid>
//
// Run: k6 run loadtest/k6/scenarios/05b_connection_pressure.js
// Override: VUS=300 DURATION=2m k6 run ...  (VUS = candidate pool size;
// actual concurrent k6 VUs across all 4 simultaneous scenarios add up to
// roughly 2.3x that — see options.scenarios below)
import { sleep } from 'k6';
import exec from 'k6/execution';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { heartbeat, saveAnswer, getPaper, listSessions } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const DURATION = __ENV.DURATION || '20s';
const NORMAL_VUS = Math.max(1, Math.floor(VUS / 3));

export const options = {
  scenarios: {
    heartbeat_load: { executor: 'constant-vus', vus: VUS, duration: DURATION, exec: 'heartbeatLoad' },
    autosave_load: { executor: 'constant-vus', vus: VUS, duration: DURATION, exec: 'autosaveLoad' },
    normal_traffic: { executor: 'constant-vus', vus: NORMAL_VUS, duration: DURATION, exec: 'normalTraffic' },
    admin_polling: { executor: 'constant-vus', vus: 1, duration: DURATION, exec: 'adminPolling' },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

// __VU is not a reliable index here — confirmed empirically (k6 v2.2.0)
// that even scenarios starting at the exact same time get interleaved,
// non-contiguous __VU ranges, not clean disjoint blocks. exec.scenario.
// iterationInTest is scoped to the current scenario alone and increments
// gap-free once per iteration (constant-vus calls its exec function
// repeatedly, so this also rotates fairly through the pool over the whole
// DURATION, not just once). Multiple scenarios' iterations legitimately
// landing on the same candidate is still fine here: the point of this test
// is DB connection volume, not per-candidate realism (that's Phase 10's job).
function pickCandidate(data) {
  return data.candidates[exec.scenario.iterationInTest % data.candidates.length];
}

export function setup() {
  const { examId, adminToken } = setupExam({ title: `LoadTest ConnPressure ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  // every set has exactly 1 coding question in this harness's fixture, so
  // any candidate's paper gives the same coding question id for all of them.
  const paperRes = getPaper(candidates[0].token);
  const codingQuestionId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  return { examId, adminToken, candidates, codingQuestionId };
}

export function heartbeatLoad(data) {
  const candidate = pickCandidate(data);
  checkOk(heartbeat(candidate.token), 'heartbeat');
  sleep(2 + Math.random());
}

export function autosaveLoad(data) {
  const candidate = pickCandidate(data);
  const res = saveAnswer(candidate.token, data.codingQuestionId, {
    code: `print(${Math.floor(Math.random() * 1000)})`,
    language: 'python',
  });
  checkOk(res, 'answers');
  sleep(1 + Math.random());
}

export function normalTraffic(data) {
  const candidate = pickCandidate(data);
  checkOk(getPaper(candidate.token), 'paper');
  sleep(1 + Math.random() * 2);
}

export function adminPolling(data) {
  checkOk(listSessions(data.adminToken, data.examId), 'normal');
  sleep(1.5);
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = {
    scenario: '05b_connection_pressure',
    examId: readMeta(data, 'exam_id'),
    vus: VUS,
    duration: DURATION,
  };
  return writeResults(data, meta, outDir, '05b_connection_pressure', [
    'checks',
    'success_heartbeat',
    'success_answers',
    'success_paper',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
