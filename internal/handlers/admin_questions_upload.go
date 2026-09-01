package handlers

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
)

const maxUploadBytes = 10 << 20 // 10 MiB

const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

//go:embed assets/question-upload-template.docx
var templateDocx []byte

// DownloadQuestionTemplate serves the blank .docx the admin fills in and uploads.
func (a *API) DownloadQuestionTemplate(c *gin.Context) {
	c.Header("Content-Disposition", `attachment; filename="question-upload-template.docx"`)
	c.Data(http.StatusOK, docxMIME, templateDocx)
}

// ---------- DOCX text extraction ----------

// docxToText pulls the plain text out of a .docx, treating paragraph ends and
// <w:br/> as newlines so the block parser sees one logical line per line.
func docxToText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a valid .docx file")
	}
	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return "", fmt.Errorf("word/document.xml missing — is this a real .docx?")
	}
	rc, err := doc.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	var sb strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "br", "cr":
				sb.WriteByte('\n')
			case "tab":
				sb.WriteByte('\t')
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String(), nil
}

// ---------- block parsing ----------

type blockError struct {
	Question int    `json:"question"`
	Error    string `json:"error"`
}

var typeAliases = map[string]string{
	"aptitude": "aptitude", "apti": "aptitude", "mcq": "aptitude",
	"maths": "aptitude", "math": "aptitude", "quant": "aptitude",
	"english": "english", "eng": "english", "verbal": "english", "comprehension": "english",
	"coding": "coding", "code": "coding", "program": "coding", "programming": "coding", "dsa": "coding",
}

var starterLangAliases = map[string]string{
	"python": "python", "py": "python",
	"java": "java",
	"c":    "c",
}

// parseQuestionBlocks splits the document text into per-question blocks (each
// starting with a "QUESTION <n>" line) and validates every block. It returns
// the valid payloads and a list of block errors; callers should import nothing
// when any error is present.
func parseQuestionBlocks(text string) ([]questionPayload, []blockError) {
	lines := strings.Split(text, "\n")

	type rawBlock struct{ lines []string }
	var blocks []rawBlock
	cur := -1
	for _, ln := range lines {
		if isBlockMarker(ln) {
			blocks = append(blocks, rawBlock{})
			cur = len(blocks) - 1
			continue
		}
		if cur < 0 {
			continue // preamble / instructions before the first QUESTION marker
		}
		blocks[cur].lines = append(blocks[cur].lines, ln)
	}

	var out []questionPayload
	var errs []blockError
	for i, b := range blocks {
		seq := i + 1
		p, err := parseOneBlock(b.lines)
		if err != nil {
			errs = append(errs, blockError{Question: seq, Error: err.Error()})
			continue
		}
		if err := p.validate(); err != nil {
			errs = append(errs, blockError{Question: seq, Error: err.Error()})
			continue
		}
		out = append(out, *p)
	}
	return out, errs
}

// isBlockMarker reports whether a line is a "QUESTION <n>" block separator
// (as opposed to the "Question:" field, which carries a colon).
func isBlockMarker(line string) bool {
	s := strings.TrimSpace(line)
	if len(s) < len("question") || !strings.EqualFold(s[:8], "question") {
		return false
	}
	rest := strings.TrimSpace(s[8:])
	if rest == "" || strings.HasPrefix(rest, ":") {
		return false
	}
	_, err := strconv.Atoi(strings.Fields(rest)[0])
	return err == nil
}

func parseOneBlock(lines []string) (*questionPayload, error) {
	p := &questionPayload{}
	optLetters := []string{}
	optText := map[string]string{}
	answerLetter := ""
	starter := map[string]string{}
	var testcases []testCasePayload

	const (
		fNone = iota
		fQuestion
		fStarter
	)
	multi := fNone
	multiLang := ""
	typeSet := false

	for _, raw := range lines {
		key, val, ok := splitField(raw)
		if !ok {
			switch multi { // continuation of a multi-line field
			case fQuestion:
				p.Body += "\n" + raw
			case fStarter:
				starter[multiLang] += "\n" + raw
			}
			continue
		}

		switch {
		case key == "type":
			canon, mapped := typeAliases[strings.ToLower(strings.TrimSpace(val))]
			if !mapped {
				return nil, fmt.Errorf("unknown type %q (use aptitude/english/coding)", strings.TrimSpace(val))
			}
			p.Type = canon
			typeSet = true
			multi = fNone
		case key == "title":
			p.Title = strings.TrimSpace(val)
			multi = fNone
		case key == "marks":
			m, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, fmt.Errorf("marks must be a number, got %q", strings.TrimSpace(val))
			}
			p.Marks = m
			multi = fNone
		case key == "difficulty":
			p.Difficulty = strings.TrimSpace(val)
			multi = fNone
		case key == "question":
			p.Body = strings.TrimSpace(val)
			multi = fQuestion
		case key == "hint":
			p.Hint = strings.TrimSpace(val)
			multi = fNone
		case key == "answer":
			answerLetter = normalizeLetter(val)
			multi = fNone
		case key == "timelimitms":
			ms, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, fmt.Errorf("timeLimitMs must be a number, got %q", strings.TrimSpace(val))
			}
			p.TimeLimitMS = ms
			multi = fNone
		case key == "testcase":
			tc, err := parseTestCase(val)
			if err != nil {
				return nil, err
			}
			testcases = append(testcases, tc)
			multi = fNone
		case strings.HasPrefix(key, "option "):
			letter := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(key, "option ")))
			if letter == "" {
				return nil, fmt.Errorf("option needs a letter, e.g. 'Option A:'")
			}
			if _, dup := optText[letter]; !dup {
				optLetters = append(optLetters, letter)
			}
			optText[letter] = strings.TrimSpace(val)
			multi = fNone
		case strings.HasPrefix(key, "starter "):
			lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(key, "starter ")))
			canon, ok := starterLangAliases[lang]
			if !ok {
				return nil, fmt.Errorf("unsupported starter language %q (use python, java, or c)", lang)
			}
			starter[canon] = strings.TrimSpace(val)
			multi = fStarter
			multiLang = canon
		}
	}

	if !typeSet {
		return nil, fmt.Errorf("missing 'Type:' line")
	}
	if strings.TrimSpace(p.Title) == "" {
		p.Title = deriveTitle(p.Body)
	}
	if len(optLetters) > 0 {
		opts := make([]string, 0, len(optLetters))
		for _, l := range optLetters {
			opts = append(opts, optText[l])
		}
		p.Options = opts
		if answerLetter != "" {
			idx := indexOf(optLetters, answerLetter)
			if idx < 0 {
				return nil, fmt.Errorf("answer %q does not match any option letter", answerLetter)
			}
			p.CorrectIndex = &idx
		}
	}
	if len(starter) > 0 {
		p.StarterCode = starter
	}
	if len(testcases) > 0 {
		p.TestCases = testcases
	}
	p.Body = strings.TrimSpace(p.Body)
	return p, nil
}

// splitField parses a "Key: value" line, but only for recognized keys — this
// lets question/starter bodies contain their own colons without being mistaken
// for fields.
func splitField(line string) (key, val string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(line[:idx]))
	if !isKnownKey(key) {
		return "", "", false
	}
	val = line[idx+1:]
	if strings.HasPrefix(val, " ") {
		val = val[1:]
	}
	return key, val, true
}

func isKnownKey(key string) bool {
	switch key {
	case "type", "title", "marks", "difficulty", "question", "hint", "answer", "timelimitms", "testcase":
		return true
	}
	return strings.HasPrefix(key, "option ") || strings.HasPrefix(key, "starter ")
}

func parseTestCase(val string) (testCasePayload, error) {
	tc := testCasePayload{Weight: 1}
	seen := false
	for _, part := range strings.Split(val, "||") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v := strings.TrimSpace(kv[1])
		switch k {
		case "input":
			tc.Input = v
			seen = true
		case "expected", "output":
			tc.Expected = v
			seen = true
		case "hidden":
			tc.Hidden = parseBool(v)
		case "weight":
			if w, err := strconv.Atoi(v); err == nil && w > 0 {
				tc.Weight = w
			}
		}
	}
	if !seen {
		return tc, fmt.Errorf("bad TestCase %q (expect input=.. || expected=.. || hidden=.. || weight=..)", strings.TrimSpace(val))
	}
	return tc, nil
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y":
		return true
	}
	return false
}

func normalizeLetter(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
			continue
		}
		break
	}
	return strings.ToUpper(b.String())
}

func deriveTitle(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return truncateRunes(ln, 200)
		}
	}
	return "Untitled question"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

// ---------- handler ----------

type uploadResult struct {
	Created int               `json:"created"`
	Set     string            `json:"set"`
	Items   []models.Question `json:"items"`
}

// UploadQuestions imports questions from an uploaded .docx into the question
// bank and assigns them (in file order) to the given set of the exam. It is
// all-or-nothing: if any block fails to parse or validate, nothing is created.
func (a *API) UploadQuestions(c *gin.Context) {
	examID, ok := paramUint(c, "id")
	if !ok {
		return
	}
	var exam models.Exam
	if err := a.DB.First(&exam, examID).Error; err != nil {
		fail(c, http.StatusNotFound, "exam not found")
		return
	}

	label := strings.ToUpper(strings.TrimSpace(c.PostForm("set")))
	if !isSetLabel(label) {
		fail(c, http.StatusBadRequest, "set must be one of A, B, C, D, E, F")
		return
	}
	var set models.QuestionSet
	if err := a.DB.Where("exam_id = ? AND label = ?", examID, label).First(&set).Error; err != nil {
		fail(c, http.StatusNotFound, "set "+label+" not found for this exam")
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "file is required (multipart field 'file')")
		return
	}
	if fh.Size > maxUploadBytes {
		fail(c, http.StatusBadRequest, "file too large (max 10 MB)")
		return
	}
	if !strings.HasSuffix(strings.ToLower(fh.Filename), ".docx") {
		fail(c, http.StatusBadRequest, "only .docx files are supported")
		return
	}
	f, err := fh.Open()
	if err != nil {
		failServer(c, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes))
	if err != nil {
		failServer(c, err)
		return
	}

	text, err := docxToText(data)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	payloads, perrs := parseQuestionBlocks(text)
	if len(perrs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "some questions are invalid — nothing was imported",
			"details": perrs,
		})
		return
	}
	if len(payloads) == 0 {
		fail(c, http.StatusBadRequest, "no questions found — start each block with a 'QUESTION <n>' line")
		return
	}

	var maxPos int
	a.DB.Model(&models.SetQuestion{}).
		Where("set_id = ?", set.ID).
		Select("COALESCE(MAX(position), -1)").Scan(&maxPos)

	created := make([]models.Question, 0, len(payloads))
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		for i := range payloads {
			p := payloads[i]
			var q models.Question
			p.apply(&q)
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
			sq := models.SetQuestion{
				SetID:      set.ID,
				QuestionID: q.ID,
				Position:   maxPos + 1 + i,
				Marks:      q.Marks,
			}
			if err := tx.Create(&sq).Error; err != nil {
				return err
			}
			created = append(created, q)
		}
		return nil
	})
	if err != nil {
		failServer(c, err)
		return
	}

	c.JSON(http.StatusCreated, uploadResult{
		Created: len(created),
		Set:     label,
		Items:   created,
	})
}

func isSetLabel(l string) bool {
	for _, s := range setLabels {
		if s == l {
			return true
		}
	}
	return false
}
