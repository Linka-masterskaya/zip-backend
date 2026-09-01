package linka_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
)

func TestParseFormatDefaultsToLinka2(t *testing.T) {
	format, err := linka.ParseFormat("")
	if err != nil {
		t.Fatalf("ParseFormat(\"\"): %v", err)
	}
	if format != linka.FormatLinka2 {
		t.Errorf("format = %q, want %q", format, linka.FormatLinka2)
	}
}

func TestParseFormatRejectsUnknownAndListsAvailable(t *testing.T) {
	_, err := linka.ParseFormat("looks-9")
	if !errors.Is(err, linka.ErrUnknownFormat) {
		t.Fatalf("err = %v, want ErrUnknownFormat", err)
	}
	// Сообщение должно подсказывать, что можно вместо этого:
	// иначе клиент угадывает значение по документации.
	for _, format := range linka.Formats() {
		if !strings.Contains(err.Error(), string(format)) {
			t.Errorf("error %q must list available format %q", err, format)
		}
	}
}

func TestRegisteredFormatsAreConvertible(t *testing.T) {
	cfg := &linka.Config{
		Metadata: linka.Metadata{Version: "2.0", Title: "t"},
		Settings: linka.Settings{Columns: 2, Rows: 1},
		Blocks: []linka.Block{{
			ID:       "b",
			Type:     linka.BlockTypeGrid,
			Elements: []linka.Element{{ID: "e", Kind: linka.ElementKindText, Value: "x"}},
		}},
	}
	formats := linka.Formats()
	if len(formats) < 2 {
		t.Fatalf("registry holds %d formats, expected the built-in ones", len(formats))
	}
	for _, format := range formats {
		converter, err := linka.ConverterFor(format)
		if err != nil {
			t.Fatalf("ConverterFor(%q): %v", format, err)
		}
		if converter.Format() != format {
			t.Errorf("converter reports %q, registered as %q", converter.Format(), format)
		}
		if converter.Description() == "" {
			t.Errorf("format %q has no description", format)
		}
		payload, err := converter.Convert(cfg)
		if err != nil {
			t.Fatalf("convert to %q: %v", format, err)
		}
		if payload == nil {
			t.Errorf("format %q produced nil payload", format)
		}
	}
}

// TestLinka2ConverterKeepsConfigIntact фиксирует, что формат по
// умолчанию ничего не переписывает.
func TestLinka2ConverterKeepsConfigIntact(t *testing.T) {
	cfg := &linka.Config{Metadata: linka.Metadata{Version: "2.0"}}
	converter, err := linka.ConverterFor(linka.FormatLinka2)
	if err != nil {
		t.Fatalf("ConverterFor: %v", err)
	}
	payload, err := converter.Convert(cfg)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if payload != any(cfg) {
		t.Error("linka-2 must return the very same config")
	}
}

func TestConvertRejectsNilConfig(t *testing.T) {
	for _, format := range linka.Formats() {
		converter, err := linka.ConverterFor(format)
		if err != nil {
			t.Fatalf("ConverterFor(%q): %v", format, err)
		}
		if _, err = converter.Convert(nil); err == nil {
			t.Errorf("format %q accepted a nil config", format)
		}
	}
}
