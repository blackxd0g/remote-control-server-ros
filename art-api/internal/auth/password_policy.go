package auth

import (
	"errors"
	"fmt"
	"strings"
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
	if !utf8.ValidString(password) {
		return fmt.Errorf("%w: invalid UTF-8", ErrPasswordPolicy)
	}
	var missing []string
	if utf8.RuneCountInString(password) < value.MinimumLength {
		missing = append(missing, fmt.Sprintf("at least %d characters", value.MinimumLength))
	}
	var upper, lower, number, special bool
	for _, character := range password {
		upper = upper || unicode.IsUpper(character)
		lower = lower || unicode.IsLower(character)
		number = number || unicode.IsNumber(character)
		special = special || (!unicode.IsLetter(character) && !unicode.IsNumber(character) && !unicode.IsSpace(character))
	}
	for _, rule := range []struct {
		required, present bool
		description       string
	}{
		{value.RequireUpper, upper, "an uppercase letter"},
		{value.RequireLower, lower, "a lowercase letter"},
		{value.RequireNumber, number, "a number"},
		{value.RequireSpecial, special, "a special character"},
	} {
		if rule.required && !rule.present {
			missing = append(missing, rule.description)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: requires %s", ErrPasswordPolicy, strings.Join(missing, ", "))
	}
	return nil
}
