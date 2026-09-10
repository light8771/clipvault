package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"
)

const (
	MaxPasswordBytes = 1024
	MinPasswordRunes = 8
	hashN            = 32768
	hashR            = 8
	hashP            = 1
	hashKeyLen       = 32
	hashSaltLen      = 16
	hashPrefix       = "$scrypt$v=1$"
)

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, hashSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	derived, err := scrypt.Key([]byte(password), salt, hashN, hashR, hashP, hashKeyLen)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}
	return fmt.Sprintf("%sn=%d$r=%d$p=%d$%s$%s", hashPrefix, hashN, hashR, hashP, hex.EncodeToString(salt), hex.EncodeToString(derived)), nil
}

var (
	ErrPasswordWhitespace = errors.New("password has leading or trailing whitespace")
	ErrPasswordTooShort   = errors.New("password is too short")
	ErrPasswordTooLong    = errors.New("password is too long")
)

func ValidatePassword(password string) error {
	if strings.TrimSpace(password) != password {
		return ErrPasswordWhitespace
	}
	if utf8.RuneCountInString(password) < MinPasswordRunes {
		return ErrPasswordTooShort
	}
	if len(password) > MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

func VerifyPassword(encoded, password string) bool {
	salt, expected, err := ParsePasswordHash(encoded)
	if err != nil {
		return false
	}
	actual, err := scrypt.Key([]byte(password), salt, hashN, hashR, hashP, len(expected))
	return err == nil && subtle.ConstantTimeCompare(actual, expected) == 1
}

func ParsePasswordHash(encoded string) ([]byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 8 || parts[1] != "scrypt" || parts[2] != "v=1" {
		return nil, nil, errors.New("invalid password hash format")
	}
	parse := func(part, name string) (int, bool) {
		v, err := strconv.Atoi(strings.TrimPrefix(part, name+"="))
		return v, err == nil && strings.HasPrefix(part, name+"=")
	}
	n, okN := parse(parts[3], "n")
	r, okR := parse(parts[4], "r")
	p, okP := parse(parts[5], "p")
	if !okN || !okR || !okP || n != hashN || r != hashR || p != hashP {
		return nil, nil, errors.New("unsupported password hash parameters")
	}
	salt, err := hex.DecodeString(parts[6])
	if err != nil || len(salt) != hashSaltLen {
		return nil, nil, errors.New("invalid password hash salt")
	}
	expected, err := hex.DecodeString(parts[7])
	if err != nil || len(expected) != hashKeyLen {
		return nil, nil, errors.New("invalid password hash value")
	}
	return salt, expected, nil
}

func ValidateStoredPasswordHash(encoded string) error {
	_, _, err := ParsePasswordHash(encoded)
	return err
}
