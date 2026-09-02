package deployment

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"shed/internal/source"
)

type RegisterRequest struct {
	RequestID     string
	ContentDigest string
	ArchiveDigest string
	Size          int64
	FileCount     int
}

type Source struct {
	ID string `json:"id"`
}

type Deployment struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type Deployer interface {
	RegisterSource(context.Context, RegisterRequest) (Source, error)
	UploadSource(context.Context, Source, source.Archive) error
	CreateDeployment(context.Context, Source, string) (Deployment, error)
}

type Mock struct{}

func (Mock) RegisterSource(_ context.Context, request RegisterRequest) (Source, error) {
	if request.ContentDigest == "" || request.ArchiveDigest == "" || request.Size <= 0 || request.FileCount <= 0 {
		return Source{}, fmt.Errorf("source registration metadata is incomplete")
	}
	return Source{ID: "src_" + digestPrefix(request.ContentDigest)}, nil
}

func (Mock) UploadSource(ctx context.Context, _ Source, archive source.Archive) error {
	file, err := os.Open(archive.Path)
	if err != nil {
		return fmt.Errorf("open mock upload: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file})
	if err != nil {
		return fmt.Errorf("read mock upload: %w", err)
	}
	if written != archive.CompressedSize {
		return fmt.Errorf("mock upload size mismatch: got %d, want %d", written, archive.CompressedSize)
	}
	digest := fmt.Sprintf("sha256:%x", hash.Sum(nil))
	if digest != archive.Digest {
		return fmt.Errorf("mock upload digest mismatch: got %s, want %s", digest, archive.Digest)
	}
	return nil
}

func (Mock) CreateDeployment(_ context.Context, sourceValue Source, archiveDigest string) (Deployment, error) {
	if sourceValue.ID == "" || archiveDigest == "" {
		return Deployment{}, fmt.Errorf("source and archive digest are required")
	}
	return Deployment{ID: "dep_" + digestPrefix(archiveDigest), State: "awaiting_builder"}, nil
}

func digestPrefix(value string) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}
