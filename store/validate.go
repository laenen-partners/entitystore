package store

import "fmt"

const (
	// MaxBatchSize is the maximum number of operations in a single BatchWrite call.
	MaxBatchSize = 1000

	maxTagLength      = 255
	maxTagsPerEntity  = 100
	maxRelationType   = 255
)

func validateTags(tags []string) error {
	if len(tags) > maxTagsPerEntity {
		return fmt.Errorf("too many tags: %d exceeds maximum %d", len(tags), maxTagsPerEntity)
	}
	for _, t := range tags {
		if t == "" {
			return fmt.Errorf("empty tag not allowed")
		}
		if len(t) > maxTagLength {
			return fmt.Errorf("tag too long: %d chars exceeds maximum %d", len(t), maxTagLength)
		}
	}
	return nil
}

func validateRelationType(rt string) error {
	if rt == "" {
		return fmt.Errorf("relation type must not be empty")
	}
	if len(rt) > maxRelationType {
		return fmt.Errorf("relation type too long: %d chars exceeds maximum %d", len(rt), maxRelationType)
	}
	return nil
}
