// Tests 33+34 — a realistic simulated exam rather than a uniform request
// mix: candidates spend most of their time reading/answering, with
// escalating code-run spikes and a synchronized final-minute rush,
// matching the plan's minute-by-minute profile:
//
//   t=0        registration (in setup()) + an immediate paper-load burst
//   t=5..59    normal activity: 70% read paper, 15% autosave, 10%
//              heartbeat, 3% run code, 1% violation (logged-only kind),
//              1% resume/navigate — a PACED mix, not a synchronized burst
//   t=20       ~1/3 of candidates run code (synchronized burst)
//   t=30       ~2/3 of candidates run code (synchronized burst)
//   t=45       all candidates run code (synchronized burst — the
//              "catastrophic synchronized event")
//   t=59       every candidate saves + heartbeats + submits together
//
// Registration itself has to happen in setup() (every other scenario below
// needs tokens to already exist when it starts) — "minute 0" here is really
// "immediately after setup()", not a separately-timed scenario.
//
// EXAM_DURATION_MIN sets the simulated exam length (matches the durationMin
// sent to POST /admin/exams); TIME_SCALE compresses the real wall-clock
// timeline for a faster dry run (e.g. TIME_SCALE=10 runs a 60-minute exam's
// worth of phase timing in 6 minutes) — leave it at 1 for a real
// certification-grade run.
//
// Run: k6 run loadtest/k6/scenarios/27_mixed_realistic.js
// Override: VUS=300 EXAM_DURATION_MIN=60 TIME_SCALE=1 k6 run ...
// Quick dry run: VUS=15 EXAM_DURATION_MIN=60 TIME_SCALE=20 k6 run ...
import { sleep } from 'k6';
import exec from 'k6/execution';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer, heartbeat, runCode, reportViolation, registerCandidate, submitExam } from '../lib/api.js';
import { CODE_FIXTURES, registerPayload } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '15', 10);
const EXAM_DURATION_MIN = parseInt(__ENV.EXAM_DURATION_MIN || '60', 10);
const TIME_SCALE = parseFloat(__ENV.TIME_SCALE || '1');

function scaledSec(minuteMark) {
  return Math.round((minuteMark * 60) / TIME_SCALE);
}
function scaledDurationStr(minutes) {
  return `${Math.max(1, Math.round((minutes * 60) / TIME_SCALE))}s`;
}

const SPIKE_20_VUS = Math.max(1, Math.round((VUS * 100) / 300));
const SPIKE_30_VUS = Math.max(1, Math.round((VUS * 200) / 300));

export const options = {
  scenarios: {
    minute0_paper_load: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      exec: 'minute0PaperLoad',
      startTime: '0s',
    },
    normal_activity: {
      executor: 'constant-vus',
      vus: VUS,
      startTime: `${scaledSec(5)}s`,
      duration: scaledDurationStr(EXAM_DURATION_MIN - 6), // 5..(duration-1)
      exec: 'normalActivityMix',
    },
    code_spike_20: {
      executor: 'per-vu-iterations',
      vus: SPIKE_20_VUS,
      iterations: 1,
      startTime: `${scaledSec(20)}s`,
      exec: 'runCodeSpike',
    },
    code_spike_30: {
      executor: 'per-vu-iterations',
      vus: SPIKE_30_VUS,
      iterations: 1,
      startTime: `${scaledSec(30)}s`,
      exec: 'runCodeSpike',
    },
    code_spike_45_catastrophic: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      startTime: `${scaledSec(45)}s`,
      exec: 'runCodeSpike',
    },
    final_minute_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      startTime: `${scaledSec(EXAM_DURATION_MIN - 1)}s`,
      exec: 'finalBurst',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({
    title: `LoadTest MixedRealistic ${Date.now()}`,
    durationMin: EXAM_DURATION_MIN,
  });
  const candidates = registerCandidates(VUS);
  const paperRes = getPaper(candidates[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  return { examId, candidates, codingId };
}

// __VU is NOT a safe way to pick a candidate here. It's fine to wrap
// `(__VU - 1) % pool.length` when every scenario in a file starts at the
// same time (see 05b_connection_pressure.js) — but this file has scenarios
// with STAGGERED startTimes, and k6 recycles VU IDs from an
// already-finished earlier scenario for a later one. Confirmed empirically
// (k6 v2.2.0): a 5-VU scenario starting at 0s and another starting at 3s do
// NOT get disjoint VU ID ranges — __VU values were scattered/overlapping
// between them. In a real run of this file, that gap silently dropped 1 of
// 50 candidates from `final_minute_burst` entirely (0 answers, 0 submit —
// found via `verify --check=grading` showing 49/50 graded, not 50/50).
// exec.scenario.iterationInTest is the actual fix: a counter scoped to
// THIS scenario alone, incrementing exactly once per iteration with no
// gaps or reuse, regardless of what __VU k6 happened to assign.
function pickCandidate(data) {
  return data.candidates[exec.scenario.iterationInTest % data.candidates.length];
}

export function minute0PaperLoad(data) {
  checkOk(getPaper(pickCandidate(data).token), 'paper');
}

function pickAction() {
  const r = Math.random() * 100;
  if (r < 70) return 'read';
  if (r < 85) return 'autosave'; // +15
  if (r < 95) return 'heartbeat'; // +10
  if (r < 98) return 'run'; // +3
  if (r < 99) return 'violation'; // +1
  return 'resume'; // +1 = 100
}

export function normalActivityMix(data) {
  const candidate = pickCandidate(data);
  const token = candidate.token;

  switch (pickAction()) {
    case 'read':
      checkOk(getPaper(token), 'paper');
      break;
    case 'autosave':
      checkOk(
        saveAnswer(token, data.codingId, { code: `print(${Math.floor(Math.random() * 1000)})`, language: 'python' }),
        'answers',
      );
      break;
    case 'heartbeat':
      checkOk(heartbeat(token), 'heartbeat');
      break;
    case 'run':
      checkOk(
        runCode(token, { questionId: data.codingId, language: 'python', code: CODE_FIXTURES.python.correctSum, mode: 'samples' }),
        'normal',
        { isRunEndpoint: true },
      );
      sleep(2.2); // clear this candidate's rate limit before their next tick might also roll 'run'
      break;
    case 'violation':
      checkOk(reportViolation(token, { kind: 'copy' }), 'normal'); // logged-only kind — no auto-submit noise
      break;
    case 'resume':
      checkOk(registerCandidate(registerPayload(candidate.n)), 'registration', {
        extraChecks: { 'mixed: resume returns resumed=true': (r) => r.json('resumed') === true },
      });
      break;
  }
  sleep(0.5 + Math.random() * 1.5); // pacing between one candidate's own actions
}

export function runCodeSpike(data) {
  const candidate = pickCandidate(data);
  const res = runCode(candidate.token, {
    questionId: data.codingId,
    language: 'python',
    code: CODE_FIXTURES.python.correctSum,
    mode: 'samples',
  });
  checkOk(res, 'normal', { isRunEndpoint: true });
}

export function finalBurst(data) {
  const candidate = pickCandidate(data);
  const token = candidate.token;
  checkOk(saveAnswer(token, data.codingId, { code: CODE_FIXTURES.python.correctSum, language: 'python' }), 'answers');
  checkOk(heartbeat(token), 'heartbeat');
  checkOk(submitExam(token), 'normal', { extraChecks: { 'final burst: submit accepted': (r) => r.status === 200 } });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = {
    scenario: '27_mixed_realistic',
    examId: readMeta(data, 'exam_id'),
    vus: VUS,
    examDurationMin: EXAM_DURATION_MIN,
    timeScale: TIME_SCALE,
  };
  return writeResults(data, meta, outDir, '27_mixed_realistic', [
    'checks',
    'success_paper',
    'success_answers',
    'success_heartbeat',
    'success_registration',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
