// Test 19 — N candidates submit, auto-grading (aptitude+coding) completes,
// then the admin manually grades each candidate's English answer with a
// DISTINCT score per candidate (cycling 0.5..4.5). Verifies manualScore/
// autoScore/totalScore are all correct AND — the important part — that
// grading one candidate's answer never affects another candidate's totals,
// by asserting each candidate's final scores match only its own expected
// value, never a neighbor's.
//
// Run: k6 run loadtest/k6/scenarios/17_manual_grading.js
// Override: VUS=50 k6 run ...
import { check, sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer, submitExam, getMe, listSessions, getSessionDetail, gradeAnswer } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '5', 10);
const MAX_POLL_SEC = parseFloat(__ENV.MAX_POLL_SEC || '30');

// 0.5..4.5 cycling every 5 candidates — always within the 5-mark cap, and
// distinct enough between neighbors to catch cross-candidate contamination.
function expectedEnglishScore(n) {
  return (n % 5) + 0.5;
}

function round2(n) {
  return Math.round(n * 100) / 100;
}

export const options = {
  scenarios: {
    manual_grading: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: `${Math.ceil(MAX_POLL_SEC + 30)}s`,
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({ title: `LoadTest ManualGrading ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, adminToken, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const questions = paperRes.json('questions');
  const english = questions.find((q) => q.type === 'english');
  const aptitude = questions.find((q) => q.type === 'aptitude');
  const coding = questions.find((q) => q.type === 'coding');

  checkOk(saveAnswer(token, english.id, { answerText: `Candidate ${candidate.n} english answer.` }), 'answers');
  checkOk(saveAnswer(token, aptitude.id, { selectedIndex: 1 }), 'answers'); // correct, per data.js's aptitudeQuestionPayload
  checkOk(saveAnswer(token, coding.id, { code: CODE_FIXTURES.python.correctSum, language: 'python' }), 'answers');

  checkOk(submitExam(token), 'normal', {
    extraChecks: { 'submit: status is grading': (r) => r.json('status') === 'grading' },
  });

  // Wait for auto-grading (aptitude+coding); the session flips to `graded`
  // once those settle even though English is still pending (details.md §4.9).
  const deadline = Date.now() + MAX_POLL_SEC * 1000;
  let graded = false;
  while (Date.now() < deadline) {
    sleep(2);
    const meRes = getMe(token);
    if (meRes.status === 200 && meRes.json('status') === 'graded') {
      graded = true;
      break;
    }
  }
  check(graded, { 'grading: auto-graded parts completed': (g) => g === true });
  if (!graded) return;

  const sessionsRes = listSessions(data.adminToken, data.examId);
  checkOk(sessionsRes, 'normal');
  const item = (sessionsRes.json('items') || []).find((i) => i.student.email === candidate.email);
  check(item, { 'manual grading: session found': (i) => !!i });
  if (!item) return;

  const detailRes = getSessionDetail(data.adminToken, item.id);
  checkOk(detailRes, 'normal');
  const englishItem = (detailRes.json('items') || []).find((x) => x.question.type === 'english');
  check(englishItem, { 'manual grading: english answer found': (x) => !!x && !!x.answer });
  if (!englishItem) return;

  const score = expectedEnglishScore(candidate.n);
  const gradeRes = gradeAnswer(data.adminToken, englishItem.answer.id, score);
  checkOk(gradeRes, 'normal', {
    extraChecks: { 'manual grading: score recorded': (r) => r.json('score') === score },
  });

  const finalSessions = listSessions(data.adminToken, data.examId);
  checkOk(finalSessions, 'normal');
  const finalItem = (finalSessions.json('items') || []).find((i) => i.student.email === candidate.email);
  const expectedTotal = round2(score + (item.autoScore || 0));
  check(finalItem, {
    'manual grading: manualScore matches this candidate only (no cross-contamination)': (i) =>
      i && i.manualScore === score,
    'manual grading: totalScore = autoScore + this candidates own manualScore': (i) =>
      i && Math.abs(i.totalScore - expectedTotal) < 0.01,
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '17_manual_grading', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '17_manual_grading', [
    'checks',
    'success_normal',
    'success_answers',
    'success_paper',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
