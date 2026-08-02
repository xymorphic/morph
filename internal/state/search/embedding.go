package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/xymorphic/morph/pkg/str"
)

// Embedder creates vector embeddings for model inputs.
type Embedder interface {
	Embed(context.Context, EmbeddingRequest) (EmbeddingResult, error)
}

// EmbeddingRequest describes an embedding request.
type EmbeddingRequest struct {
	Model        string
	Relationship string
	Target       string
	Inputs       []EmbeddingInput
}

// EmbeddingInput describes input for embedding.
type EmbeddingInput struct {
	ID         string
	Text       string
	SourceKind SourceKind
}

// EmbeddingResult contains embedding vectors returned for a request.
type EmbeddingResult struct {
	Model      string
	Items      []Embedding
	Dimensions int
}

// Embedding contains one vector for one embedding input.
type Embedding struct {
	ID          string
	ContentHash string
	Vector      []float64
}

// ValidateEmbeddingRequest checks that an embedding request has model input.
func ValidateEmbeddingRequest(req EmbeddingRequest) error {
	modelValue := str.String(req.Model)
	if modelValue.Trim() == "" {
		return errors.New("embedding model is required")
	}

	if len(req.Inputs) == 0 {
		return errors.New("embedding inputs are required")
	}

	seenIDs := make(map[string]struct{}, len(req.Inputs))
	for _, input := range req.Inputs {
		iDValue := str.String(input.ID)
		inputID := iDValue.Trim()
		if inputID == "" {
			return errors.New("embedding input id is required")
		}
		if inputID != input.ID {
			return errors.New("embedding input id must be trimmed")
		}
		if _, ok := seenIDs[inputID]; ok {
			return fmt.Errorf("embedding input id %q is duplicated", inputID)
		}
		seenIDs[inputID] = struct{}{}
		if err := validateOptionalSourceKind(input.SourceKind, "embedding input source kind"); err != nil {
			return err
		}
		textValue := str.String(input.Text)
		if textValue.Trim() == "" {
			return errors.New("embedding input text is required")
		}
	}

	return nil
}

// ValidateEmbeddingResult checks that embedding output matches the requested inputs.
func ValidateEmbeddingResult(req EmbeddingRequest, result EmbeddingResult) error {
	if err := ValidateEmbeddingRequest(req); err != nil {
		return err
	}

	modelValue2 := str.String(result.Model)
	if modelValue2.Trim() == "" {
		return errors.New("embedding result model is required")
	}
	modelValue3 := str.String(result.Model)
	modelValue4 := str.String(req.Model)
	if modelValue3.Trim() != modelValue4.Trim() {
		return errors.New("embedding result model must match request model")
	}
	if result.Dimensions <= 0 {
		return errors.New("embedding result dimensions must be greater than zero")
	}
	if len(result.Items) != len(req.Inputs) {
		return errors.New("embedding result count must match input count")
	}
	inputHashes := make(map[string]string, len(req.Inputs))
	for _, input := range req.Inputs {
		inputHashes[input.ID] = VectorContentHash(input.Text)
	}
	seenIDs := make(map[string]struct{}, len(result.Items))
	for _, item := range result.Items {
		iDValue2 := str.String(item.ID)
		itemID := iDValue2.Trim()
		if itemID == "" {
			return errors.New("embedding id is required")
		}
		if itemID != item.ID {
			return errors.New("embedding id must be trimmed")
		}
		expectedHash, ok := inputHashes[itemID]
		if !ok {
			return fmt.Errorf("embedding id %q is unknown", itemID)
		}
		if _, ok := seenIDs[itemID]; ok {
			return fmt.Errorf("embedding id %q is duplicated", itemID)
		}
		seenIDs[itemID] = struct{}{}
		if len(item.Vector) != result.Dimensions {
			return errors.New("embedding vector dimensions do not match result dimensions")
		}
		for _, value := range item.Vector {
			if !finite(value) {
				return errors.New("embedding vector value must be finite")
			}
		}
		contentHashValue := str.String(item.ContentHash)
		if contentHashValue.Trim() == "" {
			return errors.New("embedding content hash is required")
		}
		if item.ContentHash != expectedHash {
			return errors.New("embedding content hash must match input text")
		}
	}

	return nil
}
