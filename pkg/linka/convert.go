package linka

import (
	"errors"
	"fmt"
	"sort"
)

// Format — идентификатор формата, в котором отдаётся config.json
// внутри .linka.
//
// Форматов больше одного, потому что клиенты расходятся: бэкенд
// хранит Linka Config 2.0, а Linka Looks 3.2.10 читает собственный
// формат набора 3.0. Совпадение строк версий обманчиво — это разные
// схемы, см. docs/compatibility/linka-looks/ADR-001-linka-looks-3.2.10.md
type Format string

const (
	// FormatLinka2 — родной формат бэкенда.
	FormatLinka2 Format = "linka-2"
	// FormatLooks3 — формат набора Linka Looks 3.0.
	FormatLooks3 Format = "looks-3"

	// DefaultFormat применяется, когда клиент формат не указал.
	DefaultFormat = FormatLinka2
)

// ErrUnknownFormat возвращается для формата, который никто не
// зарегистрировал.
var ErrUnknownFormat = errors.New("unknown export format")

// Converter превращает Linka Config 2.0 в представление конкретного
// формата. Результат уходит в config.json как есть, поэтому это
// именно та структура, которую ждёт клиент.
//
// Добавить формат — значит написать реализацию и зарегистрировать её
// в init(); ни экспорт, ни HTTP-слой при этом не меняются.
type Converter interface {
	// Format — идентификатор, по которому формат запрашивают.
	Format() Format
	// Description — человекочитаемое описание для документации и
	// сообщений об ошибках.
	Description() string
	// Convert возвращает представление набора. Если набор в этом
	// формате невыразим, нужно вернуть ошибку, а не урезанный
	// результат: молчаливая потеря заданий хуже отказа.
	Convert(cfg *Config) (any, error)
}

var converters = map[Format]Converter{}

// Register добавляет формат в реестр. Вызывается из init()
// реализаций; повторная регистрация одного идентификатора — ошибка
// сборки логики, поэтому паникуем сразу, а не выясняем это в проде.
func Register(converter Converter) {
	format := converter.Format()
	if _, exists := converters[format]; exists {
		panic(fmt.Sprintf("linka: format %q is already registered", format))
	}
	converters[format] = converter
}

// ConverterFor возвращает конвертер формата.
func ConverterFor(format Format) (Converter, error) {
	converter, ok := converters[format]
	if !ok {
		return nil, fmt.Errorf("%w: %q (available: %s)", ErrUnknownFormat, format, formatList())
	}
	return converter, nil
}

// ParseFormat разбирает значение, пришедшее от клиента. Пустая
// строка означает формат по умолчанию.
func ParseFormat(raw string) (Format, error) {
	if raw == "" {
		return DefaultFormat, nil
	}
	format := Format(raw)
	if _, err := ConverterFor(format); err != nil {
		return "", err
	}
	return format, nil
}

// Formats перечисляет зарегистрированные форматы в стабильном
// порядке — для документации и сообщений об ошибках.
func Formats() []Format {
	list := make([]Format, 0, len(converters))
	for format := range converters {
		list = append(list, format)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	return list
}

func formatList() string {
	list := Formats()
	parts := make([]string, 0, len(list))
	for _, format := range list {
		parts = append(parts, string(format))
	}
	return joinComma(parts)
}

func joinComma(parts []string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += ", "
		}
		result += part
	}
	return result
}

// linka2Converter отдаёт конфиг как есть: бэкенд уже хранит его в
// этом формате.
type linka2Converter struct{}

func (linka2Converter) Format() Format { return FormatLinka2 }

func (linka2Converter) Description() string {
	return "родной Linka Config 2.0 (metadata/settings/blocks[].elements[])"
}

func (linka2Converter) Convert(cfg *Config) (any, error) {
	if cfg == nil {
		return nil, errors.New("convert to Linka Config 2.0: config is nil")
	}
	return cfg, nil
}

func init() { Register(linka2Converter{}) }
