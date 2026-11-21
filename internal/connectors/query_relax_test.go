package connectors

import "testing"

func TestRelaxQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"how to implement the retry logic in golang http client repo:foo/bar", "implement retry logic golang repo:foo/bar"},
		{"cómo hacer un retry de la conexión en go con timeout", "hacer retry conexión go"},
		{"language:go is:open one two three four five", "language:go is:open one two three four"},
		{"golang retry", "golang retry"},
		{"  golang   retry  ", "golang retry"},
		{"the and of", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := relaxQuery(tc.in); got != tc.want {
			t.Errorf("relaxQuery(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
