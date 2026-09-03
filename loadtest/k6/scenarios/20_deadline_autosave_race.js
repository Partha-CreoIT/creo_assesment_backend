// Test 22 — autosave + heartbeat right around endsAt+ANSWER_GRACE_SEC.
// Unlike POST /me/submit (scenario 19, no grace-check, races the sweeper),
// PUT /me/answers goes through sessionWritable() (internal/handlers/
// student_answers.go): before the grace deadline it succeeds normally;
// after it, the request itself triggers the auto-finalize (SubmitAutoTime)
// and returns 409 "exam time is over" — deterministic, not a race against
// the sweeper. POST /me/heartbeat has NO grace-check at all (it only
// re-reads and reports current status) and must always return 200
// regardless of timing.
//
// (POST /me/submit's boundary behavior is scenario 19's job, not this one —
// combining it here would finalize the session and make every subsequent
// answer-save 409 for an unrelated reason: already-submitted, not the grace
// boundary this scenario is specifically isolating.)
//
// Run: k6 run loadtest/k6/scenarios/20_deadline_autosave_race.js
// Override candidate count: VUS=300 k6 run ...
import { sleep } from 'k6';
import { check } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getMe, getPaper, heartbeat, saveAnswer } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const EXAM_DURATION_MIN = 1; // 60s, the shortest controllable via the API
const GRACE_SEC = parseFloat(__ENV.GRACE_SEC || '30'); // must match the server's ANSWER_GRACE_SEC
const JITTER_SEC = parseFloat(__ENV.JITTER_SEC || '20'); // spread around endsAt+grace

export const options = {
  scenarios: {
    deadline_autosave_race: {
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
    title: `LoadTest DeadlineAutosaveRace ${Date.now()}`,
    durationMin: EXAM_DURATION_MIN,
  });
  const candidates = registerCandidates(VUS);
  const paperRes = getPaper(candidates[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  const meRes = getMe(candidates[0].token);
  const endsAtMs = new Date(meRes.json('endsAt')).getTime();
  return { examId, adminToken, candidates, endsAtMs, codingId };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;
  const graceDeadlineMs = data.endsAtMs + GRACE_SEC * 1000;

  const jitterMs = (Math.random() * 2 - 1) * JITTER_SEC * 1000;
  const targetMs = graceDeadlineMs + jitterMs;
  const waitMs = targetMs - Date.now();
  if (waitMs > 0) sleep(waitMs / 1000);

  // Heartbeat: no grace-check exists at all — must always be 200.
  const hbRes = heartbeat(token);
  check(hbRes, { 'heartbeat: always 200, no grace-check exists': (r) => r.status === 200 });

  // Answer save: deterministic on sessionWritable()'s check, evaluated
  // against the server's clock at request time — allow slop only very
  // close to the boundary (inherent latency between our timestamp and the
  // server's), but assert strictly further out.
  const sendTimeMs = Date.now();
  const clearlyBefore = sendTimeMs < graceDeadlineMs - 3000;
  const clearlyAfter = sendTimeMs > graceDeadlineMs + 3000;

  const answerRes = saveAnswer(token, data.codingId, { code: 'print(1)', language: 'python' });
  if (clearlyBefore) {
    check(answerRes, { 'autosave before grace: 200 with savedAt': (r) => r.status === 200 && !!r.json('savedAt') });
  } else if (clearlyAfter) {
    check(answerRes, {
      'autosave after grace: 409 exam time is over': (r) =>
        r.status === 409 && (r.json('error') || '').includes('time is over'),
    });
  } else {
    check(answerRes, {
      'autosave near boundary: either clean outcome, never a 5xx': (r) =>
        r.status === 200 || (r.status === 409 && (r.json('error') || '').includes('time is over')),
    });
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '20_deadline_autosave_race', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '20_deadline_autosave_race', [
    'checks',
    'success_heartbeat',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
