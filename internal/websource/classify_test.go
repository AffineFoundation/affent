package websource

import "testing"

func TestNormalizeHost(t *testing.T) {
	if got := NormalizeHost(" WWW.X.COM "); got != "x.com" {
		t.Fatalf("NormalizeHost() = %q, want x.com", got)
	}
}

func TestIsSearchResultPage(t *testing.T) {
	cases := []struct {
		name string
		host string
		path string
		want bool
	}{
		{name: "google search", host: "www.google.com", path: "/search", want: true},
		{name: "duckduckgo html", host: "duckduckgo.com", path: "/html", want: true},
		{name: "brave search", host: "search.brave.com", path: "/", want: true},
		{name: "google source page", host: "google.com", path: "/finance/quote/TAO-USD", want: false},
		{name: "ordinary", host: "example.com", path: "/search", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsSearchResultPage(c.host, c.path); got != c.want {
				t.Fatalf("IsSearchResultPage(%q, %q) = %v, want %v", c.host, c.path, got, c.want)
			}
		})
	}
}

func TestHostClasses(t *testing.T) {
	cases := []struct {
		name       string
		host       string
		redirector bool
		social     bool
		trap       bool
	}{
		{name: "short link", host: "t.co", redirector: true},
		{name: "x", host: "mobile.x.com", social: true, trap: true},
		{name: "twitter", host: "twitter.com", social: true, trap: true},
		{name: "reddit", host: "old.reddit.com", social: true},
		{name: "medium", host: "medium.com", social: true},
		{name: "ordinary", host: "example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRedirectorHost(c.host); got != c.redirector {
				t.Fatalf("IsRedirectorHost(%q) = %v, want %v", c.host, got, c.redirector)
			}
			if got := IsSocialOrDiscussionHost(c.host); got != c.social {
				t.Fatalf("IsSocialOrDiscussionHost(%q) = %v, want %v", c.host, got, c.social)
			}
			if got := IsKnownDirectReaderTrapHost(c.host); got != c.trap {
				t.Fatalf("IsKnownDirectReaderTrapHost(%q) = %v, want %v", c.host, got, c.trap)
			}
		})
	}
}
