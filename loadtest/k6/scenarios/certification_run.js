// Phase 12 — the final acceptance/certification scenario: the full
// realistic simulated exam from 27_mixed_realistic.js (register -> paper ->
// answer -> autosave -> heartbeat -> run x several -> violation ->
// change-answer -> submit), PLUS admin monitoring polling and CSV export
// running concurrently throughout, exactly as the plan's Level A/Level B
// certification calls for. See loadtest/README.md "Phase 12" and
// loadtest/certify.sh, which wraps this with a reproducibility manifest
// (git SHA, config, timestamp) for the required repeat runs.
//
// This is the ONE scenario meant to be pointed at your deployed server with
// a specific candidate count:
//
//   BASE_URL=https://your-deployed-server.example VUS=300 EXAM_DURATION_MIN=60 \
//     k6 run loadtest/k6/scenarios/certification_run.js
//
// or, for the full reproducibility manifest:
//
//   loadtest/certify.sh https://your-deployed-server.example 300 cert300-20260906-01
//
// Quick dry run against a fresh deploy: VUS=15 TIME_SCALE=20 (compresses the
// simulated exam's internal timing so it finishes in a few minutes).
import { sleep } from 'k6';
import exec from 'k6/execution';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import {
  getPaper,
  saveAnswer,
  heartbeat,
  runCode,
  reportViolation,
  registerCandidate,
  submitExam,
  listSessions,
  getSessionDetail,
  exportCsv,
} from '../lib/api.js';
import { CODE_FIXTURES, registerPayload } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '15', 10);
const EXAM_DURATION_MIN = parseInt(__ENV.EXAM_DURATION_MIN || '60', 10);
const TIME_SCALE = parseFloat(__ENV.TIME_SCALE || '1');
const ADMIN_POLL_SEC = parseFloat(__ENV.ADMIN_POLL_SEC || '1.5');

function scaledSec(minuteMark) {
  return Math.round((minuteMark * 60) / TIME_SCALE);
}
function scaledDurationStr(minutes) {
  return `${Math.max(1, Math.round((minutes * 60) / TIME_SCALE))}s`;
}

const SPIKE_20_VUS = Math.max(1, Math.round((VUS * 100) / 300));
const SPIKE_30_VUS = Math.max(1, Math.round((VUS * 200) / 300));
const TOTAL_DURATION_SEC = scaledSec(EXAM_DURATION_MIN) + 30; // small tail for the final export

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
      duration: scaledDurationStr(EXAM_DURATION_MIN - 6),
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
    // The two additions over 27_mixed_realistic.js: continuous admin
    // monitoring for the whole run, and CSV export checks at two points.
    admin_monitoring: {
      executor: 'constant-vus',
      vus: 1,
      startTime: '0s',
      duration: `${TOTAL_DURATION_SEC}s`,
      exec: 'adminMonitoring',
    },
    csv_export_mid: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      startTime: `${scaledSec(30)}s`,
      exec: 'csvExportCheck',
    },
    csv_export_final: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      startTime: `${TOTAL_DURATION_SEC - 5}s`,
      exec: 'csvExportCheck',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({
    title: `Certification Run ${Date.now()}`,
    durationMin: EXAM_DURATION_MIN,
  });
  const candidates = registerCandidates(VUS);
  const paperRes = getPaper(candidates[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  console.log(`CERTIFICATION_RUN: examId=${examId} vus=${VUS} examDurationMin=${EXAM_DURATION_MIN} timeScale=${TIME_SCALE}`);
  return { examId, adminToken, candidates, codingId };
}

// __VU is NOT a safe candidate-selection key across scenarios with
// staggered startTimes — k6 recycles VU IDs from an already-finished
// earlier scenario, so `(__VU-1) % pool.length` can silently skip a
// candidate. Confirmed by 27_mixed_realistic.js's first real run: 1 of 50
// candidates got 0 answers and no submit, dropped by exactly this bug.
// exec.scenario.iterationInTest is scoped to the current scenario alone —
// a gap-free 0..N-1 counter regardless of __VU reuse.
function pickCandidate(data) {
  return data.candidates[exec.scenario.iterationInTest % data.candidates.length];
}

export function minute0PaperLoad(data) {
  checkOk(getPaper(pickCandidate(data).token), 'paper');
}

function pickAction() {
  const r = Math.random() * 100;
  if (r < 70) return 'read';
  if (r < 85) return 'autosave';
  if (r < 95) return 'heartbeat';
  if (r < 98) return 'run';
  if (r < 99) return 'violation';
  return 'resume';
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
      sleep(2.2);
      break;
    case 'violation':
      checkOk(reportViolation(token, { kind: 'copy' }), 'normal');
      break;
    case 'resume':
      checkOk(registerCandidate(registerPayload(candidate.n)), 'registration', {
        extraChecks: { 'cert: resume returns resumed=true': (r) => r.json('resumed') === true },
      });
      break;
  }
  sleep(0.5 + Math.random() * 1.5);
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
  checkOk(submitExam(token), 'normal', { extraChecks: { 'cert: final submit accepted': (r) => r.status === 200 } });
}

export function adminMonitoring(data) {
  const listRes = listSessions(data.adminToken, data.examId);
  checkOk(listRes, 'normal');
  if (listRes.status === 200) {
    const items = listRes.json('items') || [];
    if (items.length > 0) {
      const pick = items[Math.floor(Math.random() * items.length)];
      checkOk(getSessionDetail(data.adminToken, pick.id), 'normal');
    }
  }
  sleep(ADMIN_POLL_SEC);
}

export function csvExportCheck(data) {
  const res = exportCsv(data.adminToken, data.examId);
  checkOk(res, 'normal', {
    extraChecks: {
      'cert: csv export well-formed': (r) => (r.headers['Content-Type'] || '').includes('text/csv') && r.body.includes('Name,Email'),
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = {
    scenario: 'certification_run',
    examId: readMeta(data, 'exam_id'),
    vus: VUS,
    examDurationMin: EXAM_DURATION_MIN,
    timeScale: TIME_SCALE,
  };
  return writeResults(data, meta, outDir, 'certification_run', [
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
