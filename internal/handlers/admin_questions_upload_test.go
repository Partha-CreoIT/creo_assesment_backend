package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"exam_taker_bc/internal/config"
	"exam_taker_bc/internal/models"
)

func uploadTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(models.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestUploadQuestionsEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := uploadTestDB(t)

	exam := &models.Exam{Title: "T", DurationMin: 60, MaxViolations: 3, Status: models.ExamDraft}
	if err := db.Create(exam).Error; err != nil {
		t.Fatal(err)
	}
	for _, l := range setLabels {
		if err := db.Create(&models.QuestionSet{ExamID: exam.ID, Label: l}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var setB models.QuestionSet
	if err := db.Where("exam_id = ? AND label = ?", exam.ID, "B").First(&setB).Error; err != nil {
		t.Fatal(err)
	}

	docx, err := os.ReadFile("assets/question-upload-template.docx")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("set", "B")
	fw, _ := mw.CreateFormFile("file", "questions.docx")
	fw.Write(docx)
	mw.Close()

	api := &API{DB: db}
	r := gin.New()
	r.POST("/api/v1/admin/exams/:id/questions/upload", api.UploadQuestions)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/exams/1/questions/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var qCount int64
	db.Model(&models.Question{}).Count(&qCount)
	if qCount != 6 {
		t.Errorf("expected 6 questions in bank, got %d", qCount)
	}

	var sqs []models.SetQuestion
	db.Where("set_id = ?", setB.ID).Order("position").Find(&sqs)
	if len(sqs) != 6 {
		t.Fatalf("expected 6 set questions, got %d", len(sqs))
	}
	for i, sq := range sqs {
		if sq.Position != i {
			t.Errorf("position %d expected %d", sq.Position, i)
		}
		if sq.Marks <= 0 {
			t.Errorf("marks not copied for set question %d", sq.ID)
		}
	}

	// coding questions should have their test cases persisted
	var tcCount int64
	db.Model(&models.TestCase{}).Count(&tcCount)
	if tcCount != 4 {
		t.Errorf("expected 4 test cases (2 coding q x 2), got %d", tcCount)
	}
}

// BuildRouter must not panic — guards the /questions/template (static) vs
// /questions/:id (param) route registration — and the template download works.
func TestBuildRouterAndTemplateDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{DB: uploadTestDB(t), Cfg: &config.Config{JWTSecret: "x", CORSOrigins: []string{"*"}}}
	r := api.BuildRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/questions/template", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// no admin token -> 401 from auth middleware, but the route must exist (not 404)
	if w.Code == http.StatusNotFound {
		t.Fatalf("template route not registered (404)")
	}

	// download the embedded template directly through the handler
	api.DownloadQuestionTemplate(newTestCtx(w))
	if len(templateDocx) == 0 {
		t.Fatal("embedded template is empty")
	}
}

func newTestCtx(w *httptest.ResponseRecorder) *gin.Context {
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c
}

func TestUploadQuestionsBadSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := uploadTestDB(t)
	exam := &models.Exam{Title: "T", DurationMin: 60, MaxViolations: 3, Status: models.ExamDraft}
	db.Create(exam)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("set", "Z")
	fw, _ := mw.CreateFormFile("file", "questions.docx")
	fw.Write([]byte("dummy"))
	mw.Close()

	api := &API{DB: db}
	r := gin.New()
	r.POST("/api/v1/admin/exams/:id/questions/upload", api.UploadQuestions)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/exams/1/questions/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad set label, got %d: %s", w.Code, w.Body.String())
	}
}

// The committed template must always parse into exactly its 6 example questions
// with no block errors — this guards the docx extractor and block parser.
func TestParseTemplateDocx(t *testing.T) {
	data, err := os.ReadFile("assets/question-upload-template.docx")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	text, err := docxToText(data)
	if err != nil {
		t.Fatalf("docxToText: %v", err)
	}
	ps, errs := parseQuestionBlocks(text)
	for _, e := range errs {
		t.Errorf("unexpected block error q%d: %s", e.Question, e.Error)
	}
	if len(ps) != 6 {
		t.Fatalf("expected 6 questions, got %d", len(ps))
	}

	// Q1: aptitude, answer C -> correctIndex 2
	if ps[0].Type != "aptitude" || ps[0].CorrectIndex == nil || *ps[0].CorrectIndex != 2 {
		t.Errorf("Q1 wrong: type=%s idx=%v", ps[0].Type, ps[0].CorrectIndex)
	}
	// Q2: 'maths' alias -> aptitude, hint empty
	if ps[1].Type != "aptitude" || ps[1].Hint != "" {
		t.Errorf("Q2 wrong: type=%s hint=%q", ps[1].Type, ps[1].Hint)
	}
	// Q3: english, multi-line body kept
	if ps[2].Type != "english" || len(ps[2].Body) < 100 {
		t.Errorf("Q3 wrong: type=%s bodyLen=%d", ps[2].Type, len(ps[2].Body))
	}
	// Q5: coding, 2 test cases, python starter, 3s limit
	if ps[4].Type != "coding" || len(ps[4].TestCases) != 2 || ps[4].StarterCode["python"] == "" || ps[4].TimeLimitMS != 3000 {
		t.Errorf("Q5 wrong: %+v", ps[4])
	}
	// Q6: 'code' alias -> coding, empty hint
	if ps[5].Type != "coding" || ps[5].Hint != "" {
		t.Errorf("Q6 wrong: type=%s hint=%q", ps[5].Type, ps[5].Hint)
	}
}

func TestParseBlocksErrorsAreAllReported(t *testing.T) {
	text := `QUESTION 1
Type: aptitude
Marks: two
Question: bad marks
Option A: x
Option B: y
Answer: A

QUESTION 2
Type: wizardry
Marks: 1
Question: bad type

QUESTION 3
Type: aptitude
Marks: 1
Question: mcq missing correct answer
Option A: x
Option B: y
Answer: Z`

	ps, errs := parseQuestionBlocks(text)
	if len(ps) != 0 {
		t.Fatalf("expected 0 valid payloads, got %d", len(ps))
	}
	if len(errs) != 3 {
		t.Fatalf("expected 3 block errors, got %d: %+v", len(errs), errs)
	}
}

func TestParseBlockEmptyHintAndAliases(t *testing.T) {
	text := `QUESTION 1
Type: apti
Marks: 1
Question: alias apti maps to aptitude
Hint:
Option A: x
Option B: y
Answer: B`

	ps, errs := parseQuestionBlocks(text)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if len(ps) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(ps))
	}
	if ps[0].Type != "aptitude" {
		t.Errorf("apti should map to aptitude, got %s", ps[0].Type)
	}
	if ps[0].Hint != "" {
		t.Errorf("hint should be empty, got %q", ps[0].Hint)
	}
	if ps[0].CorrectIndex == nil || *ps[0].CorrectIndex != 1 {
		t.Errorf("answer B should be index 1, got %v", ps[0].CorrectIndex)
	}
}
