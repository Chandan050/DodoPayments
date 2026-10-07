package tests

import (
	"testing"

	"dodo-payments/internal/model"
)

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
		if got := model.TransitionAllowed(test.from, test.to); got != test.want {
			t.Errorf("TransitionAllowed(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}
