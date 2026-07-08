// Package pack содержит бизнес-логику, HTTP-хендлеры и хранилище для работы с паками.
package pack

import "time"

// Pack описывает сущность пака в системе.
type Pack struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
