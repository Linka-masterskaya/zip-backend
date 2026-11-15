package linka

import (
	"encoding/json"
	"fmt"
)

// NormalizeConfig приводит карточки устаревшей плоской формы к составной,
// чтобы в базе и в ответах API был один формат. Переписываются только
// elements блоков: остальное уходит как пришло, иначе omitempty в типах
// терял бы пустые answers/pairs, которые схема требует.
func NormalizeConfig(data json.RawMessage) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("normalize config: %w", err)
	}
	rawBlocks, ok := root["blocks"]
	if !ok {
		return data, nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(rawBlocks, &blocks); err != nil {
		return nil, fmt.Errorf("normalize config blocks: %w", err)
	}
	for i := range blocks {
		rawElements, ok := blocks[i]["elements"]
		if !ok {
			continue
		}
		var elements []Element
		if err := json.Unmarshal(rawElements, &elements); err != nil {
			return nil, fmt.Errorf("normalize block %d elements: %w", i, err)
		}
		normalized, err := json.Marshal(elements)
		if err != nil {
			return nil, fmt.Errorf("normalize block %d elements: %w", i, err)
		}
		blocks[i]["elements"] = normalized
	}
	normalizedBlocks, err := json.Marshal(blocks)
	if err != nil {
		return nil, fmt.Errorf("normalize config blocks: %w", err)
	}
	root["blocks"] = normalizedBlocks
	return json.Marshal(root)
}
