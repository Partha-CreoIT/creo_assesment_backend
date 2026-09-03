// Test 21 — races each candidate's own manual POST /me/submit against the
// background sweeper (services.StartSweeper, ticks every 30s and
// auto-submits any active session past endsAt+ANSWER_GRACE_SEC). Note
// /me/submit itself has NO grace-check (only PUT /me/answers and
// POST /me/run do — see internal/services/submit.go) — so a "late" manual
// submit is not rejected, it's just racing to be the one that wins
// FinalizeSession's atomic `WHERE status = 'active'` update against
// whichever sweeper tick gets there first. Both outcomes (submitKind
// 'manual' or 'auto_time') are correct; what must NEVER happen is a
// non-200 response, a corrupted/stuck session, or a wrong submitKind.
//
// Candidates' submit attempts are jittered across a window straddling
// endsAt+grace (some clearly before, some clearly after) so this single run
// exercises MANY repeats of the actual race in parallel, per the plan's "run
// this many times, not just once" instruction — rerun the whole scenario
// itself a few times too for even more coverage.
//
// Run: k6 run loadtest/k6/scenarios/19_deadline_submit_race.js
// Override candidate count: VUS=300 k6 run ...
import { sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getMe, submitExam, listSessions } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const EXAM_DURATION_MIN = 1; // 60s, the shortest controllable via the API
const GRACE_SEC = parseFloat(__ENV.GRACE_SEC || '30'); // must match the server's ANSWER_GRACE_SEC
const JITTER_SEC = parseFloat(__ENV.JITTER_SEC || '15'); // spread around endsAt+grace

export const options = {
  scenarios: {
    deadline_submit_race: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: `${Math.ceil(EXAM_DURATION_MIN * 60 + GRACE_SEC + JITTER_SEC + 30)}s`,
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({
    title: `LoadTest DeadlineSubmitRace ${Date.now()}`,
    durationMin: EXAM_DURATION_MIN,
  });
  const candidates = registerCandidates(VUS);
  const meRes = getMe(candidates[0].token);
  const endsAtMs = new Date(meRes.json('endsAt')).getTime();
  return { examId, adminToken, candidates, endsAtMs };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const jitterMs = (Math.random() * 2 - 1) * JITTER_SEC * 1000; // +/- JITTER_SEC around endsAt+grace
  const targetMs = data.endsAtMs + GRACE_SEC * 1000 + jitterMs;
  const waitMs = targetMs - Date.now();
  if (waitMs > 0) sleep(waitMs / 1000);

  const res = submitExam(token);
  checkOk(res, 'normal', {
    extraChecks: {
      'deadline race: submit always 200 (atomic + idempotent-safe)': (r) => r.status === 200,
      'deadline race: status is grading': (r) => r.json('status') === 'grading',
    },
  });
}

export function teardown(data) {
  const res = listSessions(data.adminToken, data.examId);
  const items = res.json('items') || [];
  const badKind = items.filter((i) => i.submitKind !== 'manual' && i.submitKind !== 'auto_time');
  const stillActive = items.filter((i) => i.status === 'active');
  const manualCount = items.filter((i) => i.submitKind === 'manual').length;
  const autoTimeCount = items.filter((i) => i.submitKind === 'auto_time').length;
  console.log(
    `RACE RESULT: total=${items.length} manual=${manualCount} auto_time=${autoTimeCount} ` +
      `bad_kind=${badKind.length} still_active=${stillActive.length}`,
  );
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '19_deadline_submit_race', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '19_deadline_submit_race', [
    'checks',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
