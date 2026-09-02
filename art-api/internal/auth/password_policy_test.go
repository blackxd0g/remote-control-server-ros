package auth

import "testing"

func TestPasswordPolicy(t *testing.T) {
	policy := PasswordPolicy{MinimumLength: 12, RequireUpper: true, RequireLower: true, RequireNumber: true, RequireSpecial: true}
	if policy.Validate("Strong-Password7") != nil {
		t.Fatal("valid password was rejected")
	}
	for _, value := range []string{"short-A7!", "lowercase-7!", "UPPERCASE-7!", "NoNumbers!xxxx", "NoSpecial7xxxx"} {
		if policy.Validate(value) == nil {
			t.Fatalf("weak password %q was accepted", value)
		}
	}
}
