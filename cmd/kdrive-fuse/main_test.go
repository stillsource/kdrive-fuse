package main

import "testing"

func TestWantsVersion(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"long flag", []string{"--version"}, true},
		{"short flag", []string{"-version"}, true},
		{"among others", []string{"--foo", "--version"}, true},
		{"no flag", []string{"--foo"}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		if got := wantsVersion(tc.args); got != tc.want {
			t.Errorf("%s: wantsVersion(%v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}
