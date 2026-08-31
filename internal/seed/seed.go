package seed

import (
	"log"

	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/services"
)

const demoExamTitle = "Campus Mock Test"

func intPtr(i int) *int { return &i }

// Run seeds a demo question bank and an activated exam with sets A–F.
// Idempotent: skips if the demo exam already exists.
func Run(db *gorm.DB) error {
	var count int64
	if err := db.Model(&models.Exam{}).Where("title = ?", demoExamTitle).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		log.Println("demo exam already exists, skipping seed")
		return nil
	}

	questions := englishQuestions()
	questions = append(questions, aptitudeQuestions()...)
	questions = append(questions, codingQuestions()...)
	for i := range questions {
		if err := db.Create(&questions[i]).Error; err != nil {
			return err
		}
	}
	log.Printf("created %d questions", len(questions))

	exam := models.Exam{
		Title:         demoExamTitle,
		DurationMin:   60,
		MaxViolations: 3,
		Status:        models.ExamDraft,
		Instructions: "Welcome to the Campus Mock Test.\n\n" +
			"**Rules**\n\n" +
			"- The exam runs in fullscreen. Leaving fullscreen, switching tabs or switching windows counts as a violation — after 3 violations your exam is submitted automatically.\n" +
			"- The exam has three sections: English, Aptitude and Coding.\n" +
			"- Coding questions accept Python, Java or C. For Java, keep the class named **Main**.\n" +
			"- Your answers save automatically. You can revisit and change any answer until you submit.\n" +
			"- The timer keeps running even if you close the browser — you can rejoin with the same email.\n\n" +
			"Good luck!",
	}
	if err := db.Create(&exam).Error; err != nil {
		return err
	}
	for _, label := range []string{"A", "B", "C", "D", "E", "F"} {
		if err := db.Create(&models.QuestionSet{ExamID: exam.ID, Label: label}).Error; err != nil {
			return err
		}
	}

	err := services.AutoDistribute(db, exam.ID, services.DistributeCounts{
		English: 2, Aptitude: 8, Coding: 3,
	}, services.DistributeShuffled)
	if err != nil {
		return err
	}
	if err := db.Model(&exam).Update("status", models.ExamActive).Error; err != nil {
		return err
	}
	log.Printf("created exam %q (60 min, sets A–F, active)", exam.Title)
	return nil
}

func englishQuestions() []models.Question {
	return []models.Question{
		{
			Type:  models.QuestionEnglish,
			Title: "Reading Comprehension: The Economics of Renewable Energy",
			Body: "Read the passage and answer the question below.\n\n" +
				"> Over the past decade, the cost of generating electricity from solar and wind has fallen faster than almost any mainstream forecast predicted. Solar photovoltaic costs have dropped by nearly 90 percent since 2010, and onshore wind by roughly 70 percent. This collapse in price was not driven by a single breakthrough but by thousands of incremental improvements in manufacturing, logistics, and installation, compounded across a rapidly growing global market. As a result, in most parts of the world, building new renewable capacity is now cheaper than merely operating existing coal plants. Critics rightly note that the sun does not always shine and the wind does not always blow, yet storage costs are following the same downward trajectory, suggesting that the intermittency problem is an engineering challenge with a visible price curve, not a permanent barrier.\n\n" +
				"**Question:** In 3–4 sentences, summarise the author's main argument and the key evidence used to support it.",
			Hint:       "Focus on what caused the cost decline and what the author says about the intermittency criticism.",
			Marks:      5,
			Difficulty: "medium",
		},
		{
			Type:  models.QuestionEnglish,
			Title: "Reading Comprehension: AI in the Classroom",
			Body: "Read the passage and answer the question below.\n\n" +
				"> When calculators entered classrooms in the 1970s, many educators predicted the death of arithmetic. Instead, curricula adapted: rote computation gave way to estimation, problem framing, and interpretation. Artificial intelligence now poses a similar, if larger, question. If a system can draft an essay in seconds, the lazy conclusion is that essays are obsolete. The more interesting conclusion is that the essay's purpose — forcing a mind to organise its own thoughts — must now be pursued deliberately rather than assumed. Teachers who treat AI as a collaborator report that students argue with the machine's drafts, spot its confident errors, and in doing so exercise precisely the critical faculties the technology was supposed to erode.\n\n" +
				"**Question:** What is the author's attitude towards AI in education? Support your answer with two phrases from the passage and explain what each suggests.",
			Hint:       "Compare how the author treats the calculator story with the AI question.",
			Marks:      5,
			Difficulty: "medium",
		},
	}
}

func aptitudeQuestions() []models.Question {
	return []models.Question{
		{
			Type: models.QuestionAptitude, Title: "Train Crossing a Pole",
			Body:    "A train 120 metres long is running at a speed of 60 km/h. How much time will it take to cross a pole?",
			Options: []string{"5.2 seconds", "7.2 seconds", "9 seconds", "12 seconds"}, CorrectIndex: intPtr(1),
			Hint: "Convert km/h to m/s first: multiply by 5/18.", Marks: 2, Difficulty: "easy",
		},
		{
			Type: models.QuestionAptitude, Title: "Number Series",
			Body:    "Find the next number in the series: 2, 6, 12, 20, 30, ?",
			Options: []string{"36", "40", "42", "48"}, CorrectIndex: intPtr(2),
			Hint: "Look at the differences between consecutive terms.", Marks: 2, Difficulty: "easy",
		},
		{
			Type: models.QuestionAptitude, Title: "Work and Time",
			Body:    "A can finish a piece of work in 12 days and B can finish the same work in 18 days. Working together, in how many days will they finish it?",
			Options: []string{"6.8 days", "7.2 days", "7.5 days", "8 days"}, CorrectIndex: intPtr(1),
			Hint: "Add their per-day work rates.", Marks: 2, Difficulty: "medium",
		},
		{
			Type: models.QuestionAptitude, Title: "Percentages",
			Body:    "What is 35% of 480?",
			Options: []string{"158", "162", "168", "172"}, CorrectIndex: intPtr(2),
			Hint: "10% of 480 is 48.", Marks: 2, Difficulty: "easy",
		},
		{
			Type: models.QuestionAptitude, Title: "Odd One Out",
			Body:    "Which number does not belong in the group: 5, 11, 14, 17?",
			Options: []string{"5", "11", "14", "17"}, CorrectIndex: intPtr(2),
			Hint: "Three of them share a well-known property.", Marks: 2, Difficulty: "easy",
		},
		{
			Type: models.QuestionAptitude, Title: "Simple Interest",
			Body:    "What is the simple interest on ₹5000 at 8% per annum for 3 years?",
			Options: []string{"₹1000", "₹1200", "₹1400", "₹1600"}, CorrectIndex: intPtr(1),
			Hint: "SI = P × R × T / 100.", Marks: 2, Difficulty: "easy",
		},
		{
			Type: models.QuestionAptitude, Title: "Letter Coding",
			Body:    "In a certain code, CODE is written as DPEF. How is GAME written in that code?",
			Options: []string{"HBNF", "HBMF", "FZLD", "HANF"}, CorrectIndex: intPtr(0),
			Hint: "Each letter shifts by the same amount.", Marks: 2, Difficulty: "easy",
		},
		{
			Type: models.QuestionAptitude, Title: "Averages",
			Body:    "What is the average of the first 20 natural numbers?",
			Options: []string{"10", "10.5", "11", "11.5"}, CorrectIndex: intPtr(1),
			Hint: "Sum of first n natural numbers is n(n+1)/2.", Marks: 2, Difficulty: "easy",
		},
	}
}

func codingQuestions() []models.Question {
	return []models.Question{
		{
			Type:  models.QuestionCoding,
			Title: "Sum of Two Numbers",
			Body: "Read two integers from a single line of standard input (separated by a space) and print their sum.\n\n" +
				"**Input format:** one line with two integers `a` and `b` (−10⁹ ≤ a, b ≤ 10⁹)\n\n" +
				"**Output format:** a single integer — the sum.\n\n" +
				"**Example**\n\n    Input:  3 5\n    Output: 8",
			Hint:        "Read the whole line, split it, convert both parts to integers.",
			Marks:       10,
			Difficulty:  "easy",
			TimeLimitMS: 3000,
			SyntaxNote: "**Reading input**\n\n" +
				"Python:\n\n    a, b = map(int, input().split())\n\n" +
				"Java (class must be named Main):\n\n    Scanner sc = new Scanner(System.in);\n    int a = sc.nextInt(), b = sc.nextInt();\n\n" +
				"C:\n\n    int a, b;\n    scanf(\"%d %d\", &a, &b);",
			StarterCode: map[string]string{
				"python": "# Read two integers and print their sum\na, b = map(int, input().split())\n# TODO: print the sum\n",
				"java":   "import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        Scanner sc = new Scanner(System.in);\n        int a = sc.nextInt();\n        int b = sc.nextInt();\n        // TODO: print the sum\n    }\n}\n",
				"c":      "#include <stdio.h>\n\nint main(void) {\n    int a, b;\n    scanf(\"%d %d\", &a, &b);\n    // TODO: print the sum\n    return 0;\n}\n",
			},
			TestCases: []models.TestCase{
				{Input: "3 5", Expected: "8", Hidden: false, Weight: 1},
				{Input: "10 20", Expected: "30", Hidden: false, Weight: 1},
				{Input: "-7 2", Expected: "-5", Hidden: true, Weight: 1},
				{Input: "100000 250000", Expected: "350000", Hidden: true, Weight: 1},
			},
		},
		{
			Type:  models.QuestionCoding,
			Title: "Reverse a String",
			Body: "Read one line of text from standard input and print it reversed.\n\n" +
				"**Input format:** a single line (may contain spaces)\n\n" +
				"**Output format:** the same line reversed.\n\n" +
				"**Example**\n\n    Input:  abcd\n    Output: dcba",
			Hint:        "In Python, slicing with [::-1] reverses a string. In C, read with fgets and print from the end.",
			Marks:       10,
			Difficulty:  "easy",
			TimeLimitMS: 3000,
			SyntaxNote: "**Reading a full line**\n\n" +
				"Python:\n\n    s = input()\n\n" +
				"Java:\n\n    Scanner sc = new Scanner(System.in);\n    String s = sc.nextLine();\n\n" +
				"C (watch out for the trailing newline):\n\n    char s[1005];\n    fgets(s, sizeof(s), stdin);",
			StarterCode: map[string]string{
				"python": "s = input()\n# TODO: print the reversed string\n",
				"java":   "import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        Scanner sc = new Scanner(System.in);\n        String s = sc.nextLine();\n        // TODO: print the reversed string\n    }\n}\n",
				"c":      "#include <stdio.h>\n#include <string.h>\n\nint main(void) {\n    char s[1005];\n    if (fgets(s, sizeof(s), stdin) == NULL) return 0;\n    s[strcspn(s, \"\\n\")] = 0; // strip trailing newline\n    // TODO: print the reversed string\n    return 0;\n}\n",
			},
			TestCases: []models.TestCase{
				{Input: "abcd", Expected: "dcba", Hidden: false, Weight: 1},
				{Input: "racecar", Expected: "racecar", Hidden: false, Weight: 1},
				{Input: "hello world", Expected: "dlrow olleh", Hidden: true, Weight: 1},
				{Input: "a", Expected: "a", Hidden: true, Weight: 1},
			},
		},
		{
			Type:  models.QuestionCoding,
			Title: "Nth Fibonacci Number",
			Body: "The Fibonacci sequence is defined as F(1) = 1, F(2) = 1 and F(n) = F(n−1) + F(n−2) for n > 2.\n\n" +
				"Read an integer n (1 ≤ n ≤ 40) and print F(n).\n\n" +
				"**Example**\n\n    Input:  5\n    Output: 5",
			Hint:        "A simple loop with two variables is enough — no recursion needed.",
			Marks:       10,
			Difficulty:  "medium",
			TimeLimitMS: 3000,
			SyntaxNote: "**Loop syntax**\n\n" +
				"Python:\n\n    for i in range(n):\n        ...\n\n" +
				"Java / C:\n\n    for (int i = 0; i < n; i++) {\n        ...\n    }",
			StarterCode: map[string]string{
				"python": "n = int(input())\n# TODO: print the nth Fibonacci number (F(1) = 1, F(2) = 1)\n",
				"java":   "import java.util.*;\n\npublic class Main {\n    public static void main(String[] args) {\n        Scanner sc = new Scanner(System.in);\n        int n = sc.nextInt();\n        // TODO: print the nth Fibonacci number (F(1) = 1, F(2) = 1)\n    }\n}\n",
				"c":      "#include <stdio.h>\n\nint main(void) {\n    int n;\n    scanf(\"%d\", &n);\n    // TODO: print the nth Fibonacci number (F(1) = 1, F(2) = 1)\n    return 0;\n}\n",
			},
			TestCases: []models.TestCase{
				{Input: "1", Expected: "1", Hidden: false, Weight: 1},
				{Input: "5", Expected: "5", Hidden: false, Weight: 1},
				{Input: "10", Expected: "55", Hidden: true, Weight: 1},
				{Input: "30", Expected: "832040", Hidden: true, Weight: 2},
			},
		},
	}
}
