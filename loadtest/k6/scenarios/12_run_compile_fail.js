// Test 12 — N candidates submit deliberately broken code (rotating
// python/java/c syntax errors, plus a Java-class-named-wrong variant),
// verifying compilation failures return a normal 200 with compileOk:false
// (never a 5xx/hang), don't consume the runner permanently, and don't break
// a subsequent candidate's clean run.
//
// Run: k6 run loadtest/k6/scenarios/12_run_compile_fail.js
// Override: VUS=300 k6 run ...
import { sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, runCode } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

// 4 broken-code variants, rotated across VUs: syntax errors in each
// language plus the classic "Java class not named Main" mistake (the API
// maps every Java submission to Main.java regardless of the class name in
// the code, so a mismatched class name is itself a compile error).
const VARIANTS = [
  { lang: 'python', code: CODE_FIXTURES.python.compileError, label: 'python syntax error' },
  { lang: 'java', code: CODE_FIXTURES.java.compileError, label: 'java syntax error' },
  { lang: 'c', code: CODE_FIXTURES.c.compileError, label: 'c syntax error' },
  { lang: 'java', code: CODE_FIXTURES.java.wrongClassName, label: 'java wrong class name' },
];

function variantFor(vu) {
  return VARIANTS[(vu - 1) % VARIANTS.length];
}

export const options = {
  scenarios: {
    compile_fail_burst: {
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
  const { examId } = setupExam({ title: `LoadTest CompileFail ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;
  const variant = variantFor(__VU);

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const coding = paperRes.json('questions').find((q) => q.type === 'coding');

  const failRes = runCode(token, {
    questionId: coding.id,
    language: variant.lang,
    code: variant.code,
    mode: 'samples',
  });
  checkOk(failRes, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`${variant.label}: normal 200, not a 5xx/hang`]: (r) => r.status === 200 || r.status === 429,
      // NOTE: compileOk is NOT reliably false for a broken submission on
      // this Piston deployment — Python has no separate compile stage
      // (compileOk stays true; the SyntaxError surfaces as a failed test's
      // `actual`), and this runtime's javac package likewise reports
      // compileOk:true with the compiler error text in `actual` rather than
      // short-circuiting via compileOk:false (only C's gcc package does
      // that here). So the meaningful, environment-accurate invariant is:
      // broken code must never be falsely reported as passing.
      [`${variant.label}: no test falsely reported as passed`]: (r) => {
        if (r.status !== 200) return true;
        // compileOk:false short-circuits with an empty results array before
        // ever running a test case (see internal/handlers/student_run.go) —
        // that's the correct, expected shape for a real compile failure,
        // distinct from the compileOk:true-with-broken-code case above.
        if (r.json('compileOk') === false) return true;
        const results = r.json('results') || [];
        return results.length > 0 && results.every((x) => x.status !== 'passed');
      },
    },
  });

  sleep(2.2); // clear RUN_RATE_LIMIT_SEC's default 2s window before checking recovery

  const recoverRes = runCode(token, {
    questionId: coding.id,
    language: variant.lang,
    code: CODE_FIXTURES[variant.lang].correctSum,
    mode: 'samples',
  });
  checkOk(recoverRes, 'normal', {
    isRunEndpoint: true,
    extraChecks: {
      [`${variant.label}: runner still healthy after a compile failure`]: (r) =>
        r.status === 429 || r.json('compileOk') === true,
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '12_run_compile_fail', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '12_run_compile_fail', [
    'checks',
    'success_normal',
    'throttled_run',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
