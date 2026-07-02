// Package auth contains authentication handlers and services.
package auth

import "errors"

var (
	ErrInvalidEmail       = errors.New("an incorrect email address was entered")
	ErrWeakPassword       = errors.New("the password must be 8–72 characters long")
	ErrEmailAlreadyExists = errors.New("email already exists")
)
