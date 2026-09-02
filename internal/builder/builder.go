package builder

import (
	"context"

	"shed/internal/source"
)

type Image struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type Builder interface {
	Build(context.Context, source.Archive) (Image, error)
}
