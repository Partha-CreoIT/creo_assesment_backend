// Test 26 — admin dashboard polling (list + per-session detail) running
// continuously and concurrently with real candidate traffic (paper loads,
// answer saves, heartbeats), for DURATION. The explicit p95 threshold on
// candidate-facing request tags is what actually proves monitoring doesn't
// degrade candidate performance, not just an eyeballed "seems fine."
//
// Run: k6 run loadtest/k6/scenarios/24_admin_monitoring.js
// Override: VUS=300 DURATION=2m k6 run ...
import { sleep } from 'k6';
import exec from 'k6/execution';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer, heartbeat, listSessions, getSessionDetail } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const DURATION = __ENV.DURATION || '20s';
const LIST_POLL_SEC = parseFloat(__ENV.LIST_POLL_SEC || '1.5');
const DETAIL_POLL_SEC = parseFloat(__ENV.DETAIL_POLL_SEC || '3');

export const options = {
  scenarios: {
    admin_list_polling: { executor: 'constant-vus', vus: 1, duration: DURATION, exec: 'adminListPolling' },
    admin_detail_polling: { executor: 'constant-vus', vus: 1, duration: DURATION, exec: 'adminDetailPolling' },
    candidate_traffic: { executor: 'constant-vus', vus: VUS, duration: DURATION, exec: 'candidateTraffic' },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
    // The actual "monitoring doesn't degrade candidates" assertion: normal
    // candidate-facing endpoints must stay within the B.2 latency target
    // while admin polling runs continuously alongside them.
    'http_req_duration{name:paper}': ['p(95)<500'],
    'http_req_duration{name:answer}': ['p(95)<500'],
    'http_req_duration{name:heartbeat}': ['p(95)<500'],
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({ title: `LoadTest AdminMonitoring ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  const paperRes = getPaper(candidates[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  return { examId, adminToken, candidates, codingId };
}

export function adminListPolling(data) {
  checkOk(listSessions(data.adminToken, data.examId), 'normal');
  sleep(LIST_POLL_SEC);
}

export function adminDetailPolling(data) {
  const sessionsRes = listSessions(data.adminToken, data.examId);
  if (sessionsRes.status === 200) {
    const items = sessionsRes.json('items') || [];
    if (items.length > 0) {
      const pick = items[Math.floor(Math.random() * items.length)];
      checkOk(getSessionDetail(data.adminToken, pick.id), 'normal');
    }
  }
  sleep(DETAIL_POLL_SEC);
}

// __VU is not reliable across concurrently-declared scenarios (confirmed
// empirically, k6 v2.2.0 — even same-start-time scenarios get interleaved,
// non-contiguous __VU ranges). exec.scenario.iterationInTest is scoped to
// this scenario alone and increments gap-free per iteration.
export function candidateTraffic(data) {
  const candidate = data.candidates[exec.scenario.iterationInTest % data.candidates.length];
  checkOk(getPaper(candidate.token), 'paper');
  sleep(0.3 + Math.random() * 0.5);
  checkOk(
    saveAnswer(candidate.token, data.codingId, { code: `print(${Math.floor(Math.random() * 1000)})`, language: 'python' }),
    'answers',
  );
  sleep(0.3 + Math.random() * 0.5);
  checkOk(heartbeat(candidate.token), 'heartbeat');
  sleep(0.3 + Math.random() * 0.5);
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '24_admin_monitoring', examId: readMeta(data, 'exam_id'), vus: VUS, duration: DURATION };
  return writeResults(data, meta, outDir, '24_admin_monitoring', [
    'checks',
    'success_paper',
    'success_answers',
    'success_heartbeat',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
