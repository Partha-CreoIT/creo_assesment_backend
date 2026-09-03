// Test 7 — N candidates each run code once, as a synchronized burst.
// LANGUAGE selects python | java | c | mixed (default: splits candidates
// evenly across all 3, matching the plan's "100 Python / 100 C / 100 Java"
// scenario D). At default RUN_CONCURRENCY (4), a 300-VU burst WILL produce
// 429s while candidates wait for a free execution slot — that's correct,
// expected throttling (see lib/checks.js), not a failure.
//
// Run: k6 run loadtest/k6/scenarios/07_run_code.js
// Override: VUS=300 LANGUAGE=mixed k6 run ...
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const LANGUAGE = __ENV.LANGUAGE || 'mixed'; // python | java | c | mixed

function languageFor(vu) {
  if (LANGUAGE !== 'mixed') return LANGUAGE;
  const langs = ['python', 'java', 'c'];
  return langs[(vu - 1) % 3];
}

export const options = {
  scenarios: {
    run_code_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '3m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest RunCode ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;
  const lang = languageFor(__VU);

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');

  const res = runCode(token, {
    questionId: coding.id,
    language: lang,
    code: CODE_FIXTURES[lang].correctSum,
    mode: 'samples',
  });
  checkOk(res, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`run ${lang}: compiled ok or throttled`]: (r) => r.status === 429 || r.json('compileOk') === true,
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '07_run_code', examId: readMeta(data, 'exam_id'), vus: VUS, language: LANGUAGE };
  return writeResults(data, meta, outDir, '07_run_code', [
    'checks',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
