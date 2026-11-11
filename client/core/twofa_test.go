package core

import "testing"

// Verifies Looks2FA matches common OTP email/SMS phrasing plus a short digit code.
func TestLooks2FA_positive(t *testing.T) {
	cases := []string{
		"Your verification code is 123456",
		"Use OTP 987654 to continue",
		"Your one-time password: 1111",
		"Security code 482910 for login",
		"Your code is 556677 for this login",
		"Two-factor authentication: use 334455",
		"Confirm your email with 221100",
		"Login code 887766 — enter in the app",
		"Your verification code is 1-2-3-4-5-6",
		"Security code: 9 8 7 6 5 4",
		"OTP\n1\n2\n3\n4\n5\n6\nis valid",
	}
	for _, s := range cases {
		if !Looks2FA(s) {
			t.Errorf("expected true for %q", s)
		}
	}
}

// Verifies collapseInterDigitSeparators merges hyphen-, space-, and newline-separated digit runs for downstream digit regex checks.
func TestCollapseInterDigitSeparators(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1-2-3-4-5-6", "123456"},
		{"9 8 7 6", "9876"},
		{"1\n2\n3\n4", "1234"},
		{"12-34", "1234"},
		{"no-digits-here", "no-digits-here"},
	}
	for _, tc := range tests {
		if got := collapseInterDigitSeparators(tc.in); got != tc.want {
			t.Errorf("collapseInterDigitSeparators(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Verifies Looks2FA rejects messages that lack OTP keywords or lack a digit code.
func TestLooks2FA_negative(t *testing.T) {
	cases := []string{
		"",
		"Hello, dinner at 7pm",
		"Invoice total 123456 dollars please pay", // keyword invoice not in list
		"Your verification code is ready",         // no digits
		"Call me at 15550000000",                  // no OTP keyword
		"Your verification code is 1-2-3",         // only three digits after collapse
	}
	for _, s := range cases {
		if Looks2FA(s) {
			t.Errorf("expected false for %q", s)
		}
	}
}
