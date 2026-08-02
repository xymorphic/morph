package search

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

// Candidate is a ranked search candidate before final fusion.
type Candidate struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Metadata     map[string]string
	ID           string
	SourceKind   SourceKind
	SessionID    string
	MemoryID     string
	Text         string
	LexicalScore float64
	VectorScore  float64
	FusedScore   float64
	MessageID    uint
}

// ScoreDirection declares whether higher or lower candidate scores rank first.
type ScoreDirection int

const (
	ScoreHigherIsBetter ScoreDirection = iota
	ScoreLowerIsBetter
)

// ValidateCandidate checks that a search candidate has usable identity and score fields.
func ValidateCandidate(candidate Candidate) error {
	iDValue := str.String(candidate.ID)
	if iDValue.Trim() == "" {
		return errors.New("candidate id is required")
	}
	sourceKindValue := str.String(string(candidate.SourceKind))
	if sourceKindValue.Trim() == "" {
		return errors.New("candidate source kind is required")
	}
	textValue := str.String(candidate.Text)
	if textValue.Trim() == "" {
		return errors.New("candidate text is required")
	}
	if !finite(candidate.LexicalScore) {
		return errors.New("candidate lexical score must be finite")
	}
	if !finite(candidate.VectorScore) {
		return errors.New("candidate vector score must be finite")
	}
	if !finite(candidate.FusedScore) {
		return errors.New("candidate fused score must be finite")
	}

	switch candidate.SourceKind {
	case SourceKindSessionMessage:
		sessionIDValue := str.String(candidate.SessionID)
		if sessionIDValue.Trim() == "" {
			return errors.New("candidate session id is required")
		}
		if candidate.MessageID == 0 {
			return errors.New("candidate message id is required")
		}
	case SourceKindMemoryItem:
		memoryIDValue := str.String(candidate.MemoryID)
		if memoryIDValue.Trim() == "" {
			return errors.New("candidate memory id is required")
		}
	default:
		return fmt.Errorf("candidate source kind %q is not supported", candidate.SourceKind)
	}

	return nil
}

// SortCandidates sorts sort candidates.
func SortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i int, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.FusedScore != right.FusedScore {
			return left.FusedScore > right.FusedScore
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		if left.SourceKind != right.SourceKind {
			return left.SourceKind < right.SourceKind
		}

		return left.ID < right.ID
	})
}

// NormalizeScores normalizes scores.
func NormalizeScores(scores []float64, direction ScoreDirection) ([]float64, error) {
	if len(scores) == 0 {
		return nil, nil
	}
	if direction != ScoreHigherIsBetter && direction != ScoreLowerIsBetter {
		return nil, errors.New("score direction is not supported")
	}

	minScore := scores[0]
	maxScore := scores[0]
	for _, score := range scores {
		if !finite(score) {
			return nil, errors.New("score must be finite")
		}

		if score < minScore {
			minScore = score
		}
		if score > maxScore {
			maxScore = score
		}
	}

	normalized := make([]float64, len(scores))
	if minScore == maxScore {
		for idx := range normalized {
			normalized[idx] = 1
		}

		return normalized, nil
	}

	spread := maxScore - minScore
	for idx, score := range scores {
		switch direction {
		case ScoreHigherIsBetter:
			normalized[idx] = (score - minScore) / spread
		case ScoreLowerIsBetter:
			normalized[idx] = (maxScore - score) / spread
		}
	}

	return normalized, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
