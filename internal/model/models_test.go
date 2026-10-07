package model

import "testing"

func TestTransitionAllowed(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{from: "draft", to: "open", want: true},
		{from: "draft", to: "void", want: true},
		{from: "open", to: "paid", want: true},
		{from: "open", to: "void", want: true},
		{from: "paid", to: "open", want: false},
		{from: "void", to: "paid", want: false},
		{from: "uncollectible", to: "paid", want: false},
	}

	for _, test := range tests {
		if got := TransitionAllowed(test.from, test.to); got != test.want {
			t.Errorf("TransitionAllowed(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestSupportedCurrency(t *testing.T) {
	for _, currency := range []string{"USD", "GBP", "INR", "EUR"} {
		if !SupportedCurrency(currency) {
			t.Errorf("SupportedCurrency(%q) = false, want true", currency)
		}
	}
	for _, currency := range []string{"", "CAD", "usd"} {
		if SupportedCurrency(currency) {
			t.Errorf("SupportedCurrency(%q) = true, want false", currency)
		}
	}
}
