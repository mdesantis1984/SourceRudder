// Package resultfilter removes search results that must never reach a caller:
// results whose URL has no host (cloaked engine links) and, when safe search
// is on, results hosted on adult domains. Only the URL host is inspected;
// titles and snippets are never matched against keywords.
package resultfilter

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// adultDomains are well-known adult registrable domains. A host matches when
// it equals one of them or is a subdomain of one.
var adultDomains = []string{
	"xnxx.com", "xvideos.com", "pornhub.com", "xhamster.com", "redtube.com",
	"youporn.com", "spankbang.com", "eporner.com", "chaturbate.com",
	"onlyfans.com", "brazzers.com", "rule34.xxx", "tube8.com", "tnaflix.com",
	"beeg.com", "motherless.com", "stripchat.com", "bongacams.com",
	"cam4.com", "livejasmin.com", "fansly.com", "e-hentai.org",
	"nhentai.net", "hentaihaven.xxx", "porn.com", "xtube.com", "thumbzilla.com",
	"4tube.com", "drtuber.com", "txxx.com", "hclips.com", "naughtyamerica.com",
	"realitykings.com", "bangbros.com", "fapello.com", "erome.com",
	"gelbooru.com", "danbooru.donmai.us", "e621.net", "rule34.paheal.net",
}

// adultTokens are host labels (split on '.' and '-') that mark a host as
// adult when they appear as a whole token.
var adultTokens = map[string]struct{}{
	"porn": {}, "porno": {}, "xxx": {}, "nsfw": {}, "hentai": {}, "sex": {},
}

// HasValidHost reports whether raw is an http(s) URL with a non-empty host.
func HasValidHost(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	return u.Hostname() != ""
}

// IsAdultHost reports whether host belongs to an adult site: a listed domain
// (or one of its subdomains), a whole-token match on a label, or any label
// containing "porn".
func IsAdultHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, d := range adultDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	for _, label := range strings.Split(host, ".") {
		if strings.Contains(label, "porn") {
			return true
		}
		for _, tok := range strings.Split(label, "-") {
			if _, ok := adultTokens[tok]; ok {
				return true
			}
		}
	}
	return false
}

// Filter returns the items that survive, in their original order, and how
// many were dropped for lacking a valid host and for being on an adult
// domain (the latter only when safeSearch is true). The input is not
// modified.
func Filter(items []types.SearchResultItem, safeSearch bool) (kept []types.SearchResultItem, noHost, adult int) {
	kept = make([]types.SearchResultItem, 0, len(items))
	for _, it := range items {
		if !HasValidHost(it.URL) {
			noHost++
			continue
		}
		if safeSearch {
			if u, err := url.Parse(strings.TrimSpace(it.URL)); err == nil && IsAdultHost(u.Hostname()) {
				adult++
				continue
			}
		}
		kept = append(kept, it)
	}
	return kept, noHost, adult
}

// FilterAdult is Filter without the host check: it only drops adult-domain
// items and is used for sources whose URLs are not http(s) (the local index).
func FilterAdult(items []types.SearchResultItem) (kept []types.SearchResultItem, adult int) {
	kept = make([]types.SearchResultItem, 0, len(items))
	for _, it := range items {
		if u, err := url.Parse(strings.TrimSpace(it.URL)); err == nil && IsAdultHost(u.Hostname()) {
			adult++
			continue
		}
		kept = append(kept, it)
	}
	return kept, adult
}

// NoHostWarning is the response warning for dropped host-less results.
func NoHostWarning(n int) string {
	return fmt.Sprintf("removed %d results without a valid URL host (cloaked engine links, searxng#6967)", n)
}

// AdultWarning is the response warning for dropped adult-domain results.
func AdultWarning(n int) string {
	return fmt.Sprintf("removed %d results from adult domains (safe search)", n)
}

// Warnings returns the warnings for the given drop counts (none for zero).
func Warnings(noHost, adult int) []string {
	var out []string
	if noHost > 0 {
		out = append(out, NoHostWarning(noHost))
	}
	if adult > 0 {
		out = append(out, AdultWarning(adult))
	}
	return out
}

// DistinctHosts counts the distinct hosts (case-insensitive, without a
// leading "www.") among the items; unparsable or host-less URLs are ignored.
func DistinctHosts(items []types.SearchResultItem) int {
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		u, err := url.Parse(strings.TrimSpace(it.URL))
		if err != nil {
			continue
		}
		h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if h != "" {
			seen[h] = struct{}{}
		}
	}
	return len(seen)
}
