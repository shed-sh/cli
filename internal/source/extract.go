package source

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"shed/internal/definition"
)

// Extract verifies and safely materializes an immutable source archive. The
// returned definition is read from the archive, never from the original tree.
func Extract(archive Archive, destination string) (definition.Manifest, error) {
	file, err := os.Open(archive.Path)
	if err != nil {
		return definition.Manifest{}, fmt.Errorf("open source archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return definition.Manifest{}, fmt.Errorf("verify source archive: %w", err)
	}
	digest := fmt.Sprintf("sha256:%x", hash.Sum(nil))
	if size != archive.CompressedSize || digest != archive.Digest {
		return definition.Manifest{}, fmt.Errorf("source archive identity mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return definition.Manifest{}, err
	}
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return definition.Manifest{}, fmt.Errorf("open compressed source: %w", err)
	}
	defer func() { _ = compressed.Close() }()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return definition.Manifest{}, fmt.Errorf("create extraction root: %w", err)
	}
	reader := tar.NewReader(compressed)
	seen := make(map[string]bool)
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return definition.Manifest{}, fmt.Errorf("read source archive: %w", nextErr)
		}
		name := header.Name
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, `\`) || strings.HasPrefix(name, "../") || name == ".." {
			return definition.Manifest{}, fmt.Errorf("archive contains unsafe path %q", name)
		}
		if header.Typeflag != tar.TypeReg || seen[name] {
			return definition.Manifest{}, fmt.Errorf("archive entry %q is not a unique regular file", name)
		}
		seen[name] = true
		filename := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			return definition.Manifest{}, err
		}
		mode := os.FileMode(0o644)
		if header.Mode&0o111 != 0 {
			mode = 0o755
		}
		output, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return definition.Manifest{}, fmt.Errorf("extract %q: %w", name, err)
		}
		written, copyErr := io.Copy(output, io.LimitReader(reader, header.Size+1))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return definition.Manifest{}, fmt.Errorf("extract %q: invalid file data", name)
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(destination, definition.ManifestFileName))
	if err != nil {
		return definition.Manifest{}, fmt.Errorf("archive has no %s: %w", definition.ManifestFileName, err)
	}
	manifest, err := definition.ParseManifest(manifestData)
	if err != nil {
		return definition.Manifest{}, err
	}
	contentData, err := os.ReadFile(filepath.Join(destination, SourceManifestFileName))
	if err != nil {
		return definition.Manifest{}, fmt.Errorf("archive has no %s: %w", SourceManifestFileName, err)
	}
	var content Manifest
	if err := json.Unmarshal(contentData, &content); err != nil {
		return definition.Manifest{}, fmt.Errorf("decode source manifest: %w", err)
	}
	if content.Digest != archive.Content.Digest || content.FileCount != archive.Content.FileCount {
		return definition.Manifest{}, fmt.Errorf("embedded source manifest does not match archive receipt")
	}
	if len(seen) != len(content.Files)+1 {
		return definition.Manifest{}, fmt.Errorf("archive file set does not match source manifest")
	}
	for _, entry := range content.Files {
		data, readErr := os.ReadFile(filepath.Join(destination, filepath.FromSlash(entry.Path)))
		if readErr != nil {
			return definition.Manifest{}, fmt.Errorf("verify extracted %q: %w", entry.Path, readErr)
		}
		fileDigest := sha256.Sum256(data)
		if int64(len(data)) != entry.Size || fmt.Sprintf("sha256:%x", fileDigest) != entry.Digest {
			return definition.Manifest{}, fmt.Errorf("extracted file %q does not match source manifest", entry.Path)
		}
	}
	return manifest, nil
}
