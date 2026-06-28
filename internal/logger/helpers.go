package logger

import "log/slog"

// Err хелпер, забирающий рутину оборачивания ошибок.
func Err(err error) slog.Attr {
	return slog.Attr{
		Key:   "error",
		Value: slog.StringValue(err.Error()),
	}
}
