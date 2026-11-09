package main

import (
	"strings"
	"testing"
)

func TestParseTopics(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
		err  bool
	}{
		{in: []string{`["tm_a","tm_b"]`}, want: "tm_a,tm_b"},
		{in: []string{"tm_a"}, want: "tm_a"},
		{in: []string{" tm_a , tm_b "}, want: "tm_a,tm_b"},
		{in: []string{`[" tm_a "]`}, want: "tm_a"},
		{in: nil, err: true},
		{in: []string{"tm_a", "tm_b"}, err: true},
		{in: []string{""}, err: true},
		{in: []string{"[]"}, err: true},
		{in: []string{"[tm_a"}, err: true},
	} {
		got, err := parseTopics(tc.in)
		if (err != nil) != tc.err || (!tc.err && strings.Join(got, ",") != tc.want) {
			t.Errorf("parseTopics(%q) = %v, %v; want %q (error %v)", tc.in, got, err, tc.want, tc.err)
		}
	}
}
