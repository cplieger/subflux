package release

import "testing"

var benchReleaseNames = []string{
	"The.Matrix.1999.2160p.UHD.BluRay.x265-GROUP",
	"Breaking.Bad.S05E16.Felina.1080p.BluRay.x264-DEMAND",
	"Oppenheimer.2023.IMAX.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-FLUX",
	"Silo.S02E01.The.Engineer.2160p.ATVP.WEB-DL.DDP5.1.H.265-NTb",
	"The.Bear.S03E10.Forever.1080p.HULU.WEB-DL.DDP5.1.H.264-NTb",
}

func BenchmarkParseReleaseName(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		for _, name := range benchReleaseNames {
			_ = ParseReleaseName(name)
		}
	}
}
