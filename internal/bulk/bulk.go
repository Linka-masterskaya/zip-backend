// Package bulk содержит общий словарь массовых операций: причины, по которым
// объект не попал в удаление, и форму записи о пропуске. Причины лежат в одном
// месте, чтобы медиа, наборы и папки отвечали одинаковыми строками и клиенту не
// приходилось разбирать разные значения для одного и того же случая.
package bulk

import "github.com/google/uuid"

// Причины пропуска. Общие для всех массовых операций.
const (
	// ReasonNotFound: объекта нет или он недоступен текущему пользователю.
	ReasonNotFound = "not_found"
	// ReasonInUse: объект ещё используется, удаление освободило бы ссылку.
	ReasonInUse = "in_use"
	// ReasonPublished: набор опубликован, снимать публикацию неявно нельзя.
	ReasonPublished = "published"
	// ReasonNotEmpty: папка не пуста, содержимое удалять не просили.
	ReasonNotEmpty = "not_empty"
)

// Skipped описывает один непройденный объект пачки.
type Skipped struct {
	ID     uuid.UUID `json:"id"`
	Reason string    `json:"reason"`
}
