package models

import "time"

type QuestionType string

const (
	QuestionEnglish  QuestionType = "english"
	QuestionAptitude QuestionType = "aptitude"
	QuestionCoding   QuestionType = "coding"
)

const (
	ExamDraft  = "draft"
	ExamActive = "active"
	ExamClosed = "closed"

	SessionActive  = "active"
	SessionGrading = "grading"
	SessionGraded  = "graded"

	SubmitManual        = "manual"
	SubmitAutoTime      = "auto_time"
	SubmitAutoViolation = "auto_violation"
)

// StrikeKinds count toward the auto-submit limit; everything else is logged only.
var StrikeKinds = map[string]bool{
	"tab_hidden":      true,
	"window_blur":     true,
	"fullscreen_exit": true,
}

var LoggedKinds = map[string]bool{
	"copy":        true,
	"paste":       true,
	"cut":         true,
	"contextmenu": true,
	"reload":      true,
}

var CodingLanguages = map[string]bool{"python": true, "java": true, "c": true}

type Admin struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Email        string    `gorm:"uniqueIndex;size:255" json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Question struct {
	ID         uint         `gorm:"primaryKey" json:"id"`
	Type       QuestionType `gorm:"index;size:20" json:"type"`
	Title      string       `gorm:"size:500" json:"title"`
	Body       string       `json:"body"` // markdown; for english: the paragraph + question prompt
	Hint       string       `json:"hint"`
	Marks      int          `json:"marks"`
	Difficulty string       `gorm:"size:20" json:"difficulty"`

	// Aptitude (MCQ)
	Options      []string `gorm:"serializer:json" json:"options"`
	CorrectIndex *int     `json:"correctIndex"` // stripped from student-facing payloads

	// Coding
	StarterCode   map[string]string `gorm:"serializer:json" json:"starterCode"` // keys: python, java, c
	SyntaxNote    string            `json:"syntaxNote"`
	TimeLimitMS   int               `json:"timeLimitMs"`
	MemoryLimitKB int               `json:"memoryLimitKb"`

	TestCases []TestCase `json:"testCases"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

type TestCase struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	QuestionID uint   `gorm:"index" json:"questionId"`
	Input      string `json:"input"`
	Expected   string `json:"expected"`
	Hidden     bool   `json:"hidden"`
	Weight     int    `json:"weight"` // defaults to 1
}

type Exam struct {
	ID            uint          `gorm:"primaryKey" json:"id"`
	Title         string        `gorm:"size:500" json:"title"`
	Instructions  string        `json:"instructions"`
	DurationMin   int           `json:"durationMin"`
	MaxViolations int           `json:"maxViolations"`
	Status        string        `gorm:"index;size:20" json:"status"`
	Sets          []QuestionSet `json:"sets,omitempty"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
}

type QuestionSet struct {
	ID        uint          `gorm:"primaryKey" json:"id"`
	ExamID    uint          `gorm:"index;uniqueIndex:idx_exam_label" json:"examId"`
	Label     string        `gorm:"size:2;uniqueIndex:idx_exam_label" json:"label"` // A..F
	Questions []SetQuestion `gorm:"foreignKey:SetID" json:"questions,omitempty"`
}

type SetQuestion struct {
	ID         uint     `gorm:"primaryKey" json:"id"`
	SetID      uint     `gorm:"index;uniqueIndex:idx_set_question" json:"setId"`
	QuestionID uint     `gorm:"uniqueIndex:idx_set_question" json:"questionId"`
	Position   int      `json:"position"`
	Marks      int      `json:"marks"` // copied from the question when assigned
	Question   Question `json:"question"`
}

type Student struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:255" json:"name"`
	Email     string    `gorm:"uniqueIndex;size:255" json:"email"`
	Semester  int       `json:"semester"`
	Phone     string    `gorm:"size:32" json:"phone"`
	CreatedAt time.Time `json:"createdAt"`
}

type ExamSession struct {
	ID             uint         `gorm:"primaryKey" json:"id"`
	Token          string       `gorm:"uniqueIndex;size:64" json:"-"`
	StudentID      uint         `gorm:"uniqueIndex:idx_exam_student" json:"studentId"`
	Student        Student      `json:"student"`
	ExamID         uint         `gorm:"index;uniqueIndex:idx_exam_student" json:"examId"`
	Exam           Exam         `json:"-"`
	SetID          uint         `json:"setId"`
	Set            *QuestionSet `json:"set,omitempty"`
	Status         string       `gorm:"index;size:20" json:"status"`
	SubmitKind     string       `gorm:"size:20" json:"submitKind"`
	Flagged        bool         `json:"flagged"`
	ViolationCount int          `json:"violationCount"`
	AutoScore      float64      `json:"autoScore"`
	ManualScore    float64      `json:"manualScore"`
	TotalScore     float64      `json:"totalScore"`
	StartedAt      time.Time    `json:"startedAt"`
	EndsAt         time.Time    `json:"endsAt"`
	SubmittedAt    *time.Time   `json:"submittedAt"`
	LastSeenAt     time.Time    `json:"lastSeenAt"`
	CreatedAt      time.Time    `json:"createdAt"`
}

type TestResult struct {
	Index    int    `json:"index"`
	Hidden   bool   `json:"hidden"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Status   string `json:"status"` // passed | failed | timeout | error | compile_error
	Passed   bool   `json:"passed"`
	Weight   int    `json:"weight"`
}

type Answer struct {
	ID            uint         `gorm:"primaryKey" json:"id"`
	SessionID     uint         `gorm:"uniqueIndex:idx_session_question" json:"sessionId"`
	QuestionID    uint         `gorm:"uniqueIndex:idx_session_question" json:"questionId"`
	Type          QuestionType `gorm:"size:20" json:"type"`
	AnswerText    string       `json:"answerText"`
	SelectedIndex *int         `json:"selectedIndex"`
	Code          string       `json:"code"`
	Language      string       `gorm:"size:20" json:"language"`
	Score         float64      `json:"score"`
	Graded        bool         `json:"graded"`
	TestResults   []TestResult `gorm:"serializer:json" json:"testResults"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

type Violation struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID uint      `gorm:"index" json:"sessionId"`
	Kind      string    `gorm:"size:32" json:"kind"`
	Strike    bool      `json:"strike"`
	Meta      string    `json:"meta"`
	CreatedAt time.Time `json:"createdAt"`
}

// AllModels is the auto-migration list.
func AllModels() []any {
	return []any{
		&Admin{}, &Question{}, &TestCase{}, &Exam{}, &QuestionSet{},
		&SetQuestion{}, &Student{}, &ExamSession{}, &Answer{}, &Violation{},
	}
}
