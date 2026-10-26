package safety

import (
	"maps"
	"testing"
)

// TestMaskHidesWhatItShould: each kind is hidden, keeping its last four;
// look-alikes that fail the check (a date, a phone number, an order number,
// an invalid IBAN or SSN, a short PIN-like word) are left as they are.
func TestMaskHidesWhatItShould(t *testing.T) {
	for _, c := range []struct {
		in, want string
		counts   map[string]int
	}{
		{"my card is 4242 4242 4242 4242, exp 12/27", "my card is [card ending 4242], exp 12/27", map[string]int{KindCard: 1}},
		{"card 4111-1111-1111-1111 and 5500005555555559", "card [card ending 1111] and [card ending 5559]", map[string]int{KindCard: 2}},
		{"order 1234 5678 9012 3456 shipped", "order 1234 5678 9012 3456 shipped", map[string]int{}}, // fails Luhn
		{"call me at +1 415-555-0132 or 2024-05-11", "call me at [phone ending 0132] or 2024-05-11", map[string]int{KindPhone: 1}},
		{"SSN 123-45-6789; not 000-12-3456 nor 912-34-5678", "SSN [SSN ending 6789]; not 000-12-3456 nor 912-34-5678", map[string]int{KindSSN: 1}},
		{"IBAN GB82 WEST 1234 5698 7654 32 please", "IBAN [IBAN ending 5432] please", map[string]int{KindIBAN: 1}},
		{"IBAN GB82 WEST 1234 5698 7654 33 is wrong", "IBAN GB82 WEST 1234 5698 7654 33 is wrong", map[string]int{}},
		{"key sk-abcdefghijklmnopqrstuvwxyz123456 and AKIAIOSFODNN7EXAMPLE", "key [secret] and [secret]", map[string]int{KindSecret: 2}},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIB\n-----END RSA PRIVATE KEY----- done", "[secret] done", map[string]int{KindSecret: 1}},
		{"My password is Hunter2!x and my PIN: 4821.", "My password is [hidden] and my PIN: [hidden].", map[string]int{KindPassword: 2}},
		{"I forgot my password again", "I forgot my password again", map[string]int{}},
		{"make sure the password is strong; Password: [Your password]", "make sure the password is strong; Password: [Your password]", map[string]int{}},
		{"User(password=hashed_password)", "User(password=hashed_password)", map[string]int{}},
		{"SLACK_TOKEN=xoxb-your-slack-token, real xoxb-123456789012-1234567890123-AbCdEfGhIjKl", "SLACK_TOKEN=xoxb-your-slack-token, real [secret]", map[string]int{KindSecret: 1}},
		{"mail elena.marconi23@examplemail.com", "mail [email]", map[string]int{KindEmail: 1}},
		{"call +1-613-742-5893 or (415) 555-0132 today", "call [phone ending 5893] or [phone ending 0132] today", map[string]int{KindPhone: 2}},
		{"account number 321958742659; acct: 0039-2861-7492", "account number [account ending 2659]; acct: [account ending 7492]", map[string]int{KindAccount: 2}},
		{"my passport number is YA9237614", "my passport number is [passport ending 7614]", map[string]int{KindPassport: 1}},
		{"driver's license: MAN-49382057-OK, license D63572849", "driver's license: [license ending 57OK], license [license ending 2849]", map[string]int{KindLicense: 2}},
		{"the MIT license and a passport photo; account settings", "the MIT license and a passport photo; account settings", map[string]int{}},
		{"version 1.24.6 of 2024-05-11, port 8080, id 4242", "version 1.24.6 of 2024-05-11, port 8080, id 4242", map[string]int{}},
		{"nothing sensitive here", "nothing sensitive here", map[string]int{}},
	} {
		got, counts := Mask(c.in)
		if got != c.want || !maps.Equal(counts, c.counts) {
			t.Errorf("Mask(%q) = %q %v, want %q %v", c.in, got, counts, c.want, c.counts)
		}
	}
}
