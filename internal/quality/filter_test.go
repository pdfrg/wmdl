package quality

import "testing"

func TestParse_1080pWebDL(t *testing.T) {
	r := Parse("The.Accountant.2.2025.1080p.WEB-DL.x264-GROUP")
	if r.Resolution != 1080 {
		t.Errorf("expected 1080, got %d", r.Resolution)
	}
	if r.Source != "web-dl" {
		t.Errorf("expected web-dl, got %q", r.Source)
	}
	if r.Codec != "x264" {
		t.Errorf("expected x264, got %q", r.Codec)
	}
	if r.ReleaseGroup != "GROUP" {
		t.Errorf("expected GROUP, got %q", r.ReleaseGroup)
	}
}

func TestParse_2160pHDRBluRay(t *testing.T) {
	r := Parse("Dune.Part.Three.2026.2160p.HDR.BluRay.x265-FLUX")
	if r.Resolution != 2160 {
		t.Errorf("expected 2160, got %d", r.Resolution)
	}
	if !r.HDR {
		t.Error("expected HDR")
	}
	if r.Source != "bluray" {
		t.Errorf("expected bluray, got %q", r.Source)
	}
	if r.Codec != "x265" {
		t.Errorf("expected x265, got %q", r.Codec)
	}
	if r.ReleaseGroup != "FLUX" {
		t.Errorf("expected FLUX, got %q", r.ReleaseGroup)
	}
}

func TestParse_StreamingWithDV(t *testing.T) {
	r := Parse("Severance.S02E01.2160p.ATVP.WEB-DL.DV.HDR10.DoVi.HEVC.DDP5.1-SMURF")
	if r.Resolution != 2160 {
		t.Errorf("expected 2160, got %d", r.Resolution)
	}
	if !r.HDR {
		t.Error("expected HDR from DV/HDR10/DoVi")
	}
	if r.Source != "web-dl" {
		t.Errorf("expected web-dl, got %q", r.Source)
	}
	if r.Codec != "h265" { // HEVC -> h265
		t.Errorf("expected h265, got %q", r.Codec)
	}
}

func TestParse_HDTV(t *testing.T) {
	r := Parse("Series.S01E01.1080p.HDTV.x264-GROUP")
	if r.Resolution != 1080 {
		t.Errorf("expected 1080, got %d", r.Resolution)
	}
	if r.Source != "hdtv" {
		t.Errorf("expected hdtv, got %q", r.Source)
	}
}

func TestParse_WebRip(t *testing.T) {
	r := Parse("Movie.2024.1080p.WEBRip.x265-NTb")
	if r.Resolution != 1080 {
		t.Errorf("expected 1080, got %d", r.Resolution)
	}
	if r.Source != "webrip" {
		t.Errorf("expected webrip, got %q", r.Source)
	}
}

func TestParse_NoResolution(t *testing.T) {
	r := Parse("Some.Release.Title")
	if r.Resolution != 0 {
		t.Errorf("expected 0, got %d", r.Resolution)
	}
}

func TestParse_4kUHD(t *testing.T) {
	r := Parse("Movie.2024.4K.UHD.BluRay.x265-GROUP")
	if r.Resolution != 2160 {
		t.Errorf("expected 2160, got %d", r.Resolution)
	}
}

func TestScore_MoviePrefs(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution: 2160,
		PreferHDR:        true,
		SourcePriority:   []string{"bluray", "web-dl", "webrip"},
		CodecPriority:    []string{"h265", "x265", "h264", "av1"},
		PreferredGroups:  []string{"FLUX", "NTb"},
		MinSeeders:       3,
	}

	cases := []struct {
		title string
		min   int // minimum expected score
	}{
		{"Dune.2026.2160p.HDR.BluRay.x265-FLUX", 430},
		{"Dune.2026.2160p.WEB-DL.x265-NTb", 300},
		{"Dune.2026.1080p.BluRay.x264-GROUP", 200},
		{"Dune.2026.1080p.WEBRip.x265-GROUP", 100},
	}

	for _, c := range cases {
		r := Parse(c.title)
		r.Seeders = 50
		s := Score(r, prefs)
		if s < c.min {
			t.Errorf("%q: expected score >= %d, got %d", c.title, c.min, s)
		}
	}
}

func TestScore_SeedersPenalty(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution: 1080,
		SourcePriority:   []string{"bluray", "web-dl", "webrip"},
		CodecPriority:    []string{"h265", "x265", "h264", "av1"},
		MinSeeders:       5,
	}

	r := Parse("Movie.2024.720p.WEBRip.x264-GROUP")
	r.Seeders = 2
	s := Score(r, prefs)
	if s >= 0 {
		t.Errorf("expected negative score for low seeders, got %d", s)
	}
}

func TestSortAndTop(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution: 2160,
		PreferHDR:        true,
		SourcePriority:   []string{"bluray", "web-dl", "webrip"},
		CodecPriority:    []string{"h265", "x265", "h264", "av1"},
		MinSeeders:       3,
	}

	releases := []ParsedRelease{
		Parse("Movie.2024.1080p.WEB-DL.x264-GROUP"),
		Parse("Movie.2024.2160p.HDR.BluRay.x265-FLUX"),
		Parse("Movie.2024.2160p.WEB-DL.x265-NTb"),
	}
	for i := range releases {
		releases[i].Seeders = 50
	}

	top := SortAndTop(releases, prefs, 2)
	if len(top) != 2 {
		t.Errorf("expected 2 results, got %d", len(top))
	}
	if top[0].Resolution != 2160 {
		t.Errorf("expected 2160p as top, got %d", top[0].Resolution)
	}
}

func TestBest(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution: 1080,
		SourcePriority:   []string{"bluray", "web-dl", "webrip"},
		CodecPriority:    []string{"h265", "x265", "h264", "av1"},
		MinSeeders:       1,
	}

	releases := []ParsedRelease{
		Parse("Movie.2024.720p.WEBRip.x264-GROUP"),
		Parse("Movie.2024.1080p.BluRay.x265-FLUX"),
		Parse("Movie.2024.1080p.WEB-DL.x264-GROUP"),
	}
	for i := range releases {
		releases[i].Seeders = 50
	}

	best := Best(releases, prefs)
	if best == nil {
		t.Fatal("expected non-nil best")
	}
	if best.Source != "bluray" {
		t.Errorf("expected bluray as best source, got %q", best.Source)
	}
}
