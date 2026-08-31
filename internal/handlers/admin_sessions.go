package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/services"
)

type monitorRow struct {
	ID             uint           `json:"id"`
	Student        models.Student `json:"student"`
	SetLabel       string         `json:"setLabel"`
	Status         string         `json:"status"`
	SubmitKind     string         `json:"submitKind"`
	Flagged        bool           `json:"flagged"`
	ViolationCount int            `json:"violationCount"`
	AnsweredCount  int64          `json:"answeredCount"`
	TotalQuestions int64          `json:"totalQuestions"`
	PendingEnglish int64          `json:"pendingEnglish"`
	AutoScore      float64        `json:"autoScore"`
	ManualScore    float64        `json:"manualScore"`
	TotalScore     float64        `json:"totalScore"`
	StartedAt      time.Time      `json:"startedAt"`
	EndsAt         time.Time      `json:"endsAt"`
	SubmittedAt    *time.Time     `json:"submittedAt"`
	LastSeenAt     time.Time      `json:"lastSeenAt"`
}

func (a *API) MonitorSessions(c *gin.Context) {
	examID, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var sessions []models.ExamSession
	if err := a.DB.Where("exam_id = ?", examID).
		Preload("Student").Preload("Set").
		Order("created_at").Find(&sessions).Error; err != nil {
		failServer(c, err)
		return
	}
	if len(sessions) == 0 {
		c.JSON(http.StatusOK, gin.H{"items": []monitorRow{}, "serverNow": time.Now().UTC()})
		return
	}

	sessionIDs := make([]uint, len(sessions))
	setIDs := map[uint]bool{}
	for i, s := range sessions {
		sessionIDs[i] = s.ID
		setIDs[s.SetID] = true
	}

	type countRow struct {
		Key uint
		N   int64
	}
	answered := map[uint]int64{}
	var rows []countRow
	a.DB.Model(&models.Answer{}).
		Select("session_id as key, count(*) as n").
		Where("session_id IN ? AND (answer_text <> '' OR selected_index IS NOT NULL OR code <> '')", sessionIDs).
		Group("session_id").Scan(&rows)
	for _, r := range rows {
		answered[r.Key] = r.N
	}

	pending := map[uint]int64{}
	rows = nil
	a.DB.Model(&models.Answer{}).
		Select("session_id as key, count(*) as n").
		Where("session_id IN ? AND type = ? AND graded = false AND answer_text <> ''", sessionIDs, models.QuestionEnglish).
		Group("session_id").Scan(&rows)
	for _, r := range rows {
		pending[r.Key] = r.N
	}

	setTotals := map[uint]int64{}
	rows = nil
	ids := make([]uint, 0, len(setIDs))
	for id := range setIDs {
		ids = append(ids, id)
	}
	a.DB.Model(&models.SetQuestion{}).
		Select("set_id as key, count(*) as n").
		Where("set_id IN ?", ids).
		Group("set_id").Scan(&rows)
	for _, r := range rows {
		setTotals[r.Key] = r.N
	}

	out := make([]monitorRow, 0, len(sessions))
	for _, s := range sessions {
		label := ""
		if s.Set != nil {
			label = s.Set.Label
		}
		out = append(out, monitorRow{
			ID: s.ID, Student: s.Student, SetLabel: label,
			Status: s.Status, SubmitKind: s.SubmitKind, Flagged: s.Flagged,
			ViolationCount: s.ViolationCount,
			AnsweredCount:  answered[s.ID], TotalQuestions: setTotals[s.SetID],
			PendingEnglish: pending[s.ID],
			AutoScore:      s.AutoScore, ManualScore: s.ManualScore, TotalScore: s.TotalScore,
			StartedAt: s.StartedAt, EndsAt: s.EndsAt, SubmittedAt: s.SubmittedAt, LastSeenAt: s.LastSeenAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "serverNow": time.Now().UTC()})
}

func (a *API) SessionDetail(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var session models.ExamSession
	if err := a.DB.Preload("Student").Preload("Set").Preload("Exam").First(&session, id).Error; err != nil {
		fail(c, http.StatusNotFound, "session not found")
		return
	}
	var setQuestions []models.SetQuestion
	if err := a.DB.Where("set_id = ?", session.SetID).
		Preload("Question.TestCases").Order("position").Find(&setQuestions).Error; err != nil {
		failServer(c, err)
		return
	}
	var answers []models.Answer
	if err := a.DB.Where("session_id = ?", id).Find(&answers).Error; err != nil {
		failServer(c, err)
		return
	}
	byQ := map[uint]*models.Answer{}
	for i := range answers {
		byQ[answers[i].QuestionID] = &answers[i]
	}
	type item struct {
		Position int              `json:"position"`
		Marks    int              `json:"marks"`
		Question models.Question  `json:"question"`
		Answer   *models.Answer   `json:"answer"`
	}
	items := make([]item, 0, len(setQuestions))
	for _, sq := range setQuestions {
		items = append(items, item{Position: sq.Position, Marks: sq.Marks, Question: sq.Question, Answer: byQ[sq.QuestionID]})
	}
	var violations []models.Violation
	a.DB.Where("session_id = ?", id).Order("created_at").Find(&violations)

	label := ""
	if session.Set != nil {
		label = session.Set.Label
	}
	c.JSON(http.StatusOK, gin.H{
		"session":    session,
		"examTitle":  session.Exam.Title,
		"setLabel":   label,
		"items":      items,
		"violations": violations,
	})
}

type gradePayload struct {
	Score float64 `json:"score"`
}

func (a *API) GradeAnswer(c *gin.Context) {
	id, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var answer models.Answer
	if err := a.DB.First(&answer, id).Error; err != nil {
		fail(c, http.StatusNotFound, "answer not found")
		return
	}
	if answer.Type != models.QuestionEnglish {
		fail(c, http.StatusBadRequest, "only english answers are graded manually")
		return
	}
	var p gradePayload
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var session models.ExamSession
	if err := a.DB.First(&session, answer.SessionID).Error; err != nil {
		failServer(c, err)
		return
	}
	var sq models.SetQuestion
	maxMarks := 0.0
	if err := a.DB.Where("set_id = ? AND question_id = ?", session.SetID, answer.QuestionID).First(&sq).Error; err == nil {
		maxMarks = float64(sq.Marks)
	}
	if p.Score < 0 {
		p.Score = 0
	}
	if maxMarks > 0 && p.Score > maxMarks {
		fail(c, http.StatusBadRequest, fmt.Sprintf("score cannot exceed %g marks", maxMarks))
		return
	}
	if err := a.DB.Model(&answer).Updates(map[string]any{"score": p.Score, "graded": true}).Error; err != nil {
		failServer(c, err)
		return
	}
	if err := services.RecomputeManual(a.DB, answer.SessionID); err != nil {
		failServer(c, err)
		return
	}
	a.DB.First(&session, answer.SessionID)
	c.JSON(http.StatusOK, gin.H{
		"score":       p.Score,
		"manualScore": session.ManualScore,
		"totalScore":  session.TotalScore,
	})
}

func (a *API) ExportCSV(c *gin.Context) {
	examID, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.First(&exam, examID).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}
	var sessions []models.ExamSession
	if err := a.DB.Where("exam_id = ?", examID).
		Preload("Student").Preload("Set").
		Order("total_score desc").Find(&sessions).Error; err != nil {
		failServer(c, err)
		return
	}
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", `attachment; filename="exam_`+strconv.Itoa(int(examID))+`_results.csv"`)
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"Name", "Email", "Semester", "Phone", "Set", "Status", "SubmitKind", "Flagged",
		"Violations", "AutoScore", "ManualScore", "TotalScore", "StartedAt", "SubmittedAt"})
	for _, s := range sessions {
		label := ""
		if s.Set != nil {
			label = s.Set.Label
		}
		submitted := ""
		if s.SubmittedAt != nil {
			submitted = s.SubmittedAt.Format(time.RFC3339)
		}
		_ = w.Write([]string{
			s.Student.Name, s.Student.Email, strconv.Itoa(s.Student.Semester), s.Student.Phone,
			label, s.Status, s.SubmitKind, strconv.FormatBool(s.Flagged),
			strconv.Itoa(s.ViolationCount),
			strconv.FormatFloat(s.AutoScore, 'f', 2, 64),
			strconv.FormatFloat(s.ManualScore, 'f', 2, 64),
			strconv.FormatFloat(s.TotalScore, 'f', 2, 64),
			s.StartedAt.Format(time.RFC3339), submitted,
		})
	}
	w.Flush()
}
