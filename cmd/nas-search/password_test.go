package main

import (
	"golang.org/x/crypto/bcrypt"
	"strings"
	"testing"
)

func TestPasswordHashBounds(t *testing.T) {
	for _, input := range []string{strings.Repeat("x", 11), strings.Repeat("x", 73)} {
		if _, err := passwordHash(strings.NewReader(input)); err == nil {
			t.Fatal("unsafe password size accepted")
		}
	}
	value := strings.Repeat("x", 12)
	hash, err := passwordHash(strings.NewReader(value))
	if err != nil {
		t.Fatal(err)
	}
	if err = bcrypt.CompareHashAndPassword(hash, []byte(value)); err != nil {
		t.Fatal("hash mismatch")
	}
	if cost, _ := bcrypt.Cost(hash); cost != 12 {
		t.Fatal("unexpected bcrypt cost")
	}
}
