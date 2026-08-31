package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/services"
)

var setLabels = []string{"A", "B", "C", "D", "E", "F"}

type examPayload struct {
	Title         string `json:"title"`
	Instructions  string `json:"instructions"`
	DurationMin   int    `json:"durationMin"`
	MaxViolations int    `json:"maxViolations"`
}

func (a *API) ListExams(c *gin.Context) {
	var exams []models.Exam
	if err := a.DB.Order("id desc").Find(&exams).Error; err != nil {
		failServer(c, err)
		return
	}
	type row struct {
		models.Exam
		SessionCount int64 `json:"sessionCount"`
	}
	rows := make([]row, 0, len(exams))
	for _, e := range exams {
		var n int64
		a.DB.Model(&models.ExamSession{}).Where("exam_id = ?", e.ID).Count(&n)
		rows = append(rows, row{Exam: e, SessionCount: n})
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (a *API) CreateExam(c *gin.Context) {
	var p examPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(p.Title) == "" {
		fail(c, http.StatusBadRequest, "title is required")
		return
	}
	if p.DurationMin <= 0 {
		p.DurationMin = 60
	}
	if p.MaxViolations <= 0 {
		p.MaxViolations = 3
	}
	exam := models.Exam{
		Title:         strings.TrimSpace(p.Title),
		Instructions:  p.Instructions,
		DurationMin:   p.DurationMin,
		MaxViolations: p.MaxViolations,
		Status:        models.ExamDraft,
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&exam).Error; err != nil {
			return err
		}
		for _, label := range setLabels {
			if err := tx.Create(&models.QuestionSet{ExamID: exam.ID, Label: label}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusCreated, exam)
}

func (a *API) GetExam(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.Preload("Sets", func(db *gorm.DB) *gorm.DB { return db.Order("label") }).
		Preload("Sets.Questions", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Preload("Sets.Questions.Question").
		First(&exam, id).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}
	c.JSON(http.StatusOK, exam)
}

func (a *API) UpdateExam(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.First(&exam, id).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}
	var p examPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(p.Title) != "" {
		exam.Title = strings.TrimSpace(p.Title)
	}
	exam.Instructions = p.Instructions
	if p.DurationMin > 0 {
		exam.DurationMin = p.DurationMin
	}
	if p.MaxViolations > 0 {
		exam.MaxViolations = p.MaxViolations
	}
	if err := a.DB.Save(&exam).Error; err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, exam)
}

func (a *API) DeleteExam(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.First(&exam, id).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}
	if exam.Status != models.ExamDraft {
		fail(c, http.StatusBadRequest, "only draft exams can be deleted")
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var setIDs []uint
		if err := tx.Model(&models.QuestionSet{}).Where("exam_id = ?", id).Pluck("id", &setIDs).Error; err != nil {
			return err
		}
		if len(setIDs) > 0 {
			if err := tx.Where("set_id IN ?", setIDs).Delete(&models.SetQuestion{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("exam_id = ?", id).Delete(&models.QuestionSet{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Exam{}, id).Error
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (a *API) ActivateExam(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.Preload("Sets").First(&exam, id).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}
	for _, s := range exam.Sets {
		var n int64
		a.DB.Model(&models.SetQuestion{}).Where("set_id = ?", s.ID).Count(&n)
		if n == 0 {
			fail(c, http.StatusBadRequest, "set "+s.Label+" has no questions — fill all sets before activating")
			return
		}
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Exam{}).
			Where("status = ? AND id <> ?", models.ExamActive, id).
			Update("status", models.ExamClosed).Error; err != nil {
			return err
		}
		return tx.Model(&exam).Update("status", models.ExamActive).Error
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": models.ExamActive})
}

func (a *API) CloseExam(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	if err := a.DB.Model(&models.Exam{}).Where("id = ?", id).Update("status", models.ExamClosed).Error; err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": models.ExamClosed})
}

type setQuestionsPayload struct {
	QuestionIDs []uint `json:"questionIds"`
}

// UpdateSetQuestions replaces one set's question list (ordered).
func (a *API) UpdateSetQuestions(c *gin.Context) {
	setID, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var set models.QuestionSet
	if err := a.DB.First(&set, setID).Error; err != nil {
		fail(c, http.StatusNotFound, "set not found")
		return
	}
	var p setQuestionsPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var questions []models.Question
	if len(p.QuestionIDs) > 0 {
		if err := a.DB.Select("id", "marks").Where("id IN ?", p.QuestionIDs).Find(&questions).Error; err != nil {
			failServer(c, err)
			return
		}
		if len(questions) != len(uniqueIDs(p.QuestionIDs)) {
			fail(c, http.StatusBadRequest, "one or more question ids do not exist (or are duplicated)")
			return
		}
	}
	marksByID := map[uint]int{}
	for _, q := range questions {
		marksByID[q.ID] = q.Marks
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("set_id = ?", setID).Delete(&models.SetQuestion{}).Error; err != nil {
			return err
		}
		for pos, qid := range p.QuestionIDs {
			if err := tx.Create(&models.SetQuestion{
				SetID: setID, QuestionID: qid, Position: pos, Marks: marksByID[qid],
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(p.QuestionIDs)})
}

func uniqueIDs(ids []uint) []uint {
	seen := map[uint]bool{}
	var out []uint
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

type autoDistributePayload struct {
	English  int    `json:"english"`
	Aptitude int    `json:"aptitude"`
	Coding   int    `json:"coding"`
	Mode     string `json:"mode"`
}

func (a *API) AutoDistribute(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.First(&exam, id).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}
	var p autoDistributePayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if p.Mode == "" {
		p.Mode = services.DistributeShuffled
	}
	err := services.AutoDistribute(a.DB, exam.ID, services.DistributeCounts{
		English: p.English, Aptitude: p.Aptitude, Coding: p.Coding,
	}, p.Mode)
	if err != nil {
		if errors.Is(err, services.ErrBadDistribute) {
			fail(c, http.StatusBadRequest, err.Error())
		} else {
			failServer(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"distributed": true})
}
