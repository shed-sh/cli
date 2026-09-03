package source

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"shed/internal/definition"
)

const SourceManifestFileName = ".shed-source.json"

type Entry struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	Type   string `json:"type"`
}

type Manifest struct {
	Digest    string  `json:"digest"`
	FileCount int     `json:"fileCount"`
	TotalSize int64   `json:"totalSize"`
	Files     []Entry `json:"files"`
}

type Archive struct {
	Path           string   `json:"-"`
	Digest         string   `json:"archiveDigest"`
	CompressedSize int64    `json:"size"`
	Content        Manifest `json:"content"`
	Temporary      bool     `json:"-"`
}

func Prepare(root, outputPath string, definitionSource []byte, selections ...string) (Archive, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Archive{}, fmt.Errorf("resolve application root: %w", err)
	}
	files, err := CollectFiles(absoluteRoot)
	if err != nil {
		return Archive{}, err
	}
	if outputPath != "" {
		files, err = excludeOutput(absoluteRoot, outputPath, files)
		if err != nil {
			return Archive{}, err
		}
	}
	if len(selections) > 0 {
		files, err = selectFiles(files, selections)
		if err != nil {
			return Archive{}, err
		}
	}
	entries, err := inspectEntries(absoluteRoot, files, definitionSource)
	if err != nil {
		return Archive{}, err
	}
	content := Manifest{Files: entries}
	for _, entry := range entries {
		content.TotalSize += entry.Size
	}
	content.FileCount = len(entries)
	content.Digest = digestEntries(entries)
	sourceManifest, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return Archive{}, fmt.Errorf("encode source manifest: %w", err)
	}
	sourceManifest = append(sourceManifest, '\n')

	temporary := outputPath == ""
	if temporary {
		file, createErr := os.CreateTemp("", "shed-source-*.tar.gz")
		if createErr != nil {
			return Archive{}, fmt.Errorf("create temporary archive: %w", createErr)
		}
		outputPath = file.Name()
		if closeErr := file.Close(); closeErr != nil {
			return Archive{}, fmt.Errorf("close temporary archive: %w", closeErr)
		}
	}
	digest, size, err := writeArchive(absoluteRoot, outputPath, entries, definitionSource, sourceManifest)
	if err != nil {
		if temporary {
			_ = os.Remove(outputPath)
		}
		return Archive{}, err
	}
	return Archive{
		Path:           outputPath,
		Digest:         digest,
		CompressedSize: size,
		Content:        content,
		Temporary:      temporary,
	}, nil
}

func selectFiles(files, selections []string) ([]string, error) {
	result := make([]string, 0, len(files))
	matched := make(map[string]bool, len(selections))
	for _, filename := range files {
		for _, selected := range selections {
			if filename == selected || strings.HasPrefix(filename, strings.TrimSuffix(selected, "/")+"/") {
				result = append(result, filename)
				matched[selected] = true
				break
			}
		}
	}
	for _, selected := range selections {
		if !matched[selected] {
			return nil, fmt.Errorf("declared content path %q is missing or structurally excluded", selected)
		}
	}
	return result, nil
}

func (a Archive) Close() error {
	if !a.Temporary || a.Path == "" {
		return nil
	}
	return os.Remove(a.Path)
}

func inspectEntries(root string, files []string, definitionSource []byte) ([]Entry, error) {
	entries := make([]Entry, 0, len(files)+1)
	entries = append(entries, bytesEntry(definition.ManifestFileName, definitionSource, 0o644))
	for _, relative := range files {
		filename := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, fmt.Errorf("inspect %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlinks are not supported by the current Werf bundle contract: %q", relative)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("special files are not supported: %q", relative)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", relative, err)
		}
		after, err := os.Stat(filename)
		if err != nil {
			return nil, fmt.Errorf("reinspect %q: %w", relative, err)
		}
		if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return nil, fmt.Errorf("%q changed while reading", relative)
		}
		mode := int64(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		entries = append(entries, bytesEntry(relative, data, mode))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func bytesEntry(path string, data []byte, mode int64) Entry {
	digest := sha256.Sum256(data)
	return Entry{
		Path:   filepath.ToSlash(path),
		Digest: fmt.Sprintf("sha256:%x", digest),
		Mode:   fmt.Sprintf("%04o", mode),
		Size:   int64(len(data)),
		Type:   "file",
	}
}

func digestEntries(entries []Entry) string {
	hash := sha256.New()
	for _, entry := range entries {
		writeField(hash, entry.Path)
		writeField(hash, entry.Type)
		writeField(hash, entry.Mode)
		writeField(hash, entry.Digest)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(entry.Size))
		_, _ = hash.Write(size[:])
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func writeField(writer io.Writer, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = io.WriteString(writer, value)
}

func writeArchive(root, outputPath string, entries []Entry, definitionSource, sourceManifest []byte) (string, int64, error) {
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create source archive: %w", err)
	}
	hash := sha256.New()
	counter := &countingWriter{writer: io.MultiWriter(output, hash)}
	gzipWriter, err := gzip.NewWriterLevel(counter, gzip.BestSpeed)
	if err != nil {
		_ = output.Close()
		return "", 0, fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.ModTime = time.Unix(0, 0)
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)

	closeAll := func(writeErr error) (string, int64, error) {
		tarErr := tarWriter.Close()
		gzipErr := gzipWriter.Close()
		fileErr := output.Close()
		for _, closeErr := range []error{writeErr, tarErr, gzipErr, fileErr} {
			if closeErr != nil {
				return "", 0, closeErr
			}
		}
		return fmt.Sprintf("sha256:%x", hash.Sum(nil)), counter.count, nil
	}

	archiveEntries := append(append([]Entry(nil), entries...), bytesEntry(SourceManifestFileName, sourceManifest, 0o644))
	sort.Slice(archiveEntries, func(i, j int) bool { return archiveEntries[i].Path < archiveEntries[j].Path })
	for _, entry := range archiveEntries {
		var data []byte
		switch entry.Path {
		case definition.ManifestFileName:
			data = definitionSource
		case SourceManifestFileName:
			data = sourceManifest
		default:
			data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
			if err != nil {
				return closeAll(fmt.Errorf("read %q for archive: %w", entry.Path, err))
			}
		}
		actualDigest := sha256.Sum256(data)
		if fmt.Sprintf("sha256:%x", actualDigest) != entry.Digest || int64(len(data)) != entry.Size {
			return closeAll(fmt.Errorf("%q changed after content manifest generation", entry.Path))
		}
		mode := int64(0o644)
		if entry.Mode == "0755" {
			mode = 0o755
		}
		header := &tar.Header{
			Name:     entry.Path,
			Mode:     mode,
			Size:     entry.Size,
			ModTime:  time.Unix(0, 0),
			Typeflag: tar.TypeReg,
			Uid:      0,
			Gid:      0,
			Format:   tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return closeAll(fmt.Errorf("write archive header for %q: %w", entry.Path, err))
		}
		if _, err := tarWriter.Write(data); err != nil {
			return closeAll(fmt.Errorf("write archive file %q: %w", entry.Path, err))
		}
	}
	return closeAll(nil)
}

func excludeOutput(root, outputPath string, files []string) ([]string, error) {
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, fmt.Errorf("resolve archive output: %w", err)
	}
	relative, err := filepath.Rel(root, absOutput)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return files, nil
	}
	relative = filepath.ToSlash(relative)
	result := files[:0]
	for _, filename := range files {
		if filename != relative {
			result = append(result, filename)
		}
	}
	return result, nil
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	w.count += int64(written)
	return written, err
}
