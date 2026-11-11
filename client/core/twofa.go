package core

import (
	"regexp"
	"strings"
)

// TwoFARedactedPlaceholder replaces message bodies that appear to contain OTP/2FA codes so they are not stored or indexed verbatim.
const TwoFARedactedPlaceholder = "[2FA/OTP code — content redacted for security]"

var (
	otpKeywordRe = regexp.MustCompile(`(?i)\b(otp|one[- ]time|verification\s+code|security\s+code|login\s+code|authentication\s+code|two[- ]factor|2fa|passcode|confirm\s+your|your\s+code\s+is)\b`)
	otpDigitsRe  = regexp.MustCompile(`(?:^|[^\d])(\d{4,8})(?:[^\d]|$)`)
	// interDigitSepRe matches a thin separator run between two digits (hyphen, spaces, common unicode spaces, zero-width) so Looks2FA can see codes split in HTML or formatted as 1-2-3-4.
	interDigitSepRe = regexp.MustCompile(`(\d)([\s\-\x{00a0}·\x{2009}\x{202f}\x{200b}]+)(\d)`)
)

const interDigitCollapseMaxPasses = 64

// collapseInterDigitSeparators joins digit groups when only thin separators sit between them, repeated until stable so patterns like 1-2-3-4-5-6 become one run for otpDigitsRe.
func collapseInterDigitSeparators(s string) string {
	prev := ""
	for i := 0; i < interDigitCollapseMaxPasses && s != prev; i++ {
		prev = s
		s = interDigitSepRe.ReplaceAllString(s, "$1$3")
	}
	return s
}

// Looks2FA reports whether text likely contains a short numeric one-time code together with common OTP phrasing.
func Looks2FA(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if !otpKeywordRe.MatchString(text) {
		return false
	}
	if otpDigitsRe.MatchString(text) {
		return true
	}
	return otpDigitsRe.MatchString(collapseInterDigitSeparators(text))
}
