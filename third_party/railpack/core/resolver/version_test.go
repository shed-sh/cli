package resolver

import "testing"

func TestResolveToFuzzyVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple major version", "14", "14"},
		{"major.minor version", "18.2", "18.2"},
		{"major.minor.patch version", "18.3.2", "18.3.2"},
		{"x notation", "14.x", "14"},
		{"x notation with minor", "14.2.x", "14.2"},
		{"range notation", ">=22 <23", "22"},
		{"range notation major only", ">= 22", "22"},
		{"range notation with minor", ">=22.1 <23", "22"},
		{"range notation with patch", ">=22.1.3 <23", "22"},
		{"range notation simple", ">=20.0.0", "20"},
		{"range notation major.minor", ">=20.4", "20"},
		{"caret notation", "^14.3.2", "14"},
		{"caret notation minor", "^14.3", "14"},
		{"caret notation minor", "^20.0.0", "20"},
		{"caret notation major", "^14", "14"},
		{"tilde notation", "~14.3.2", "14.3.2"},
		{"v prefix", "v14.3.2", "14.3.2"},
		{"empty string", "", "latest"},
		{"star wildcard", "*", "latest"},
		{"whitespace", "  14.3  ", "14.3"},
		{"multiple x", "14.x.x", "14"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveToFuzzyVersion(tt.input)
			if result != tt.expected {
				t.Errorf("resolveToFuzzyVersion(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
