// General-purpose "candidates doing normal things for DURATION" scenario —
// the TARGET workload for Phase 8's chaos tests (Postgres/API/Piston
// restart mid-exam). Deliberately has NO strict thresholds: a chaos action
// fired partway through WILL cause some requests to fail during the outage
// window — that's the point, not a bug. Pass/fail for a chaos test is about
// recovery afterward and data integrity (verified via `loadtest/verify`
// once the run completes), not a zero-error run.
//
// Not meant to be run standalone — see chaos/run_with_chaos.sh, which
// starts this in the background and fires one chaos action partway through.
//
// Env: VUS, DURATION, INCLUDE_RUN_CODE=true|false (adds periodic /me/run
// calls — set true for the Piston chaos test, false/omit otherwise).
import { sleep } from 'k6';
import { checkOk } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer, heartbeat, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const DURATION = __ENV.DURATION || '60s';
const INCLUDE_RUN_CODE = (__ENV.INCLUDE_RUN_CODE || 'false') === 'true';

export const options = {
  scenarios: {
    sustained_activity: { executor: 'constant-vus', vus: VUS, duration: DURATION, exec: 'sustainedActivity' },
  },
  // No BASELINE_THRESHOLDS here on purpose — see file header.
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest ChaosTarget ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  const paperRes = getPaper(candidates[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  return { examId, candidates, codingId };
}

export function sustainedActivity(data) {
  const candidate = data.candidates[(__VU - 1) % data.candidates.length];
  const token = candidate.token;

  checkOk(getPaper(token), 'paper');
  sleep(1 + Math.random());

  checkOk(saveAnswer(token, data.codingId, { code: `print(${Math.floor(Math.random() * 1000)})`, language: 'python' }), 'answers');
  sleep(1 + Math.random());

  checkOk(heartbeat(token), 'heartbeat');
  sleep(1 + Math.random());

  if (INCLUDE_RUN_CODE && Math.random() < 0.3) {
    checkOk(
      runCode(token, { questionId: data.codingId, language: 'python', code: CODE_FIXTURES.python.correctSum, mode: 'samples' }),
      'normal',
      { isRunEndpoint: true },
    );
    sleep(2.2); // clear the per-session rate limit before this VU's next iteration
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '26_sustained_activity', examId: readMeta(data, 'exam_id'), vus: VUS, duration: DURATION };
  return writeResults(data, meta, outDir, '26_sustained_activity', [
    'checks',
    'success_paper',
    'success_answers',
    'success_heartbeat',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
