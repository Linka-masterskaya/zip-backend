// Package app assembles the server application and owns its lifecycle.
package app

import (
	"context"
	"log/slog"

	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

type closeFn struct {
	name string
	fn   func(context.Context) error
}

// Closer holds resource shutdown functions and runs them in reverse order.
type Closer struct {
	fns []closeFn
}

// Add registers a shutdown function. Resources must register immediately
// after they are successfully created, so a later failure still releases them.
func (c *Closer) Add(name string, fn func(context.Context) error) {
	c.fns = append(c.fns, closeFn{name: name, fn: fn})
}

// Len reports how many shutdown functions are registered.
func (c *Closer) Len() int { return len(c.fns) }

// Close runs every registered function in LIFO order. It does not stop at the
// first failure: one broken resource must not block releasing the others.
// It returns the first error encountered, or the context error if the context
// is done before the stack is drained.
func (c *Closer) Close(ctx context.Context) error {
	var firstErr error

	for i := len(c.fns) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			slog.Error("closer aborted", "remaining", i+1, logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
			return firstErr
		}

		entry := c.fns[i]
		if err := entry.fn(ctx); err != nil {
			slog.Error("resource close failed", "resource", entry.name, logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		slog.Debug("resource closed", "resource", entry.name)
	}

	return firstErr
}
