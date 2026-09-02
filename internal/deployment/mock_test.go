package deployment

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"shed/internal/source"
)

func TestMockVerifiesUploadAndReturnsStableIDs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := source.Prepare(root, filepath.Join(t.TempDir(), "source.tar.gz"), []byte("manifest"))
	if err != nil {
		t.Fatal(err)
	}
	mock := Mock{}
	registered, err := mock.RegisterSource(context.Background(), RegisterRequest{
		ContentDigest: archive.Content.Digest,
		ArchiveDigest: archive.Digest,
		Size:          archive.CompressedSize,
		FileCount:     archive.Content.FileCount,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.UploadSource(context.Background(), registered, archive); err != nil {
		t.Fatal(err)
	}
	created, err := mock.CreateDeployment(context.Background(), registered, archive.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if registered.ID == "" || created.ID == "" || created.State != "awaiting_builder" {
		t.Fatalf("receipt = %#v %#v", registered, created)
	}
}
