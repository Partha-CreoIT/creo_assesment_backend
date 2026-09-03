// Test 36 — while a candidate is mid-exam, the admin edits the coding
// question's marks/test cases, then deletes it outright. Documents the
// ACTUAL observed behavior rather than assuming one: per details.md, an
// edit is expected to show up live (the paper is generated fresh from the
// current Question row on every GET, not a frozen snapshot taken at
// registration time), and a delete is expected to cascade the SetQuestion
// link with no guard against deleting a question in active use — this
// scenario empirically confirms both rather than taking the documentation
// on faith, and is exactly the kind of test that should catch it if a
// future change adds a guard (or should but doesn't).
//
// Run: k6 run loadtest/k6/scenarios/29_question_mutation_mid_exam.js
import { check } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, updateQuestion, deleteQuestion } from '../lib/api.js';
import { codingQuestionPayload } from '../lib/data.js';

const VUS = 1; // a single, deeply-inspected candidate — this is a targeted
// correctness/behavior-documentation test, not a scale test.

export const options = {
  scenarios: {
    question_mutation: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '1m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({ title: `LoadTest QuestionMutation ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, adminToken, candidates };
}

export default function (data) {
  const candidate = data.candidates[0];
  const token = candidate.token;

  const paper1 = getPaper(token);
  checkOk(paper1, 'paper');
  const originalCount = paper1.json('questions').length;
  const coding = paper1.json('questions').find((q) => q.type === 'coding');
  console.log(`MUTATION: original paper has ${originalCount} questions, coding id=${coding.id} marks=${coding.marks}`);

  // 1. Edit the question's marks/test cases mid-exam.
  const updatedPayload = codingQuestionPayload(1, {});
  updatedPayload.marks = 99;
  const updateRes = updateQuestion(data.adminToken, coding.id, updatedPayload);
  checkOk(updateRes, 'normal', {
    extraChecks: { 'mutation: question update accepted': (r) => r.status === 200 && r.json('marks') === 99 },
  });

  const paper2 = getPaper(token);
  checkOk(paper2, 'paper');
  const coding2 = paper2.json('questions').find((q) => q.type === 'coding');
  const liveReflected = !!coding2 && coding2.marks === 99;
  console.log(`MUTATION: after edit, candidate's paper shows marks=${coding2 ? coding2.marks : 'MISSING'} (live=${liveReflected})`);
  check(coding2, {
    'mutation: DOCUMENTED — paper reflects the live question, not a frozen snapshot': () => liveReflected,
  });

  // 2. Delete the question while it's still actively assigned to this
  // candidate's set — per details.md, no guard exists against this.
  const deleteRes = deleteQuestion(data.adminToken, coding.id);
  check(deleteRes, {
    'mutation: DOCUMENTED — delete succeeds with no guard, even while in active use': (r) => r.status === 200,
  });

  const paper3 = getPaper(token);
  checkOk(paper3, 'paper');
  const newCount = paper3.json('questions').length;
  console.log(`MUTATION RESULT: original=${originalCount} after_delete=${newCount} (expect original-1: cascade removed the SetQuestion link)`);
  check(newCount, {
    'mutation: DOCUMENTED — deleting an in-use question silently shrinks the candidate paper by 1': (n) =>
      n === originalCount - 1,
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '29_question_mutation_mid_exam', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '29_question_mutation_mid_exam', [
    'checks',
    'success_paper',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
