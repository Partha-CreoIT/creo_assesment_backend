// Test 9 — N candidates all call /me/run simultaneously (synchronized
// burst), while a separate small pool of candidates concurrently does
// heartbeats and answer saves throughout. This is the RUN_CONCURRENCY sweep
// target (Phase 9 reruns this same scenario at RUN_CONCURRENCY=4/8/12/16 and
// records the health columns per value — see loadtest/README.md). The
// question is never "did all N runs execute immediately" (they should NOT,
// by design) — it's whether the rest of the API (heartbeats, answer saves)
// stays healthy while /me/run is saturated and correctly throttling.
//
// Run: k6 run loadtest/k6/scenarios/09_run_concurrency_limit.js
// Override: VUS=300 k6 run ...  (VUS = candidates hammering /me/run; a
// smaller fixed pool separately exercises heartbeat/answers throughout)
import { sleep } from 'k6';
import exec from 'k6/execution';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode, heartbeat, saveAnswer } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const HEALTH_VUS = Math.max(1, Math.floor(VUS / 10));
const HEALTH_DURATION = __ENV.HEALTH_DURATION || '15s';

export const options = {
  scenarios: {
    run_saturation: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      exec: 'runSaturation',
      maxDuration: '3m',
    },
    health_traffic: {
      executor: 'constant-vus',
      vus: HEALTH_VUS,
      duration: HEALTH_DURATION,
      exec: 'healthTraffic',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest RunConcurrency ${Date.now()}` });
  // +HEALTH_VUS extra candidates dedicated to the concurrent health-traffic
  // pool, kept separate from the run-saturation pool so a slow/queued run
  // request can never starve a heartbeat/answer-save check.
  const candidates = registerCandidates(VUS + HEALTH_VUS);
  const runCandidates = candidates.slice(0, VUS);
  const healthCandidates = candidates.slice(VUS);

  const paperRes = getPaper(healthCandidates[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;

  return { examId, runCandidates, healthCandidates, codingId };
}

// __VU is not a reliable per-candidate key: confirmed empirically (k6
// v2.2.0) that even scenarios starting simultaneously get interleaved,
// non-contiguous __VU ranges — `(__VU-1) % pool.length` can duplicate one
// candidate while skipping another instead of hitting all N exactly once.
// exec.scenario.iterationInTest is scoped to the current scenario and
// increments gap-free once per iteration, giving each of this scenario's N
// VUs a distinct 0..N-1 slot regardless of what __VU k6 assigned.
export function runSaturation(data) {
  const candidate = data.runCandidates[exec.scenario.iterationInTest % data.runCandidates.length];
  const paperRes = getPaper(candidate.token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');

  const res = runCode(candidate.token, {
    questionId: coding.id,
    language: 'python',
    code: CODE_FIXTURES.python.correctSum,
    mode: 'samples',
  });
  checkOk(res, 'normal', { isRunEndpoint: true });
}

export function healthTraffic(data) {
  const candidate = data.healthCandidates[exec.scenario.iterationInTest % data.healthCandidates.length];
  checkOk(heartbeat(candidate.token), 'heartbeat');
  sleep(0.5);
  checkOk(
    saveAnswer(candidate.token, data.codingId, {
      code: `print(${Math.floor(Math.random() * 1000)})`,
      language: 'python',
    }),
    'answers',
  );
  sleep(0.5);
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '09_run_concurrency_limit', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '09_run_concurrency_limit', [
    'checks',
    'success_normal',
    'success_heartbeat',
    'success_answers',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
