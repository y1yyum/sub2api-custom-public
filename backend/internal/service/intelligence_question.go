package service

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Answer rules for admin-managed quiz questions.
const (
	IntelligenceRuleExact  = "exact"
	IntelligenceRuleNumber = "number"
	IntelligenceRuleRegex  = "regex"
)

const (
	intelligencePromptMaxRunes = 2000
	intelligenceAnswerMaxRunes = 200
	intelligenceAnswersMax     = 10
)

const intelligenceQuestionInvalidReason = "INTELLIGENCE_QUESTION_INVALID"

var ErrIntelligenceQuestionNotFound = infraerrors.NotFound("INTELLIGENCE_QUESTION_NOT_FOUND", "intelligence question not found")

// IntelligenceQuestion is a quiz question with fixed answers; the bank is
// empty until an admin adds questions.
type IntelligenceQuestion struct {
	ID              int64     `json:"id"`
	Prompt          string    `json:"prompt"`
	AnswerRule      string    `json:"answer_rule"`
	ExpectedAnswers []string  `json:"expected_answers"`
	Tolerance       float64   `json:"tolerance"`
	Enabled         bool      `json:"enabled"`
	PlanCount       int       `json:"plan_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// IntelligenceQuestionRepository stores the question bank and plan links.
type IntelligenceQuestionRepository interface {
	List(ctx context.Context) ([]*IntelligenceQuestion, error)
	GetByID(ctx context.Context, id int64) (*IntelligenceQuestion, error)
	Create(ctx context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error)
	Update(ctx context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error)
	Delete(ctx context.Context, id int64) error
	// ListEnabledForPlan returns the plan's enabled questions in link order.
	ListEnabledForPlan(ctx context.Context, planID int64) ([]*IntelligenceQuestion, error)
	ListPlanQuestionIDs(ctx context.Context, planIDs []int64) (map[int64][]int64, error)
	// SetPlanQuestions replaces the plan's links; it returns
	// ErrIntelligenceQuestionNotFound when any ID does not exist.
	SetPlanQuestions(ctx context.Context, planID int64, questionIDs []int64) error
	// AdvanceRotation increments the plan's rotation cursor and returns its previous value.
	AdvanceRotation(ctx context.Context, planID int64) (int, error)
}

func invalidIntelligenceQuestion(message string) error {
	return infraerrors.BadRequest(intelligenceQuestionInvalidReason, message)
}

// Normalize trims the question and validates it for storage.
func (q *IntelligenceQuestion) Normalize() error {
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Prompt == "" {
		return invalidIntelligenceQuestion("prompt is required")
	}
	if utf8.RuneCountInString(q.Prompt) > intelligencePromptMaxRunes {
		return invalidIntelligenceQuestion("prompt must be at most 2000 characters")
	}
	return q.normalizeAnswerSpec()
}

// normalizeAnswerSpec validates the rule, answers and tolerance.
func (q *IntelligenceQuestion) normalizeAnswerSpec() error {
	answers, err := normalizeIntelligenceAnswers(q.AnswerRule, q.ExpectedAnswers)
	if err != nil {
		return err
	}
	q.ExpectedAnswers = answers
	if q.AnswerRule != IntelligenceRuleNumber {
		q.Tolerance = 0
	} else if q.Tolerance < 0 || math.IsNaN(q.Tolerance) || math.IsInf(q.Tolerance, 0) {
		return invalidIntelligenceQuestion("tolerance must be a non-negative number")
	}
	return nil
}

func normalizeIntelligenceAnswers(rule string, raw []string) ([]string, error) {
	switch rule {
	case IntelligenceRuleExact, IntelligenceRuleNumber, IntelligenceRuleRegex:
	default:
		return nil, invalidIntelligenceQuestion("answer_rule must be exact, number or regex")
	}
	answers := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, answer := range raw {
		answer = strings.TrimSpace(answer)
		if answer == "" {
			continue
		}
		if _, dup := seen[answer]; dup {
			continue
		}
		if utf8.RuneCountInString(answer) > intelligenceAnswerMaxRunes {
			return nil, invalidIntelligenceQuestion("each answer must be at most 200 characters")
		}
		switch rule {
		case IntelligenceRuleNumber:
			if _, ok := parseIntelligenceNumber(answer); !ok {
				return nil, invalidIntelligenceQuestion("number answers must be numeric: " + answer)
			}
		case IntelligenceRuleRegex:
			if _, err := regexp.Compile(answer); err != nil {
				return nil, invalidIntelligenceQuestion("invalid regex: " + answer)
			}
		}
		seen[answer] = struct{}{}
		answers = append(answers, answer)
	}
	if len(answers) == 0 {
		return nil, invalidIntelligenceQuestion("at least one expected answer is required")
	}
	if len(answers) > intelligenceAnswersMax {
		return nil, invalidIntelligenceQuestion("at most 10 expected answers are allowed")
	}
	return answers, nil
}

// ExpectedAnswerText is the answer snapshot stored with each quiz result.
func (q *IntelligenceQuestion) ExpectedAnswerText() string {
	text := strings.Join(q.ExpectedAnswers, " / ")
	if q.AnswerRule == IntelligenceRuleNumber && q.Tolerance > 0 {
		text += " ±" + strconv.FormatFloat(q.Tolerance, 'f', -1, 64)
	}
	return text
}

// Grade reports whether reply answers the question; q must be normalized.
func (q *IntelligenceQuestion) Grade(reply string) bool {
	switch q.AnswerRule {
	case IntelligenceRuleExact:
		candidates := []string{normalizeIntelligenceText(reply), normalizeIntelligenceText(lastNonEmptyLine(reply))}
		for _, answer := range q.ExpectedAnswers {
			want := normalizeIntelligenceText(answer)
			for _, got := range candidates {
				if got == want {
					return true
				}
			}
		}
	case IntelligenceRuleNumber:
		got, ok := lastIntelligenceNumber(reply)
		if !ok {
			return false
		}
		for _, answer := range q.ExpectedAnswers {
			if want, ok := parseIntelligenceNumber(answer); ok && math.Abs(got-want) <= q.Tolerance+1e-9 {
				return true
			}
		}
	case IntelligenceRuleRegex:
		text := strings.TrimSpace(foldFullWidth(reply))
		for _, answer := range q.ExpectedAnswers {
			if re, err := regexp.Compile(answer); err == nil && re.MatchString(text) {
				return true
			}
		}
	}
	return false
}

// foldFullWidth maps full-width ASCII variants and the ideographic space to ASCII.
func foldFullWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '　':
			return ' '
		case r >= '！' && r <= '～':
			return r - 0xFEE0
		}
		return r
	}, s)
}

const intelligenceWrapChars = "*`\"'“”‘’「」『』"

func normalizeIntelligenceText(s string) string {
	s = strings.TrimSpace(foldFullWidth(s))
	for {
		trimmed := strings.TrimSpace(strings.TrimRight(strings.Trim(s, intelligenceWrapChars), "。.!"))
		if trimmed == s {
			break
		}
		s = trimmed
	}
	return strings.ToLower(s)
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

var intelligenceNumberPattern = regexp.MustCompile(`[-+]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?`)

func lastIntelligenceNumber(reply string) (float64, bool) {
	matches := intelligenceNumberPattern.FindAllString(foldFullWidth(reply), -1)
	if len(matches) == 0 {
		return 0, false
	}
	return parseIntelligenceNumber(matches[len(matches)-1])
}

func parseIntelligenceNumber(s string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(foldFullWidth(s)), ",", ""), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
