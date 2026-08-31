package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
)

type testCasePayload struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Hidden   bool   `json:"hidden"`
	Weight   int    `json:"weight"`
}

type questionPayload struct {
	Type          string            `json:"type"`
	Title         string            `json:"title"`
	Body          string            `json:"body"`
	Hint          string            `json:"hint"`
	Marks         int               `json:"marks"`
	Difficulty    string            `json:"difficulty"`
	Options       []string          `json:"options"`
	CorrectIndex  *int              `json:"correctIndex"`
	StarterCode   map[string]string `json:"starterCode"`
	SyntaxNote    string            `json:"syntaxNote"`
	TimeLimitMS   int               `json:"timeLimitMs"`
	MemoryLimitKB int               `json:"memoryLimitKb"`
	TestCases     []testCasePayload `json:"testCases"`
}

func (p *questionPayload) validate() error {
	t := models.QuestionType(p.Type)
	if t != models.QuestionEnglish && t != models.QuestionAptitude && t != models.QuestionCoding {
		return fmt.Errorf("type must be english, aptitude or coding")
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(p.Body) == "" {
		return fmt.Errorf("body is required")
	}
	if p.Marks <= 0 {
		return fmt.Errorf("marks must be positive")
	}
	switch t {
	case models.QuestionAptitude:
		if len(p.Options) < 2 {
			return fmt.Errorf("MCQ needs at least 2 options")
		}
		for i, o := range p.Options {
			if strings.TrimSpace(o) == "" {
				return fmt.Errorf("option %d is empty", i+1)
			}
		}
		if p.CorrectIndex == nil || *p.CorrectIndex < 0 || *p.CorrectIndex >= len(p.Options) {
			return fmt.Errorf("correctIndex must point at one of the options")
		}
	case models.QuestionCoding:
		if len(p.TestCases) == 0 {
			return fmt.Errorf("coding question needs at least one test case")
		}
		hasVisible := false
		for _, tc := range p.TestCases {
			if !tc.Hidden {
				hasVisible = true
			}
		}
		if !hasVisible {
			return fmt.Errorf("coding question needs at least one visible (sample) test case")
		}
		for lang := range p.StarterCode {
			if !models.CodingLanguages[lang] {
				return fmt.Errorf("unsupported starter code language %q", lang)
			}
		}
	}
	return nil
}

func (p *questionPayload) apply(q *models.Question) {
	q.Type = models.QuestionType(p.Type)
	q.Title = strings.TrimSpace(p.Title)
	q.Body = p.Body
	q.Hint = p.Hint
	q.Marks = p.Marks
	q.Difficulty = p.Difficulty
	q.Options = p.Options
	q.CorrectIndex = p.CorrectIndex
	q.StarterCode = p.StarterCode
	q.SyntaxNote = p.SyntaxNote
	q.TimeLimitMS = p.TimeLimitMS
	q.MemoryLimitKB = p.MemoryLimitKB
	if q.Type == models.QuestionCoding && q.TimeLimitMS <= 0 {
		q.TimeLimitMS = 3000
	}
	if q.Type != models.QuestionAptitude {
		q.Options = nil
		q.CorrectIndex = nil
	}
	if q.Type != models.QuestionCoding {
		q.StarterCode = nil
		q.SyntaxNote = ""
		q.TimeLimitMS = 0
		q.MemoryLimitKB = 0
	}
}

func (p *questionPayload) buildTestCases(questionID uint) []models.TestCase {
	if models.QuestionType(p.Type) != models.QuestionCoding {
		return nil
	}
	out := make([]models.TestCase, 0, len(p.TestCases))
	for _, tc := range p.TestCases {
		w := tc.Weight
		if w <= 0 {
			w = 1
		}
		out = append(out, models.TestCase{
			QuestionID: questionID,
			Input:      tc.Input,
			Expected:   tc.Expected,
			Hidden:     tc.Hidden,
			Weight:     w,
		})
	}
	return out
}

func (a *API) ListQuestions(c *gin.Context) {
	q := a.DB.Model(&models.Question{})
	if t := c.Query("type"); t != "" {
		q = q.Where("type = ?", t)
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		q = q.Where("title ILIKE ?", "%"+search+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		failServer(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var items []models.Question
	if err := q.Preload("TestCases").Order("id desc").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (a *API) GetQuestion(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var q models.Question
	if err := a.DB.Preload("TestCases").First(&q, id).Error; err != nil {
		fail(c, http.StatusNotFound, "question not found")
		return
	}
	c.JSON(http.StatusOK, q)
}

func (a *API) CreateQuestion(c *gin.Context) {
	var p questionPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := p.validate(); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var q models.Question
	p.apply(&q)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&q).Error; err != nil {
			return err
		}
		tcs := p.buildTestCases(q.ID)
		if len(tcs) > 0 {
			if err := tx.Create(&tcs).Error; err != nil {
				return err
			}
			q.TestCases = tcs
		}
		return nil
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusCreated, q)
}

func (a *API) UpdateQuestion(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var q models.Question
	if err := a.DB.First(&q, id).Error; err != nil {
		fail(c, http.StatusNotFound, "question not found")
		return
	}
	var p questionPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := p.validate(); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	p.apply(&q)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("question_id = ?", q.ID).Delete(&models.TestCase{}).Error; err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{FullSaveAssociations: false}).Save(&q).Error; err != nil {
			return err
		}
		tcs := p.buildTestCases(q.ID)
		if len(tcs) > 0 {
			if err := tx.Create(&tcs).Error; err != nil {
				return err
			}
			q.TestCases = tcs
		} else {
			q.TestCases = nil
		}
		// keep marks in sync where this question is already assigned to sets
		return tx.Model(&models.SetQuestion{}).Where("question_id = ?", q.ID).Update("marks", q.Marks).Error
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, q)
}

func (a *API) DeleteQuestion(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("question_id = ?", id).Delete(&models.SetQuestion{}).Error; err != nil {
			return err
		}
		if err := tx.Where("question_id = ?", id).Delete(&models.TestCase{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Question{}, id).Error
	})
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
