// Test 35 — Exam A is active with candidates mid-attempt; the admin then
// activates Exam B. Verifies the documented single-active-exam-platform-wide
// behavior (internal/handlers/admin_exams.go: activating one exam
// auto-closes every other active exam) AND, importantly, what happens to
// Exam A's existing candidates: sessionWritable() only checks the
// session's OWN status/deadline, never the parent exam's status, so an
// Exam-A candidate should keep functioning completely normally even after
// their exam is no longer the "active" one platform-wide.
//
// Run: k6 run loadtest/k6/scenarios/28_multi_exam_activation.js
// Override candidate count: VUS=300 k6 run ...
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta, recordMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getExam, getPaper, getMe, saveAnswer } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    multi_exam_activation: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      maxDuration: '2m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId: examAId, adminToken } = setupExam({ title: `LoadTest MultiExamA ${Date.now()}` });
  const candidatesA = registerCandidates(VUS);
  const paperRes = getPaper(candidatesA[0].token);
  const codingId = paperRes.json('questions').find((q) => q.type === 'coding').id;
  return { examAId, adminToken, candidatesA, codingId };
}

export default function (data) {
  // Activating exam B auto-closes exam A — this call alone is the trigger.
  const { examId: examBId } = setupExam({ title: `LoadTest MultiExamB ${Date.now()}` });
  recordMeta('exam_b_id', examBId); // setupExam() already recorded exam A's id under 'exam_id' in setup()

  const examADetail = getExam(data.adminToken, data.examAId);
  checkOk(examADetail, 'normal', {
    extraChecks: { 'multi-exam: exam A auto-closed by exam B activating': (r) => r.json('status') === 'closed' },
  });

  const examBDetail = getExam(data.adminToken, examBId);
  checkOk(examBDetail, 'normal', {
    extraChecks: { 'multi-exam: exam B is now the active exam': (r) => r.json('status') === 'active' },
  });

  // Exam A's existing candidates keep functioning — closing the exam does
  // not touch their sessions.
  for (const candidate of data.candidatesA) {
    const meRes = getMe(candidate.token);
    checkOk(meRes, 'normal', {
      extraChecks: {
        'multi-exam: exam A candidate session unaffected (still active)': (r) => r.json('status') === 'active',
      },
    });
    const answerRes = saveAnswer(candidate.token, data.codingId, { code: 'print(1)', language: 'python' });
    checkOk(answerRes, 'answers', {
      extraChecks: {
        'multi-exam: exam A candidate can still autosave after their exam closed': (r) => r.status === 200,
      },
    });
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = {
    scenario: '28_multi_exam_activation',
    examAId: readMeta(data, 'exam_id'),
    examBId: readMeta(data, 'exam_b_id'),
    vus: VUS,
  };
  return writeResults(data, meta, outDir, '28_multi_exam_activation', [
    'checks',
    'success_normal',
    'success_answers',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
