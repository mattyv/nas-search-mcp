package main

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
)

func passwordHash(input io.Reader) ([]byte, error) {
	password, err := io.ReadAll(io.LimitReader(input, 73))
	if err != nil || len(password) < 12 || len(password) > 72 {
		return nil, errors.New("password must contain 12 to 72 bytes")
	}
	return bcrypt.GenerateFromPassword(password, 12)
}
