package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

const (
	MaxArchiveSize       = int64(50 * 1024 * 1024)
	maxArchiveEntries    = 256
	maxUncompressedTotal = int64(50 * 1024 * 1024)
)

var (
	ErrInvalidArchive  = errors.New("invalid linka archive")
	ErrArchiveTooLarge = errors.New("linka archive is too large")
)

type archiveStorage interface {
	GetObject(context.Context, string) (io.ReadCloser, error)
}

func buildArchive(
	ctx context.Context,
	config json.RawMessage,
	files []*media.File,
	storage archiveStorage,
	pictureLoaders ...PictureLoader,
) ([]byte, error) {
	exportedConfig, paths, err := archiveConfig(config, files)
	if err != nil {
		return nil, err
	}
	var pictureLoader PictureLoader
	if len(pictureLoaders) > 0 {
		pictureLoader = pictureLoaders[0]
	}
	exportedConfig, pictures, err := archivePictures(ctx, exportedConfig, pictureLoader)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if err = writeZipEntry(writer, "config.json", bytes.NewReader(exportedConfig)); err != nil {
		return nil, fmt.Errorf("write archive config: %w", err)
	}
	for _, file := range files {
		if err = writeArchiveMedia(ctx, writer, file, paths[file.ID], storage); err != nil {
			return nil, err
		}
		if int64(buffer.Len()) > MaxArchiveSize {
			return nil, ErrArchiveTooLarge
		}
	}
	for _, picture := range pictures {
		if err = writeZipEntry(writer, picture.name, bytes.NewReader(picture.data)); err != nil {
			return nil, fmt.Errorf("write Pictures Bank image %s: %w", picture.name, err)
		}
		if int64(buffer.Len()) > MaxArchiveSize {
			return nil, ErrArchiveTooLarge
		}
	}
	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("close archive: %w", err)
	}
	if int64(buffer.Len()) > MaxArchiveSize {
		return nil, ErrArchiveTooLarge
	}
	return buffer.Bytes(), nil
}

type externalArchivePicture struct {
	name string
	data []byte
}

func archivePictures(
	ctx context.Context,
	config json.RawMessage,
	loader PictureLoader,
) ([]byte, []externalArchivePicture, error) {
	var cfg linka.Config
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, nil, fmt.Errorf("decode config for Pictures Bank export: %w", err)
	}
	paths := make(map[uuid.UUID]string)
	pictures := make([]externalArchivePicture, 0)
	for blockIndex := range cfg.Blocks {
		for elementIndex := range cfg.Blocks[blockIndex].Elements {
			element := &cfg.Blocks[blockIndex].Elements[elementIndex]
			if element.Kind != linka.ElementKindImage || element.MediaID != nil ||
				element.SourcePictureID == nil {
				continue
			}
			pictureID := *element.SourcePictureID
			if path, ok := paths[pictureID]; ok {
				element.MediaURL = path
				continue
			}
			if loader == nil {
				return nil, nil, errors.New("pictures bank loader is required for export")
			}
			data, mimeType, err := loader(ctx, pictureID)
			if err != nil {
				return nil, nil, fmt.Errorf("load Pictures Bank image %s: %w", pictureID, err)
			}
			path := "media/picture-" + pictureID.String() + extensionForMIME(mimeType)
			paths[pictureID] = path
			element.MediaURL = path
			pictures = append(pictures, externalArchivePicture{name: path, data: data})
		}
	}
	exported, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Pictures Bank export config: %w", err)
	}
	return exported, pictures, nil
}

func archiveConfig(
	config json.RawMessage,
	files []*media.File,
) ([]byte, map[uuid.UUID]string, error) {
	var cfg linka.Config
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, nil, fmt.Errorf("decode config for export: %w", err)
	}
	paths := make(map[uuid.UUID]string, len(files))
	for _, file := range files {
		paths[file.ID] = "media/" + file.ID.String() + extensionForMIME(file.MIMEType)
	}
	for blockIndex := range cfg.Blocks {
		for elementIndex := range cfg.Blocks[blockIndex].Elements {
			element := &cfg.Blocks[blockIndex].Elements[elementIndex]
			if element.MediaID != nil {
				element.MediaURL = paths[*element.MediaID]
			}
		}
	}
	exportedConfig, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("encode exported config: %w", err)
	}
	return exportedConfig, paths, nil
}

func writeArchiveMedia(
	ctx context.Context,
	writer *zip.Writer,
	file *media.File,
	name string,
	storage archiveStorage,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, err := storage.GetObject(ctx, file.MinIOKey)
	if err != nil {
		return fmt.Errorf("open media %s: %w", file.ID, err)
	}
	writeErr := writeZipEntry(writer, name, io.LimitReader(reader, file.SizeBytes+1))
	closeErr := reader.Close()
	if writeErr != nil {
		return fmt.Errorf("write media %s: %w", file.ID, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close media %s: %w", file.ID, closeErr)
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
