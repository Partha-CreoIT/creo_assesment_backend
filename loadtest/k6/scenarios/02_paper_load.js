// Test 2 — N pre-registered candidates load GET /me and GET /me/paper
// concurrently (synchronized burst — see loadtest/README.md's VU
// convention). This is both a load test and a SECURITY test: the paper
// response must never leak an aptitude question's correctIndex or a coding
// question's hidden test cases.
//
// Run: k6 run loadtest/k6/scenarios/02_paper_load.js
// Override candidate count: VUS=300 k6 run ...  (default 10, for smoke tests)
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getMe, getPaper } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

export const options = {
  scenarios: {
    paper_load_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '2m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest PaperLoad ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const meRes = getMe(token);
  checkOk(meRes, 'normal', {
    extraChecks: {
      'me: correct exam': (r) => String(r.json('exam.id')) === String(data.examId),
      'me: status active': (r) => r.json('status') === 'active',
      'me: set assigned (A-F)': (r) => ['A', 'B', 'C', 'D', 'E', 'F'].includes(r.json('setLabel')),
    },
  });

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper', {
    extraChecks: {
      'paper: 3 questions': (r) => r.json('questions').length === 3,
      'paper: order is english, aptitude, coding': (r) => {
        const types = r.json('questions').map((q) => q.type);
        return JSON.stringify(types) === JSON.stringify(['english', 'aptitude', 'coding']);
      },
      // --- security: no answer key or hidden tests in the response ---
      'SECURITY paper: aptitude has no correctIndex': (r) => {
        const q = r.json('questions').find((x) => x.type === 'aptitude');
        return q && (q.correctIndex === undefined || q.correctIndex === null);
      },
      'SECURITY paper: coding hides hidden test cases': (r) => {
        const q = r.json('questions').find((x) => x.type === 'coding');
        if (!q) return false;
        // Our fixture has 4 test cases, 2 hidden — the paper must expose
        // only the 2 non-hidden ones, and report the hidden count, not
        // their content.
        return q.sampleTests && q.sampleTests.length === 2 && q.hiddenTestCount === 2;
      },
      'SECURITY paper: sample tests are only the visible ones': (r) => {
        const q = r.json('questions').find((x) => x.type === 'coding');
        const inputs = q.sampleTests.map((t) => t.input);
        // the fixture's hidden cases are "100 200" and "-5 5" (data.js
        // codingQuestionPayload) — must never appear in sampleTests.
        return !inputs.includes('100 200') && !inputs.includes('-5 5');
      },
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '02_paper_load', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '02_paper_load', [
    'checks',
    'success_paper',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
