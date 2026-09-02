package auth

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

var ErrPasswordPolicy = errors.New("password does not satisfy security policy")

type PasswordPolicy struct {
	MinimumLength  int
	RequireUpper   bool
	RequireLower   bool
	RequireNumber  bool
	RequireSpecial bool
}

func (value PasswordPolicy) Validate(password string) error {
	if value.MinimumLength < 1 {
		value.MinimumLength = 1
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < value.MinimumLength {
		return ErrPasswordPolicy
	}
	var upper, lower, number, special bool
	for _, character := range password {
		upper = upper || unicode.IsUpper(character)
		lower = lower || unicode.IsLower(character)
		number = number || unicode.IsNumber(character)
		special = special || (!unicode.IsLetter(character) && !unicode.IsNumber(character) && !unicode.IsSpace(character))
	}
	if value.RequireUpper && !upper || value.RequireLower && !lower || value.RequireNumber && !number || value.RequireSpecial && !special {
		return ErrPasswordPolicy
	}
	return nil
}
