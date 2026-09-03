// Candidate identities and code fixtures shared across scenarios.

// `student001@test.com` .. `student300@test.com` — reusable verbatim across
// every scenario and every repeat run. ExamSession uniqueness is scoped to
// (exam_id, student_id), and every scenario's setup() creates its own exam,
// so the same email set never collides across runs. See details.md and the
// plan's B.3 note.
export function candidateEmail(n) {
  return `student${String(n).padStart(3, '0')}@test.com`;
}

export function candidateName(n) {
  return `Load Student ${n}`;
}

export function registerPayload(n, overrides = {}) {
  return {
    name: candidateName(n),
    email: candidateEmail(n),
    semester: (n % 8) + 1,
    phone: '9000000000',
    ...overrides,
  };
}

// One example question of each type, shaped like internal/seed/seed.go's
// coding questions (4 test cases, 2 hidden) so grading-pressure numbers
// match the plan's worked example. `n` lets a scenario mint several
// distinct questions per type without title collisions.
export function englishQuestionPayload(n = 1) {
  return {
    type: 'english',
    title: `Load Test English #${n}`,
    body: 'Read the passage and summarize the main point in 2-3 sentences.',
    hint: 'Focus on the central claim.',
    marks: 5,
    difficulty: 'medium',
  };
}

export function aptitudeQuestionPayload(n = 1) {
  return {
    type: 'aptitude',
    title: `Load Test Aptitude #${n}`,
    body: 'What is 2 + 2?',
    hint: 'Basic arithmetic.',
    marks: 2,
    difficulty: 'easy',
    options: ['3', '4', '5', '6'],
    correctIndex: 1,
  };
}

export function codingQuestionPayload(n = 1, opts = {}) {
  return {
    type: 'coding',
    title: `Load Test Coding #${n}`,
    body: 'Read two space-separated integers from stdin and print their sum.',
    hint: 'Use input().split()',
    marks: 10,
    difficulty: 'easy',
    starterCode: {
      python: '# write your solution here',
      java: '// write your solution here',
      c: '// write your solution here',
    },
    syntaxNote: 'Print only the sum, nothing else.',
    timeLimitMs: opts.timeLimitMs || 3000,
    memoryLimitKb: opts.memoryLimitKb || 0, // 0 = unlimited, per the Question model
    testCases: [
      { input: '2 2', expected: '4', hidden: false, weight: 1 },
      { input: '10 15', expected: '25', hidden: false, weight: 1 },
      { input: '100 200', expected: '300', hidden: true, weight: 1 },
      { input: '-5 5', expected: '0', hidden: true, weight: 1 },
    ],
  };
}

// Code fixtures per language/purpose, for the run-code stress scenarios.
export const CODE_FIXTURES = {
  python: {
    correctSum: 'a, b = map(int, input().split())\nprint(a + b)',
    infiniteLoop: 'while True:\n    pass',
    memoryHog: 'x = []\nwhile True:\n    x.append(bytearray(10_000_000))',
    compileError: 'def f(:\n    pass',
  },
  java: {
    correctSum:
      'import java.util.Scanner;\npublic class Main {\n  public static void main(String[] args) {\n    Scanner sc = new Scanner(System.in);\n    int a = sc.nextInt();\n    int b = sc.nextInt();\n    System.out.println(a + b);\n  }\n}',
    infiniteLoop: 'public class Main {\n  public static void main(String[] args) {\n    while (true) {}\n  }\n}',
    memoryHog:
      'import java.util.*;\npublic class Main {\n  public static void main(String[] args) {\n    List<byte[]> l = new ArrayList<>();\n    while (true) { l.add(new byte[10_000_000]); }\n  }\n}',
    wrongClassName:
      'public class Solution {\n  public static void main(String[] args) {\n    System.out.println("hi");\n  }\n}',
    compileError: 'public class Main {\n  public static void main(String[] args) {\n    int x = ;\n  }\n}',
  },
  c: {
    correctSum:
      '#include <stdio.h>\nint main() {\n  int a, b;\n  scanf("%d %d", &a, &b);\n  printf("%d\\n", a + b);\n  return 0;\n}',
    infiniteLoop: 'int main() {\n  while (1) {}\n  return 0;\n}',
    memoryHog:
      '#include <stdlib.h>\nint main() {\n  while (1) { if (!malloc(1024*1024*10)) break; }\n  return 0;\n}',
    compileError: 'int main( {\n  return 0\n}',
  },
};
