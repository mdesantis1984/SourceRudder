package youtube

import (
	"sort"
	"strings"
)

type subFormat struct {
	Ext string `json:"ext"`
}

type videoInfo struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	Channel           string                 `json:"channel"`
	Uploader          string                 `json:"uploader"`
	UploadDate        string                 `json:"upload_date"`
	Duration          float64                `json:"duration"`
	Language          string                 `json:"language"`
	Subtitles         map[string][]subFormat `json:"subtitles"`
	AutomaticCaptions map[string][]subFormat `json:"automatic_captions"`
}

type track struct {
	Lang   string
	Manual bool
}

// chooseTrack picks exactly one track: a manual track in the requested (or
// spoken) language, then that language's automatic "-orig" track, then any
// manual track, then any "-orig" track. live_chat is never a transcript.
func chooseTrack(info *videoInfo, requested string) (track, bool) {
	manual := usableKeys(info.Subtitles)
	auto := usableKeys(info.AutomaticCaptions)
	var origs []string
	for _, k := range auto {
		if strings.HasSuffix(strings.ToLower(k), "-orig") {
			origs = append(origs, k)
		}
	}

	targets := []string{}
	if requested != "" {
		targets = append(targets, strings.TrimSuffix(strings.ToLower(requested), "-orig"))
	}
	if info.Language != "" && languagePattern.MatchString(info.Language) {
		targets = append(targets, strings.ToLower(info.Language))
	}
	for _, target := range targets {
		if k, ok := matchLang(manual, target); ok {
			return track{Lang: k, Manual: true}, true
		}
		if k, ok := matchLang(origs, target+"-orig"); ok {
			return track{Lang: k}, true
		}
	}
	if len(manual) > 0 {
		return track{Lang: manual[0], Manual: true}, true
	}
	if len(origs) > 0 {
		return track{Lang: origs[0]}, true
	}
	return track{}, false
}

// usableKeys returns the sorted track keys that can be safely passed to
// --sub-langs; live_chat and anything that is not a plain language code are
// dropped.
func usableKeys(m map[string][]subFormat) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k == "live_chat" || !languagePattern.MatchString(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// matchLang prefers an exact (case-insensitive) match, then the same base
// language ("es" matches "es-419"; "es-orig" matches "es-ES-orig").
func matchLang(keys []string, want string) (string, bool) {
	for _, k := range keys {
		if strings.EqualFold(k, want) {
			return k, true
		}
	}
	wantOrig := strings.HasSuffix(want, "-orig")
	wantBase := baseLang(strings.TrimSuffix(want, "-orig"))
	for _, k := range keys {
		lk := strings.ToLower(k)
		isOrig := strings.HasSuffix(lk, "-orig")
		if isOrig != wantOrig {
			continue
		}
		if baseLang(strings.TrimSuffix(lk, "-orig")) == wantBase {
			return k, true
		}
	}
	return "", false
}

func baseLang(code string) string {
	if i := strings.IndexByte(code, '-'); i >= 0 {
		return code[:i]
	}
	return code
}
