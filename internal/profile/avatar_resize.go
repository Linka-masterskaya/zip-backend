package profile

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// AvatarMaxDimension bounds the longest avatar side after upload.
	AvatarMaxDimension = 512
	// AvatarMaxPixels prevents small compressed inputs from expanding into
	// unreasonably large in-memory images during decoding.
	AvatarMaxPixels   = 25_000_000
	avatarJPEGQuality = 90
)

type preparedAvatar struct {
	data        []byte
	contentType string
}

// prepareAvatar validates, decodes and, when needed, resizes an avatar while
// preserving its aspect ratio. PNG and JPEG keep their format. Large WebP
// images are normalized to PNG because the standard Go stack has no WebP
// encoder; small WebP images remain byte-for-byte unchanged after validation.
func prepareAvatar(reader io.Reader, size int64, contentType string) (preparedAvatar, error) {
	data, err := readAvatarData(reader, size)
	if err != nil {
		return preparedAvatar{}, err
	}

	img, cfg, err := decodeAvatar(data)
	if err != nil {
		return preparedAvatar{}, err
	}
	if cfg.Width <= AvatarMaxDimension && cfg.Height <= AvatarMaxDimension {
		return preparedAvatar{data: data, contentType: contentType}, nil
	}

	width, height := resizedDimensions(cfg.Width, cfg.Height, AvatarMaxDimension)
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return encodeResizedAvatar(dst, contentType)
}

func readAvatarData(reader io.Reader, size int64) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("avatar is empty")
	}
	data, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("avatar size mismatch")
	}
	return data, nil
}

func decodeAvatar(data []byte) (image.Image, image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, image.Config{}, fmt.Errorf("decode avatar config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > AvatarMaxPixels {
		return nil, image.Config{}, fmt.Errorf("avatar dimensions are invalid or too large")
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, image.Config{}, fmt.Errorf("decode avatar: %w", err)
	}
	return img, cfg, nil
}

func encodeResizedAvatar(img image.Image, contentType string) (preparedAvatar, error) {
	var out bytes.Buffer
	outType := contentType
	if err := encodeAvatarImage(&out, img, contentType, &outType); err != nil {
		return preparedAvatar{}, err
	}
	return preparedAvatar{data: out.Bytes(), contentType: outType}, nil
}

func encodeAvatarImage(out io.Writer, img image.Image, contentType string, outType *string) error {
	switch contentType {
	case "image/jpeg":
		if err := jpeg.Encode(out, img, &jpeg.Options{Quality: avatarJPEGQuality}); err != nil {
			return fmt.Errorf("encode resized jpeg avatar: %w", err)
		}
	case "image/png":
		if err := png.Encode(out, img); err != nil {
			return fmt.Errorf("encode resized png avatar: %w", err)
		}
	case "image/webp":
		*outType = "image/png"
		if err := png.Encode(out, img); err != nil {
			return fmt.Errorf("encode resized webp avatar as png: %w", err)
		}
	default:
		return fmt.Errorf("unsupported avatar content type %q", contentType)
	}
	return nil
}

func resizedDimensions(width, height, maxDimension int) (int, int) {
	if width <= maxDimension && height <= maxDimension {
		return width, height
	}
	if width >= height {
		return maxDimension, max(1, height*maxDimension/width)
	}
	return max(1, width*maxDimension/height), maxDimension
}
