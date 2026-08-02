package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

const (
	MaxArchiveSize       = int64(50 * 1024 * 1024)
	maxArchiveEntries    = 256
	maxUncompressedTotal = int64(50 * 1024 * 1024)
)

var (
	ErrInvalidArchive        = errors.New("invalid linka archive")
	ErrArchiveTooLarge       = errors.New("linka archive is too large")
	ErrMissingMediaReference = errors.New("archive media reference is missing")
)

type archiveStorage interface {
	GetObject(context.Context, string) (io.ReadCloser, error)
}

type archiveStream struct {
	file *os.File
	path string
	size int64
}

func (a *archiveStream) Read(data []byte) (int, error) {
	return a.file.Read(data)
}

func (a *archiveStream) Close() error {
	closeErr := a.file.Close()
	removeErr := os.Remove(a.path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}

type archiveLimitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *archiveLimitWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if w.remaining <= 0 {
		return 0, ErrArchiveTooLarge
	}
	allowed := int64(len(data))
	if allowed > w.remaining {
		allowed = w.remaining
	}
	written, err := w.writer.Write(data[:allowed])
	w.remaining -= int64(written)
	if err != nil {
		return written, err
	}
	if written != int(allowed) {
		return written, io.ErrShortWrite
	}
	if allowed < int64(len(data)) {
		return written, ErrArchiveTooLarge
	}
	return written, nil
}

func buildArchive(
	ctx context.Context,
	config json.RawMessage,
	files []*media.File,
	storageClient archiveStorage,
	pictureLoaders ...PictureLoader,
) (*archiveStream, error) {
	var pictureLoader PictureLoader
	if len(pictureLoaders) > 0 {
		pictureLoader = pictureLoaders[0]
	}
	return buildArchiveWithLimit(
		ctx, config, files, storageClient, pictureLoader, MaxArchiveSize,
	)
}

func buildArchiveWithLimit(
	ctx context.Context,
	config json.RawMessage,
	files []*media.File,
	storageClient archiveStorage,
	pictureLoader PictureLoader,
	maxSize int64,
) (*archiveStream, error) {
	if maxSize <= 0 {
		return nil, ErrArchiveTooLarge
	}
	archiveConfig, archiveFiles, err := prepareArchiveConfig(config, files)
	if err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp("", "linka-export-*.linka")
	if err != nil {
		return nil, fmt.Errorf("create temporary archive: %w", err)
	}
	archive, err := writeTemporaryArchive(
		ctx, temporary, archiveConfig, archiveFiles, storageClient, pictureLoader, maxSize,
	)
	if err != nil {
		cleanupTemporaryArchive(temporary)
		return nil, err
	}
	return archive, nil
}

func writeTemporaryArchive(
	ctx context.Context,
	temporary *os.File,
	config *linka.Config,
	files []*media.File,
	storageClient archiveStorage,
	pictureLoader PictureLoader,
	maxSize int64,
) (*archiveStream, error) {
	limited := &archiveLimitWriter{writer: temporary, remaining: maxSize}
	writer := zip.NewWriter(limited)
	for _, file := range files {
		if err := writeArchiveMedia(ctx, writer, file, storageClient); err != nil {
			return nil, err
		}
	}
	if err := writeArchivePictures(ctx, writer, config, pictureLoader); err != nil {
		return nil, err
	}
	exportedConfig, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode exported config: %w", err)
	}
	if err = writeZipEntry(writer, "config.json", bytes.NewReader(exportedConfig)); err != nil {
		return nil, fmt.Errorf("write archive config: %w", err)
	}
	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("close archive: %w", err)
	}
	return openTemporaryArchive(temporary, maxSize)
}

func openTemporaryArchive(temporary *os.File, maxSize int64) (*archiveStream, error) {
	info, err := temporary.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}
	if info.Size() > maxSize {
		return nil, ErrArchiveTooLarge
	}
	if _, err = temporary.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind archive: %w", err)
	}
	return &archiveStream{file: temporary, path: temporary.Name(), size: info.Size()}, nil
}

func cleanupTemporaryArchive(temporary *os.File) {
	path := temporary.Name()
	if closeErr := temporary.Close(); closeErr != nil {
		slog.Warn("close incomplete archive", "path", path, "err", closeErr)
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		slog.Warn("remove incomplete archive", "path", path, "err", removeErr)
	}
}

func prepareArchiveConfig(
	config json.RawMessage,
	files []*media.File,
) (*linka.Config, []*media.File, error) {
	var cfg linka.Config
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, nil, fmt.Errorf("decode config for export: %w", err)
	}
	filesByID := make(map[uuid.UUID]*media.File, len(files))
	for _, file := range files {
		if file != nil {
			filesByID[file.ID] = file
		}
	}
	seen := make(map[uuid.UUID]struct{})
	archiveFiles := make([]*media.File, 0, len(filesByID))
	for blockIndex := range cfg.Blocks {
		for elementIndex := range cfg.Blocks[blockIndex].Elements {
			element := &cfg.Blocks[blockIndex].Elements[elementIndex]
			if element.MediaID == nil {
				continue
			}
			file, exists := filesByID[*element.MediaID]
			if !exists {
				return nil, nil, fmt.Errorf("%w: media %s", ErrMissingMediaReference, *element.MediaID)
			}
			element.MediaURL = archiveMediaPath(file)
			if _, exists = seen[file.ID]; !exists {
				seen[file.ID] = struct{}{}
				archiveFiles = append(archiveFiles, file)
			}
		}
	}
	return &cfg, archiveFiles, nil
}

func archiveMediaPath(file *media.File) string {
	return "media/" + file.ID.String() + extensionForMIME(file.MIMEType)
}

func writeArchiveMedia(
	ctx context.Context,
	writer *zip.Writer,
	file *media.File,
	storageClient archiveStorage,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if storageClient == nil || file.SizeBytes < 0 || file.MinIOKey == "" {
		return fmt.Errorf("%w: media %s", ErrMissingMediaReference, file.ID)
	}
	reader, err := storageClient.GetObject(ctx, file.MinIOKey)
	if errors.Is(err, storage.ErrObjectNotFound) {
		return fmt.Errorf("%w: media %s", ErrMissingMediaReference, file.ID)
	}
	if err != nil {
		return fmt.Errorf("open media %s: %w", file.ID, err)
	}
	entry, err := writer.Create(archiveMediaPath(file))
	if err != nil {
		createErr := fmt.Errorf("create media entry %s: %w", file.ID, err)
		if closeErr := reader.Close(); closeErr != nil {
			return errors.Join(createErr, fmt.Errorf("close media %s: %w", file.ID, closeErr))
		}
		return createErr
	}
	written, copyErr := io.Copy(entry, io.LimitReader(reader, file.SizeBytes+1))
	closeErr := reader.Close()
	if copyErr != nil {
		return fmt.Errorf("write media %s: %w", file.ID, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close media %s: %w", file.ID, closeErr)
	}
	if written != file.SizeBytes {
		return fmt.Errorf("%w: media %s has inconsistent size", ErrMissingMediaReference, file.ID)
	}
	return nil
}

func writeArchivePictures(
	ctx context.Context,
	writer *zip.Writer,
	cfg *linka.Config,
	loader PictureLoader,
) error {
	paths := make(map[uuid.UUID]string)
	for blockIndex := range cfg.Blocks {
		for elementIndex := range cfg.Blocks[blockIndex].Elements {
			element := &cfg.Blocks[blockIndex].Elements[elementIndex]
			if element.Kind != linka.ElementKindImage || element.MediaID != nil ||
				element.SourcePictureID == nil {
				continue
			}
			pictureID := *element.SourcePictureID
			path, exists := paths[pictureID]
			if !exists {
				if loader == nil {
					return fmt.Errorf("%w: picture %s", ErrMissingMediaReference, pictureID)
				}
				data, mimeType, err := loader(ctx, pictureID)
				if err != nil {
					return fmt.Errorf("load Pictures Bank image %s: %w", pictureID, err)
				}
				if len(data) == 0 {
					return fmt.Errorf("%w: picture %s", ErrMissingMediaReference, pictureID)
				}
				path = "media/picture-" + pictureID.String() + extensionForMIME(mimeType)
				if err = writeZipEntry(writer, path, bytes.NewReader(data)); err != nil {
					return fmt.Errorf("write Pictures Bank image %s: %w", path, err)
				}
				paths[pictureID] = path
			}
			element.MediaURL = path
		}
	}
	return nil
}

func writeZipEntry(writer *zip.Writer, name string, reader io.Reader) error {
	entry, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, reader)
	return err
}

type importedArchive struct {
	Config json.RawMessage
	Files  map[string][]byte
}

func parseArchive(data []byte) (*importedArchive, error) {
	if int64(len(data)) > MaxArchiveSize {
		return nil, ErrArchiveTooLarge
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open zip", ErrInvalidArchive)
	}
	if len(reader.File) == 0 || len(reader.File) > maxArchiveEntries {
		return nil, fmt.Errorf("%w: invalid entry count", ErrInvalidArchive)
	}
	result := &importedArchive{Files: make(map[string][]byte)}
	var total uint64
	for _, entry := range reader.File {
		clean, nameErr := safeArchiveEntryName(entry)
		if nameErr != nil {
			return nil, nameErr
		}
		if entry.UncompressedSize64 > uint64(maxUncompressedTotal)-total {
			return nil, ErrArchiveTooLarge
		}
		total += entry.UncompressedSize64
		content, readErr := readArchiveEntry(entry)
		if readErr != nil {
			return nil, readErr
		}
		if storeErr := result.store(clean, content); storeErr != nil {
			return nil, storeErr
		}
	}
	if len(result.Config) == 0 {
		return nil, fmt.Errorf("%w: config.json is required", ErrInvalidArchive)
	}
	return result, nil
}

func safeArchiveEntryName(entry *zip.File) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(entry.Name))
	if clean != entry.Name || strings.HasPrefix(clean, "/") || clean == ".." ||
		strings.HasPrefix(clean, "../") || entry.FileInfo().IsDir() {
		return "", fmt.Errorf("%w: unsafe entry path", ErrInvalidArchive)
	}
	return clean, nil
}

func readArchiveEntry(entry *zip.File) ([]byte, error) {
	stream, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open entry", ErrInvalidArchive)
	}
	content, readErr := io.ReadAll(io.LimitReader(stream, maxUncompressedTotal+1))
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("%w: read entry", ErrInvalidArchive)
	}
	return content, nil
}

func (a *importedArchive) store(name string, content []byte) error {
	if name == "config.json" {
		if a.Config != nil {
			return fmt.Errorf("%w: duplicate config.json", ErrInvalidArchive)
		}
		a.Config = content
		return nil
	}
	if !strings.HasPrefix(name, "media/") {
		return fmt.Errorf("%w: unexpected entry", ErrInvalidArchive)
	}
	a.Files[name] = content
	return nil
}

func extensionForMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	}
	extensions, err := mime.ExtensionsByType(mimeType)
	if err != nil {
		return ""
	}
	if len(extensions) > 0 {
		return extensions[0]
	}
	return ""
}
