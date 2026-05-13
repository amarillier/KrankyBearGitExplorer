package main

import "testing"

func TestVersionOrder(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.1", -1},
		{"0.1.1", "0.1.0", 1},
		{"0.1.1", "0.1.1", 0},
		{"v0.1.0", "0.1.1", -1},
		{"0.1.1", "v0.1.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"0.1.0-rc", "0.1.0", -1},
		{"0.1.0", "0.1.0-rc", 1},
	}
	for _, tt := range tests {
		got := versionOrder(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("versionOrder(%q, %q) = %d; want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
