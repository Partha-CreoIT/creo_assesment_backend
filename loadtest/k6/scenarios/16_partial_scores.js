// Test 18 — weighted partial coding scores. Grading computes
// score = marks * passedWeight / totalWeight (internal/services/grading.go).
// This exam has one coding question with weights 1+1+3+5=10 and marks=10,
// where the submitted code is deliberately correct ONLY for the weight-5
// case — expected score is exactly 10 * 5/10 = 5.00. A handful of
// candidates run the identical scenario so the assertion holds
// independently for each of them, proving grading one candidate can't
// affect another's score even under concurrency.
//
// Run: k6 run loadtest/k6/scenarios/16_partial_scores.js
// Override: VUS=20 k6 run ...
import { check, sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta, recordMeta } from '../lib/summary.js';
import { adminAuth, registerCandidates } from '../lib/setup.js';
import { createExam, createQuestion, getExam, updateSetQuestions, activateExam, getPaper, saveAnswer, submitExam, getMe, listSessions } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '5', 10);
const MAX_POLL_SEC = parseFloat(__ENV.MAX_POLL_SEC || '30');
const EXPECTED_SCORE = 5; // marks(10) * passedWeight(5) / totalWeight(1+1+3+5=10)

// Passes ONLY the weight-5 case ("999" -> SPECIAL); fails the other three.
const PARTIAL_CREDIT_CODE = 'n = input().strip()\nprint("SPECIAL" if n == "999" else "OOPS")';

export const options = {
  scenarios: {
    partial_scores: {
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
  const adminToken = adminAuth(__ENV.ADMIN_EMAIL || 'admin@example.com', __ENV.ADMIN_PASSWORD || 'admin123');

  const examRes = createExam(adminToken, {
    title: `LoadTest PartialScores ${Date.now()}`,
    instructions: 'Created by loadtest/k6 16_partial_scores.js',
    durationMin: 60,
    maxViolations: 3,
  });
  const examId = examRes.json('id');

  const qRes = createQuestion(adminToken, {
    type: 'coding',
    title: 'Load Test Weighted Partial Credit',
    body: 'Print SPECIAL if the input is 999, else print OOPS.',
    marks: 10,
    difficulty: 'medium',
    starterCode: { python: '# write your solution here', java: '// n/a', c: '// n/a' },
    syntaxNote: 'Weighted test cases: 1, 1, 3, 5.',
    timeLimitMs: 3000,
    memoryLimitKb: 0,
    testCases: [
      { input: '1', expected: 'SPECIAL', hidden: false, weight: 1 },
      { input: '2', expected: 'SPECIAL', hidden: true, weight: 1 },
      { input: '3', expected: 'SPECIAL', hidden: true, weight: 3 },
      { input: '999', expected: 'SPECIAL', hidden: true, weight: 5 },
    ],
  });
  const questionId = qRes.json('id');

  // Manual assignment, NOT auto-distribute: auto-distribute samples from
  // the entire (session-accumulated) questions table, not just this exam's
  // — see the comment in lib/setup.js's setupExam() for the full story.
  // This exact bug is what this scenario originally caught.
  const examDetail = getExam(adminToken, examId);
  for (const set of examDetail.json('sets') || []) {
    updateSetQuestions(adminToken, set.id, [questionId]);
  }
  activateExam(adminToken, examId);
  recordMeta('exam_id', examId);

  const candidates = registerCandidates(VUS);
  return { examId, adminToken, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');

  checkOk(saveAnswer(token, coding.id, { code: PARTIAL_CREDIT_CODE, language: 'python' }), 'answers');

  const submitRes = submitExam(token);
  checkOk(submitRes, 'normal', { extraChecks: { 'submit: status is grading': (r) => r.json('status') === 'grading' } });

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
  check(graded, { 'grading: reached graded within the poll budget': (g) => g === true });
  if (!graded) return;

  const sessionsRes = listSessions(data.adminToken, data.examId);
  checkOk(sessionsRes, 'normal');
  const item = (sessionsRes.json('items') || []).find((i) => i.student.email === candidate.email);
  check(item, {
    'partial score: matching session found by email': (i) => !!i,
    'partial score: exactly marks x passedWeight/totalWeight': (i) => i && i.totalScore === EXPECTED_SCORE,
    'partial score: autoScore matches (no manual grading in this exam)': (i) => i && i.autoScore === EXPECTED_SCORE,
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '16_partial_scores', examId: readMeta(data, 'exam_id'), vus: VUS, expectedScore: EXPECTED_SCORE };
  return writeResults(data, meta, outDir, '16_partial_scores', [
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
