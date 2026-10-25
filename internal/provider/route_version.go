package provider

import (
	"regexp"
	"strconv"
)

// versionRe reads the first dotted number of a model id ("gpt-6.1-sol" →
// 6.1, "kimi-k2.8-preview" → 2.8): the version the fallback order sorts a
// model the family did not name by.
var versionRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// VersionDesc orders two model ids newest version first; equal versions keep
// their current order, so the provider's list breaks the tie.
func VersionDesc(a, b string) int {
	return -versionCompare(modelVersion(a), modelVersion(b))
}

// versionCompare orders two versions; positive when a is newer.
func versionCompare(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

// modelVersion is a model id's version as [major, minor, patch]; an id with
// no number is the oldest.
func modelVersion(id string) [3]int {
	m := versionRe.FindStringSubmatch(id)
	var v [3]int
	for i := 1; i < len(m); i++ {
		if m[i] != "" {
			v[i-1], _ = strconv.Atoi(m[i])
		}
	}
	return v
}
