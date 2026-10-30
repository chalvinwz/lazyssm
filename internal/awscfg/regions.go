package awscfg

import (
	"regexp"
	"strings"
)

// Region is an AWS Region code and its display name.
type Region struct {
	Code string
	Name string
}

// Regions lists the Regions an ordinary AWS account offers, as published at
// https://docs.aws.amazon.com/global-infrastructure/latest/regions/aws-regions.html
// (34 as of October 2026). Regions launched later can still be typed into the
// region picker; see LooksLikeRegion.
var Regions = []Region{
	{"us-east-1", "US East (N. Virginia)"},
	{"us-east-2", "US East (Ohio)"},
	{"us-west-1", "US West (N. California)"},
	{"us-west-2", "US West (Oregon)"},
	{"af-south-1", "Africa (Cape Town)"},
	{"ap-east-1", "Asia Pacific (Hong Kong)"},
	{"ap-east-2", "Asia Pacific (Taipei)"},
	{"ap-northeast-1", "Asia Pacific (Tokyo)"},
	{"ap-northeast-2", "Asia Pacific (Seoul)"},
	{"ap-northeast-3", "Asia Pacific (Osaka)"},
	{"ap-south-1", "Asia Pacific (Mumbai)"},
	{"ap-south-2", "Asia Pacific (Hyderabad)"},
	{"ap-southeast-1", "Asia Pacific (Singapore)"},
	{"ap-southeast-2", "Asia Pacific (Sydney)"},
	{"ap-southeast-3", "Asia Pacific (Jakarta)"},
	{"ap-southeast-4", "Asia Pacific (Melbourne)"},
	{"ap-southeast-5", "Asia Pacific (Malaysia)"},
	{"ap-southeast-6", "Asia Pacific (New Zealand)"},
	{"ap-southeast-7", "Asia Pacific (Thailand)"},
	{"ca-central-1", "Canada (Central)"},
	{"ca-west-1", "Canada West (Calgary)"},
	{"eu-central-1", "Europe (Frankfurt)"},
	{"eu-central-2", "Europe (Zurich)"},
	{"eu-north-1", "Europe (Stockholm)"},
	{"eu-south-1", "Europe (Milan)"},
	{"eu-south-2", "Europe (Spain)"},
	{"eu-west-1", "Europe (Ireland)"},
	{"eu-west-2", "Europe (London)"},
	{"eu-west-3", "Europe (Paris)"},
	{"il-central-1", "Israel (Tel Aviv)"},
	{"me-central-1", "Middle East (UAE)"},
	{"me-south-1", "Middle East (Bahrain)"},
	{"mx-central-1", "Mexico (Central)"},
	{"sa-east-1", "South America (São Paulo)"},
}

var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

// LooksLikeRegion reports whether s has the shape of a Region code, such as
// eu-west-1 or us-gov-west-1.
func LooksLikeRegion(s string) bool {
	return regionPattern.MatchString(s)
}

// SuggestRegions returns up to three regions s most likely meant: the closest
// codes ("ap-southeast3"), else those whose name contains s ("sao paulo").
func SuggestRegions(s string) []Region {
	want := strings.ToLower(strings.TrimSpace(s))
	if want == "" {
		return nil
	}
	var out []Region
	best := max(1, len(want)/3) + 1 // only codes within a third of the length
	for _, r := range Regions {
		switch d := editDistance(want, r.Code); {
		case d < best:
			best, out = d, []Region{r}
		case d == best:
			out = append(out, r)
		}
	}
	if len(out) == 0 && len(want) >= 3 {
		for _, r := range Regions {
			if strings.Contains(strings.ToLower(FoldAccents(r.Name)), FoldAccents(want)) {
				out = append(out, r)
			}
		}
	}
	return out[:min(3, len(out))]
}

// plainLetters maps accented letters to the ones a plain keyboard types.
var plainLetters = func() map[rune]rune {
	accented := []rune("áàâãäåéèêëíìîïóòôõöúùûüçñÁÀÂÃÄÅÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑ")
	plain := []rune("aaaaaaeeeeiiiiooooouuuucnAAAAAAEEEEIIIIOOOOOUUUUCN")
	m := make(map[rune]rune, len(accented))
	for i, r := range accented {
		m[r] = plain[i]
	}
	return m
}()

// FoldAccents spells s without accents, so "sao paulo" finds "São Paulo".
func FoldAccents(s string) string {
	return strings.Map(func(r rune) rune {
		if p, ok := plainLetters[r]; ok {
			return p
		}
		return r
	}, s)
}
