package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/xymorphic/morph/pkg/str"
)

type fakeEmbeddingProvider struct {
	dimensions int
}

func (p fakeEmbeddingProvider) Embed(_ context.Context, req EmbeddingRequest) (EmbeddingResult, error) {
	if err := ValidateEmbeddingRequest(req); err != nil {
		return EmbeddingResult{}, err
	}
	if p.dimensions <= 0 {
		return EmbeddingResult{}, errors.New("embedding dimensions must be greater than zero")
	}

	modelValue := str.String(req.Model)
	model := modelValue.Trim()
	items := make([]Embedding, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		items = append(items, Embedding{
			ID:          input.ID,
			Vector:      deterministicVector(input.Text, p.dimensions),
			ContentHash: VectorContentHash(input.Text),
		})
	}

	return EmbeddingResult{Model: model, Dimensions: p.dimensions, Items: items}, nil
}

func deterministicVector(text string, dimensions int) []float64 {
	hash := VectorContentHash(text)
	vector := make([]float64, dimensions)
	for idx := range vector {
		start := (idx * 2) % len(hash)
		value := hash[start : start+2]
		var parsed uint64
		_, _ = fmt.Sscanf(value, "%x", &parsed)
		vector[idx] = float64(parsed) / 255
	}

	return vector
}

func cloneEmbeddingResult(result EmbeddingResult) EmbeddingResult {
	result.Items = append([]Embedding(nil), result.Items...)
	for idx := range result.Items {
		result.Items[idx].Vector = append([]float64(nil), result.Items[idx].Vector...)
	}

	return result
}
