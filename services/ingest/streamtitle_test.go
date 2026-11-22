package ingest

import (
	"strings"
	"testing"
)

func TestParseStreamTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "GameTime", want: "GameTime"},
		{name: "encoded hash", in: "%23GameTime", want: "#GameTime"},
		{name: "chinese", in: "%E6%B8%B8%E6%88%8F%E6%97%B6%E9%97%B4", want: "游戏时间"},
		{name: "special characters", in: "a%20%26%20b%3F", want: "a & b?"},
		{name: "percent survives once", in: "100%2525", want: "100%25"},
		{name: "empty", in: "", want: ""},
		{name: "only spaces", in: "   ", want: ""},
		{name: "trims around", in: "  hi  ", want: "hi"},
		{name: "invalid encoding", in: "%zz", want: ""},
		{name: "truncated encoding", in: "%E6", want: ""},
		{name: "control character", in: "a%00b", want: ""},
		{name: "newline", in: "a%0Ab", want: ""},
		{name: "too long", in: strings.Repeat("a", MaxStreamTitleLength+1), want: ""},
		{name: "at the limit", in: strings.Repeat("a", MaxStreamTitleLength), want: strings.Repeat("a", MaxStreamTitleLength)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ParseStreamTitle(testCase.in); got != testCase.want {
				t.Fatalf("ParseStreamTitle(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

func TestParseStreamTitleFromQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "encoded hash", in: "title=%23GameTime", want: "#GameTime"},
		{name: "chinese", in: "title=%E6%B8%B8%E6%88%8F", want: "游戏"},
		{name: "other parameters", in: "a=1&title=hello&b=2", want: "hello"},
		{name: "absent", in: "a=1", want: ""},
		{name: "empty query", in: "", want: ""},
		{name: "duplicate title", in: "title=one&title=two", want: ""},
		{name: "invalid encoding", in: "title=%zz", want: ""},
		{name: "control character", in: "title=a%00b", want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ParseStreamTitleFromQuery(testCase.in); got != testCase.want {
				t.Fatalf("ParseStreamTitleFromQuery(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}
