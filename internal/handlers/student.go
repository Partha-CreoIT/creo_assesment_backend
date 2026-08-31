package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"exam_taker_bc/internal/middleware"
	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/services"
)

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
var phoneRe = regexp.MustCompile(`^[0-9+\-\s]{7,15}$`)

func (a *API) ActiveExam(c *gin.Context) {
	var exam models.Exam
	err := a.DB.Where("status = ?", models.ExamActive).Order("updated_at desc").First(&exam).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, gin.H{"exam": nil})
			return
		}
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"exam": gin.H{
		"id":            exam.ID,
		"title":         exam.Title,
		"instructions":  exam.Instructions,
		"durationMin":   exam.DurationMin,
		"maxViolations": exam.MaxViolations,
	}})
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Semester int    `json:"semester"`
	Phone    string `json:"phone"`
}

func (r *registerRequest) validate() error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
	r.Phone = strings.TrimSpace(r.Phone)
	if len(r.Name) < 2 {
		return errors.New("please enter your full name")
	}
	if !emailRe.MatchString(r.Email) {
		return errors.New("please enter a valid email address")
	}
	if r.Semester < 1 || r.Semester > 8 {
		return errors.New("semester must be between 1 and 8")
	}
	if r.Phone != "" && !phoneRe.MatchString(r.Phone) {
		return errors.New("please enter a valid phone number")
	}
	return nil
}

func (a *API) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := req.validate(); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	var exam models.Exam
	if err := a.DB.Where("status = ?", models.ExamActive).Order("updated_at desc").First(&exam).Error; err != nil {
		fail(c, http.StatusNotFound, "no exam is currently active")
		return
	}

	// find-or-create the student by email
	var student models.Student
	err := a.DB.Where("email = ?", req.Email).First(&student).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		student = models.Student{Name: req.Name, Email: req.Email, Semester: req.Semester, Phone: req.Phone}
		if err := a.DB.Create(&student).Error; err != nil {
			failServer(c, err)
			return
		}
	} else if err != nil {
		failServer(c, err)
		return
	} else {
		a.DB.Model(&student).Updates(map[string]any{"name": req.Name, "semester": req.Semester, "phone": req.Phone})
	}

	respond := func(s *models.ExamSession, resumed bool, code int) {
		c.JSON(code, gin.H{
			"token":     s.Token,
			"resumed":   resumed,
			"endsAt":    s.EndsAt,
			"serverNow": time.Now().UTC(),
			"exam": gin.H{
				"id":            exam.ID,
				"title":         exam.Title,
				"instructions":  exam.Instructions,
				"durationMin":   exam.DurationMin,
				"maxViolations": exam.MaxViolations,
			},
		})
	}

	var existing models.ExamSession
	err = a.DB.Where("exam_id = ? AND student_id = ?", exam.ID, student.ID).First(&existing).Error
	if err == nil {
		if existing.Status == models.SessionActive {
			grace := time.Duration(a.Cfg.AnswerGraceSec) * time.Second
			if time.Now().UTC().After(existing.EndsAt.Add(grace)) {
				_ = services.FinalizeSession(a.DB, a.Grader, existing.ID, models.SubmitAutoTime)
				fail(c, http.StatusConflict, "your exam time is over — the attempt was submitted automatically")
				return
			}
			respond(&existing, true, http.StatusOK)
			return
		}
		fail(c, http.StatusConflict, "this email has already attempted the exam")
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		failServer(c, err)
		return
	}

	set, err := services.AssignSet(a.DB, exam.ID)
	if err != nil {
		failServer(c, err)
		return
	}
	now := time.Now().UTC()
	session := models.ExamSession{
		Token:      uuid.NewString(),
		StudentID:  student.ID,
		ExamID:     exam.ID,
		SetID:      set.ID,
		Status:     models.SessionActive,
		StartedAt:  now,
		EndsAt:     now.Add(time.Duration(exam.DurationMin) * time.Minute),
		LastSeenAt: now,
	}
	if err := a.DB.Create(&session).Error; err != nil {
		// double-submit race on the unique (exam, student) index → resume the winner
		if err2 := a.DB.Where("exam_id = ? AND student_id = ?", exam.ID, student.ID).First(&existing).Error; err2 == nil {
			respond(&existing, true, http.StatusOK)
			return
		}
		failServer(c, err)
		return
	}
	respond(&session, false, http.StatusCreated)
}

func (a *API) Me(c *gin.Context) {
	s := middleware.GetSession(c)
	var set models.QuestionSet
	a.DB.First(&set, s.SetID)
	c.JSON(http.StatusOK, gin.H{
		"status":         s.Status,
		"studentName":    s.Student.Name,
		"setLabel":       set.Label,
		"violationCount": s.ViolationCount,
		"maxViolations":  s.Exam.MaxViolations,
		"submitKind":     s.SubmitKind,
		"startedAt":      s.StartedAt,
		"endsAt":         s.EndsAt,
		"submittedAt":    s.SubmittedAt,
		"serverNow":      time.Now().UTC(),
		"exam": gin.H{
			"id":            s.Exam.ID,
			"title":         s.Exam.Title,
			"instructions":  s.Exam.Instructions,
			"durationMin":   s.Exam.DurationMin,
			"maxViolations": s.Exam.MaxViolations,
		},
	})
}

type sampleTest struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

type paperQuestion struct {
	ID              uint                `json:"id"`
	Type            models.QuestionType `json:"type"`
	Title           string              `json:"title"`
	Body            string              `json:"body"`
	Hint            string              `json:"hint"`
	Marks           int                 `json:"marks"`
	Position        int                 `json:"position"`
	Difficulty      string              `json:"difficulty"`
	Options         []string            `json:"options,omitempty"`
	StarterCode     map[string]string   `json:"starterCode,omitempty"`
	SyntaxNote      string              `json:"syntaxNote,omitempty"`
	TimeLimitMS     int                 `json:"timeLimitMs,omitempty"`
	SampleTests     []sampleTest        `json:"sampleTests,omitempty"`
	HiddenTestCount int                 `json:"hiddenTestCount,omitempty"`
}

type savedAnswer struct {
	QuestionID    uint   `json:"questionId"`
	AnswerText    string `json:"answerText"`
	SelectedIndex *int   `json:"selectedIndex"`
	Code          string `json:"code"`
	Language      string `json:"language"`
}

// Paper returns the student's assigned question paper, stripped of correct
// answers and hidden test cases, plus previously saved answers (for resume).
func (a *API) Paper(c *gin.Context) {
	s := middleware.GetSession(c)
	if s.Status != models.SessionActive {
		fail(c, http.StatusConflict, "exam already submitted")
		return
	}

	var set models.QuestionSet
	if err := a.DB.First(&set, s.SetID).Error; err != nil {
		failServer(c, err)
		return
	}
	var setQuestions []models.SetQuestion
	if err := a.DB.Where("set_id = ?", s.SetID).
		Preload("Question.TestCases").Order("position").Find(&setQuestions).Error; err != nil {
		failServer(c, err)
		return
	}

	questions := make([]paperQuestion, 0, len(setQuestions))
	for _, sq := range setQuestions {
		q := sq.Question
		pq := paperQuestion{
			ID: q.ID, Type: q.Type, Title: q.Title, Body: q.Body, Hint: q.Hint,
			Marks: sq.Marks, Position: sq.Position, Difficulty: q.Difficulty,
		}
		switch q.Type {
		case models.QuestionAptitude:
			pq.Options = q.Options
		case models.QuestionCoding:
			pq.StarterCode = q.StarterCode
			pq.SyntaxNote = q.SyntaxNote
			pq.TimeLimitMS = q.TimeLimitMS
			for _, tc := range q.TestCases {
				if tc.Hidden {
					pq.HiddenTestCount++
				} else {
					pq.SampleTests = append(pq.SampleTests, sampleTest{Input: tc.Input, Expected: tc.Expected})
				}
			}
		}
		questions = append(questions, pq)
	}

	var answers []models.Answer
	if err := a.DB.Where("session_id = ?", s.ID).Find(&answers).Error; err != nil {
		failServer(c, err)
		return
	}
	saved := make([]savedAnswer, 0, len(answers))
	for _, an := range answers {
		saved = append(saved, savedAnswer{
			QuestionID: an.QuestionID, AnswerText: an.AnswerText,
			SelectedIndex: an.SelectedIndex, Code: an.Code, Language: an.Language,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"exam": gin.H{
			"id":            s.Exam.ID,
			"title":         s.Exam.Title,
			"instructions":  s.Exam.Instructions,
			"durationMin":   s.Exam.DurationMin,
			"maxViolations": s.Exam.MaxViolations,
		},
		"studentName":    s.Student.Name,
		"setLabel":       set.Label,
		"status":         s.Status,
		"violationCount": s.ViolationCount,
		"startedAt":      s.StartedAt,
		"endsAt":         s.EndsAt,
		"serverNow":      time.Now().UTC(),
		"questions":      questions,
		"answers":        saved,
	})
}

func (a *API) Heartbeat(c *gin.Context) {
	s := middleware.GetSession(c)
	a.DB.Model(&models.ExamSession{}).Where("id = ?", s.ID).Update("last_seen_at", time.Now().UTC())
	// re-read status so the client notices a server-side auto-submit
	var fresh models.ExamSession
	a.DB.Select("status", "violation_count", "ends_at").First(&fresh, s.ID)
	c.JSON(http.StatusOK, gin.H{
		"status":         fresh.Status,
		"violationCount": fresh.ViolationCount,
		"endsAt":         fresh.EndsAt,
		"serverNow":      time.Now().UTC(),
	})
}
