// Test 14 + 15 (grading queue pressure) combined — N candidates save real
// answers (english + aptitude + 3 coding questions, matching the plan's
// "300 x 3 coding x 4 test cases = 3,600 executions" worked example),
// submit as a synchronized burst, then each polls its own GET /me until it
// reaches `graded` (or a budget expires), recording end-to-end submit->
// graded latency per candidate as the submit_to_graded_ms Trend metric
// (p50/p95/p99/max come straight out of the k6 summary for that metric).
//
// "grading started" is not independently observable (POST /me/submit
// synchronously flips status to `grading` and enqueues — see
// internal/services/grading.go), so a second concurrent scenario polls the
// admin sessions endpoint every CENSUS_INTERVAL_SEC and logs a
// `CENSUS t+Ns grading=X graded=Y total=Z` line — grep the run's output for
// the line closest to t+300/600/1200 to answer "how many still grading at
// 5/10/20 min" for a real-scale run, per the plan's two-sided measurement.
//
// Run: k6 run loadtest/k6/scenarios/14_submit_storm.js
// Override: VUS=300 MAX_POLL_SEC=1800 k6 run ...  (raise MAX_POLL_SEC for
// real scale — grading 3,600 executions through 4 workers can take a while,
// which is not itself a failure, see loadtest/README.md)
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { Trend } from 'k6/metrics';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer, submitExam, getMe, listSessions } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const POLL_INTERVAL_SEC = parseFloat(__ENV.POLL_INTERVAL_SEC || '3');
const MAX_POLL_SEC = parseFloat(__ENV.MAX_POLL_SEC || '120');
const CENSUS_INTERVAL_SEC = parseFloat(__ENV.CENSUS_INTERVAL_SEC || '5');

const TEST_START_MS = Date.now();
const submitToGradedMs = new Trend('submit_to_graded_ms', true);

export const options = {
  scenarios: {
    submit_and_wait: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      exec: 'submitAndWait',
      maxDuration: `${Math.ceil(MAX_POLL_SEC + 30)}s`,
    },
    grading_census: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      exec: 'gradingCensus',
      startTime: '2s', // let submit_and_wait actually submit something first
      maxDuration: `${Math.ceil(MAX_POLL_SEC + 20)}s`,
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({
    title: `LoadTest SubmitStorm ${Date.now()}`,
    counts: { english: 1, aptitude: 1, coding: 3 },
  });
  const candidates = registerCandidates(VUS);
  return { examId, adminToken, candidates };
}

export function submitAndWait(data) {
  // __VU is NOT a safe candidate-selection key: this file has two scenarios
  // with different startTimes (submit_and_wait at 0s, grading_census at
  // 2s), and k6 can recycle a VU ID from one scenario for another rather
  // than guaranteeing disjoint ranges. Confirmed at VUS=50: exactly 1
  // candidate got skipped entirely (0 answers, never submitted, stuck
  // `active`) — the same bug found in 27_mixed_realistic.js, which didn't
  // reproduce at the smaller VUS=5 this file was first tested with.
  // exec.scenario.iterationInTest is scoped to this scenario alone — a
  // gap-free 0..N-1 counter regardless of __VU reuse.
  const candidate = data.candidates[exec.scenario.iterationInTest % data.candidates.length];
  const token = candidate.token;

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const questions = paperRes.json('questions');

  for (const q of questions) {
    let payload;
    if (q.type === 'english') payload = { answerText: 'A reasonably complete answer, for grading load.' };
    else if (q.type === 'aptitude') payload = { selectedIndex: 1 };
    else payload = { code: CODE_FIXTURES.python.correctSum, language: 'python' };
    checkOk(saveAnswer(token, q.id, payload), 'answers');
  }

  const submitRes = submitExam(token);
  const submitOk = checkOk(submitRes, 'normal', {
    extraChecks: {
      'submit: status is grading': (r) => r.json('status') === 'grading',
      'submit: submittedAt present': (r) => !!r.json('submittedAt'),
    },
  });
  if (!submitOk) return;

  const start = Date.now();
  let graded = false;
  while (Date.now() - start < MAX_POLL_SEC * 1000) {
    sleep(POLL_INTERVAL_SEC);
    const meRes = getMe(token);
    if (meRes.status === 200 && meRes.json('status') === 'graded') {
      graded = true;
      break;
    }
  }
  if (graded) submitToGradedMs.add(Date.now() - start);
  check(graded, { 'grading: reached graded within the poll budget': (g) => g === true });
}

export function gradingCensus(data) {
  // Single iteration that loops internally until everyone's finalized (or
  // MAX_POLL_SEC elapses) rather than a fixed-duration executor — no point
  // burning wall-clock time polling after grading has actually finished.
  const deadline = Date.now() + MAX_POLL_SEC * 1000;
  while (Date.now() < deadline) {
    const res = listSessions(data.adminToken, data.examId);
    if (res.status === 200) {
      const items = res.json('items') || [];
      const stillActive = items.filter((i) => i.status === 'active').length;
      const stillGrading = items.filter((i) => i.status === 'grading').length;
      const graded = items.filter((i) => i.status === 'graded').length;
      const elapsedSec = Math.round((Date.now() - TEST_START_MS) / 1000);
      console.log(
        `CENSUS t+${elapsedSec}s active=${stillActive} grading=${stillGrading} graded=${graded} total=${items.length}`,
      );
      if (stillActive === 0 && stillGrading === 0) break; // everyone finalized
    }
    sleep(CENSUS_INTERVAL_SEC);
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '14_submit_storm', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '14_submit_storm', [
    'checks',
    'success_normal',
    'success_answers',
    'success_paper',
    'submit_to_graded_ms',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
