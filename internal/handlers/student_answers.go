package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"exam_taker_bc/internal/middleware"
	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/services"
)

func (a *API) sessionWritable(c *gin.Context, s *models.ExamSession) bool {
	if s.Status != models.SessionActive {
		fail(c, http.StatusConflict, "exam is already submitted")
		return false
	}
	grace := time.Duration(a.Cfg.AnswerGraceSec) * time.Second
	if time.Now().UTC().After(s.EndsAt.Add(grace)) {
		_ = services.FinalizeSession(a.DB, a.Grader, s.ID, models.SubmitAutoTime)
		fail(c, http.StatusConflict, "exam time is over")
		return false
	}
	return true
}

// setQuestion verifies the question belongs to the session's assigned set.
func (a *API) setQuestion(c *gin.Context, s *models.ExamSession, questionID uint) (*models.SetQuestion, *models.Question, bool) {
	var sq models.SetQuestion
	err := a.DB.Where("set_id = ? AND question_id = ?", s.SetID, questionID).First(&sq).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, "question is not part of your paper")
		} else {
			failServer(c, err)
		}
		return nil, nil, false
	}
	var q models.Question
	if err := a.DB.Preload("TestCases").First(&q, questionID).Error; err != nil {
		failServer(c, err)
		return nil, nil, false
	}
	return &sq, &q, true
}

type answerPayload struct {
	AnswerText    string `json:"answerText"`
	SelectedIndex *int   `json:"selectedIndex"`
	Code          string `json:"code"`
	Language      string `json:"language"`
}

func (a *API) UpsertAnswer(c *gin.Context) {
	s := middleware.GetSession(c)
	if !a.sessionWritable(c, s) {
		return
	}
	questionID, ok := paramUint(c, "questionId")
	if !ok {
		return
	}
	_, q, ok := a.setQuestion(c, s, questionID)
	if !ok {
		return
	}
	var p answerPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}

	answer := models.Answer{
		SessionID:  s.ID,
		QuestionID: q.ID,
		Type:       q.Type,
	}
	switch q.Type {
	case models.QuestionEnglish:
		if len(p.AnswerText) > 20000 {
			fail(c, http.StatusBadRequest, "answer is too long")
			return
		}
		answer.AnswerText = p.AnswerText
	case models.QuestionAptitude:
		if p.SelectedIndex != nil && (*p.SelectedIndex < 0 || *p.SelectedIndex >= len(q.Options)) {
			fail(c, http.StatusBadRequest, "selected option out of range")
			return
		}
		answer.SelectedIndex = p.SelectedIndex
	case models.QuestionCoding:
		if len(p.Code) > 65536 {
			fail(c, http.StatusBadRequest, "code is too long")
			return
		}
		if p.Language != "" && !models.CodingLanguages[p.Language] {
			fail(c, http.StatusBadRequest, "language must be python, java or c")
			return
		}
		answer.Code = p.Code
		answer.Language = p.Language
	}

	now := time.Now().UTC()
	answer.UpdatedAt = now
	err := a.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "session_id"}, {Name: "question_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"answer_text", "selected_index", "code", "language", "updated_at",
		}),
	}).Create(&answer).Error
	if err != nil {
		failServer(c, err)
		return
	}
	a.DB.Model(&models.ExamSession{}).Where("id = ?", s.ID).Update("last_seen_at", now)
	c.JSON(http.StatusOK, gin.H{"savedAt": now})
}

type violationPayload struct {
	Kind string `json:"kind"`
	Meta string `json:"meta"`
}

func (a *API) ReportViolation(c *gin.Context) {
	s := middleware.GetSession(c)
	var p violationPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	isStrike := models.StrikeKinds[p.Kind]
	if !isStrike && !models.LoggedKinds[p.Kind] {
		fail(c, http.StatusBadRequest, "unknown violation kind")
		return
	}
	if len(p.Meta) > 500 {
		p.Meta = p.Meta[:500]
	}
	if err := a.DB.Create(&models.Violation{
		SessionID: s.ID, Kind: p.Kind, Strike: isStrike, Meta: p.Meta,
	}).Error; err != nil {
		failServer(c, err)
		return
	}

	autoSubmitted := false
	count := s.ViolationCount
	if isStrike && s.Status == models.SessionActive {
		if err := a.DB.Model(&models.ExamSession{}).Where("id = ?", s.ID).
			Update("violation_count", gorm.Expr("violation_count + 1")).Error; err != nil {
			failServer(c, err)
			return
		}
		var fresh models.ExamSession
		if err := a.DB.Select("violation_count", "status").First(&fresh, s.ID).Error; err != nil {
			failServer(c, err)
			return
		}
		count = fresh.ViolationCount
		if count >= s.Exam.MaxViolations {
			err := services.FinalizeSession(a.DB, a.Grader, s.ID, models.SubmitAutoViolation)
			if err == nil || errors.Is(err, services.ErrNotActive) {
				autoSubmitted = true
			} else {
				failServer(c, err)
				return
			}
		}
	} else if s.Status != models.SessionActive {
		autoSubmitted = true
	}

	c.JSON(http.StatusOK, gin.H{
		"violationCount": count,
		"maxViolations":  s.Exam.MaxViolations,
		"strike":         isStrike,
		"autoSubmitted":  autoSubmitted,
	})
}

func (a *API) Submit(c *gin.Context) {
	s := middleware.GetSession(c)
	err := services.FinalizeSession(a.DB, a.Grader, s.ID, models.SubmitManual)
	if err != nil && !errors.Is(err, services.ErrNotActive) {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": models.SessionGrading, "submittedAt": time.Now().UTC()})
}
