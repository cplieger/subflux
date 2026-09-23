window.BENCHMARK_DATA = {
  "lastUpdate": 1790125870692,
  "repoUrl": "https://github.com/cplieger/ci",
  "entries": {
    "Benchmark": [
      {
        "commit": {
          "author": {
            "name": "cplieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "committer": {
            "name": "Christopher Plieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "id": "7070902ea9e83aa864442f770053ed7a3932c873",
          "message": "fix(ui): let password managers fill and save the first-run admin account\n\nOn the first-launch wizard the admin-profile card offered no password-manager integration on its username field, while the password field beside it worked. Three causes, none of them in the field itself: its markup already matches the browsers' documented sign-up form.\n\nlogin.html carries four page-states in one document, and only the sign-in one shipped visible. The page was therefore classified while the SIGN-IN form was the visible, focused credential form and the admin card was still display:none. Every state now starts hidden and showPage reveals the one that applies.\n\nBoth username fields also carried autofocus. Only the first one in a document takes effect, and that one sat inside the page about to be hidden, so the admin username field was never focused at all. showPage now focuses the revealed page first field, skipping any field inside a hidden subtree.\n\nThe admin form submits over fetch and hands off through replaceState, so no navigation follows the password just chosen and nothing asked the browser to save it. It is now offered explicitly through the Credential Management API, un-awaited and never fatal.\n\nAlso fixes the single-sign-on link form, which hid the username input but left its label captioning the gap: those labels are siblings of their inputs, so the closest(\"label\") lookup never matched one.",
          "timestamp": "2026-08-25T16:15:57Z",
          "url": "https://github.com/cplieger/subflux/commit/7070902ea9e83aa864442f770053ed7a3932c873"
        },
        "date": 1787700632509,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "BenchmarkActivityLog_StartEnd - B/op",
            "value": 31,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd",
            "value": 2729,
            "range": "± 65.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlign/200 - B/op",
            "value": 1269262,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/200 - allocs/op",
            "value": 5456,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/200",
            "value": 3241512,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/50 - B/op",
            "value": 191170,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/50 - allocs/op",
            "value": 1301,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/50",
            "value": 649718,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/500 - B/op",
            "value": 4651887,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/500 - allocs/op",
            "value": 14076,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/500",
            "value": 10943401,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - B/op",
            "value": 38387763,
            "range": "± 3.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - allocs/op",
            "value": 7,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500",
            "value": 8268651.5,
            "range": "± 79339.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - B/op",
            "value": 95985730,
            "range": "± 4.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - allocs/op",
            "value": 8,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000",
            "value": 44108620,
            "range": "± 148768.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - B/op",
            "value": 23986233,
            "range": "± 1.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - allocs/op",
            "value": 8,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500",
            "value": 6209819,
            "range": "± 64549.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - B/op",
            "value": 163880.5,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - allocs/op",
            "value": 6,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50",
            "value": 518161.5,
            "range": "± 2414.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignWithSplits - B/op",
            "value": 375071,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlignWithSplits - allocs/op",
            "value": 55,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlignWithSplits",
            "value": 22492761,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - B/op",
            "value": 228,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - allocs/op",
            "value": 3,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char",
            "value": 568,
            "range": "± 3.65",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty",
            "value": 2.4965,
            "range": "± 0.0035",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - B/op",
            "value": 228,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - allocs/op",
            "value": 3,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char",
            "value": 695.65,
            "range": "± 2.6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - B/op",
            "value": 240,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - allocs/op",
            "value": 4,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char",
            "value": 426.95,
            "range": "± 1.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - B/op",
            "value": 128056,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - allocs/op",
            "value": 8002,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues",
            "value": 314620,
            "range": "± 3052.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - B/op",
            "value": 12848,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - allocs/op",
            "value": 801,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues",
            "value": 31602.5,
            "range": "± 126.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - B/op",
            "value": 64056,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - allocs/op",
            "value": 4002,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues",
            "value": 157469.5,
            "range": "± 1070.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - B/op",
            "value": 24,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild",
            "value": 38.48,
            "range": "± 0.93",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - B/op",
            "value": 160,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - allocs/op",
            "value": 10,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles",
            "value": 885.4,
            "range": "± 6.25",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - B/op",
            "value": 16,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle",
            "value": 87.555,
            "range": "± 0.265",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - B/op",
            "value": 800,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - allocs/op",
            "value": 50,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles",
            "value": 4386.5,
            "range": "± 8.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - B/op",
            "value": 328,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - allocs/op",
            "value": 7,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1",
            "value": 570.1,
            "range": "± 1.6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - B/op",
            "value": 936,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - allocs/op",
            "value": 12,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10",
            "value": 1213,
            "range": "± 4.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - B/op",
            "value": 648,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - allocs/op",
            "value": 12,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5",
            "value": 933.95,
            "range": "± 11.35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit",
            "value": 79.535,
            "range": "± 0.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - B/op",
            "value": 944,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - allocs/op",
            "value": 7,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss",
            "value": 553.25,
            "range": "± 9.9",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup",
            "value": 76.28,
            "range": "± 0.495",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - B/op",
            "value": 7,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent",
            "value": 72.535,
            "range": "± 3.47",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - B/op",
            "value": 1256,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - allocs/op",
            "value": 5,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates",
            "value": 16869.5,
            "range": "± 62.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - B/op",
            "value": 2612561,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - allocs/op",
            "value": 49,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues",
            "value": 10508597,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - B/op",
            "value": 384340120,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues",
            "value": 174428927,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - B/op",
            "value": 96047256,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues",
            "value": 23958196,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCountNonText - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText",
            "value": 345.4,
            "range": "± 1.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - B/op",
            "value": 10707,
            "range": "± 5350.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - allocs/op",
            "value": 5,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000",
            "value": 4015914,
            "range": "± 32122.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - B/op",
            "value": 199834,
            "range": "± 112353.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - allocs/op",
            "value": 5,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000",
            "value": 18592906.5,
            "range": "± 194709.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - B/op",
            "value": 2583.5,
            "range": "± 1306.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - allocs/op",
            "value": 5,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000",
            "value": 1941796.5,
            "range": "± 47391.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - B/op",
            "value": 105.5,
            "range": "± 10.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - allocs/op",
            "value": 5,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000",
            "value": 131011.5,
            "range": "± 5607.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - B/op",
            "value": 9952,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - allocs/op",
            "value": 10,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100",
            "value": 8716.5,
            "range": "± 144.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - B/op",
            "value": 76384,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - allocs/op",
            "value": 13,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000",
            "value": 272946.5,
            "range": "± 911.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - B/op",
            "value": 40928,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - allocs/op",
            "value": 12,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500",
            "value": 116974.5,
            "range": "± 1799.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - B/op",
            "value": 24,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode",
            "value": 37.27,
            "range": "± 0.615",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024",
            "value": 16461.5,
            "range": "± 16.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256",
            "value": 3564,
            "range": "± 9.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096",
            "value": 80661.5,
            "range": "± 605.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - B/op",
            "value": 272,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - allocs/op",
            "value": 7,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10",
            "value": 499.95,
            "range": "± 2.6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - B/op",
            "value": 6832,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - allocs/op",
            "value": 89,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200",
            "value": 8443.5,
            "range": "± 276.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - B/op",
            "value": 1456,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - allocs/op",
            "value": 21,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50",
            "value": 1928.5,
            "range": "± 8.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - B/op",
            "value": 5296,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - allocs/op",
            "value": 100,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles",
            "value": 9228,
            "range": "± 135.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - B/op",
            "value": 5080,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - allocs/op",
            "value": 15,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10",
            "value": 1632.5,
            "range": "± 117.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - B/op",
            "value": 93008,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - allocs/op",
            "value": 209,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200",
            "value": 26788.5,
            "range": "± 302.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - B/op",
            "value": 21496,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - allocs/op",
            "value": 57,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50",
            "value": 6545.5,
            "range": "± 78.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - B/op",
            "value": 5360,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - allocs/op",
            "value": 15,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10",
            "value": 3662.5,
            "range": "± 133.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - B/op",
            "value": 46259,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - allocs/op",
            "value": 109,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100",
            "value": 33230,
            "range": "± 386.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - B/op",
            "value": 22896,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - allocs/op",
            "value": 57,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50",
            "value": 16536.5,
            "range": "± 127.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - B/op",
            "value": 107146.5,
            "range": "± 17.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - allocs/op",
            "value": 858,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler",
            "value": 183683.5,
            "range": "± 3567.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - B/op",
            "value": 16,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired",
            "value": 521.35,
            "range": "± 1.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - B/op",
            "value": 228,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - allocs/op",
            "value": 3,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides",
            "value": 597.15,
            "range": "± 1.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - B/op",
            "value": 228,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - allocs/op",
            "value": 3,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides",
            "value": 601.15,
            "range": "± 4.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit",
            "value": 28.13,
            "range": "± 0.155",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - B/op",
            "value": 240,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - allocs/op",
            "value": 4,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown",
            "value": 333.5,
            "range": "± 2.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - B/op",
            "value": 25340,
            "range": "± 16.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - allocs/op",
            "value": 298,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath",
            "value": 290843,
            "range": "± 202506.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - B/op",
            "value": 98074.5,
            "range": "± 260.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - allocs/op",
            "value": 1052,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024",
            "value": 493383.5,
            "range": "± 3247.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - B/op",
            "value": 11275.5,
            "range": "± 9.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - allocs/op",
            "value": 150,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128",
            "value": 49347.5,
            "range": "± 280.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - B/op",
            "value": 23602,
            "range": "± 46.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - allocs/op",
            "value": 280,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256",
            "value": 105799,
            "range": "± 907.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - B/op",
            "value": 48306,
            "range": "± 55.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - allocs/op",
            "value": 538,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512",
            "value": 228939,
            "range": "± 1929.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - B/op",
            "value": 5613,
            "range": "± 7.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - allocs/op",
            "value": 84,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64",
            "value": 23305.5,
            "range": "± 181.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - B/op",
            "value": 41259,
            "range": "± 170.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - allocs/op",
            "value": 254,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024",
            "value": 3259373,
            "range": "± 107622.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - B/op",
            "value": 5119.5,
            "range": "± 21.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - allocs/op",
            "value": 47,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128",
            "value": 324088,
            "range": "± 8370.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - B/op",
            "value": 10063,
            "range": "± 13.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - allocs/op",
            "value": 77,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256",
            "value": 721529.5,
            "range": "± 33395.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - B/op",
            "value": 20172,
            "range": "± 58.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - allocs/op",
            "value": 137,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512",
            "value": 1539461.5,
            "range": "± 54247.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - B/op",
            "value": 2779,
            "range": "± 11.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - allocs/op",
            "value": 31,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64",
            "value": 141847.5,
            "range": "± 3393.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - B/op",
            "value": 503,
            "range": "± 5.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - allocs/op",
            "value": 9,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024",
            "value": 52548,
            "range": "± 353.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - B/op",
            "value": 499,
            "range": "± 1.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - allocs/op",
            "value": 9,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128",
            "value": 7573,
            "range": "± 35.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - B/op",
            "value": 500,
            "range": "± 2.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - allocs/op",
            "value": 9,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256",
            "value": 14022,
            "range": "± 67.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - B/op",
            "value": 503,
            "range": "± 2.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - allocs/op",
            "value": 9,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512",
            "value": 26917,
            "range": "± 154.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - B/op",
            "value": 499.5,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - allocs/op",
            "value": 9,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64",
            "value": 4372.5,
            "range": "± 44.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - B/op",
            "value": 53785,
            "range": "± 52.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - allocs/op",
            "value": 705,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024",
            "value": 255941,
            "range": "± 1031.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - B/op",
            "value": 9919.5,
            "range": "± 10.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - allocs/op",
            "value": 105,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128",
            "value": 28959,
            "range": "± 146.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - B/op",
            "value": 20872,
            "range": "± 12.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - allocs/op",
            "value": 191,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256",
            "value": 57126.5,
            "range": "± 188.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - B/op",
            "value": 42883.5,
            "range": "± 36.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - allocs/op",
            "value": 365,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512",
            "value": 125509,
            "range": "± 463.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - B/op",
            "value": 4897.5,
            "range": "± 5.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - allocs/op",
            "value": 59,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64",
            "value": 14810,
            "range": "± 159.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - B/op",
            "value": 6709,
            "range": "± 28.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - allocs/op",
            "value": 110,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName",
            "value": 590626,
            "range": "± 5507.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - B/op",
            "value": 195065,
            "range": "± 171.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - allocs/op",
            "value": 1516,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT",
            "value": 119228.5,
            "range": "± 1047.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - B/op",
            "value": 317123,
            "range": "± 310.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - allocs/op",
            "value": 8402,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess",
            "value": 1150141.5,
            "range": "± 43540.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes",
            "value": 4.055,
            "range": "± 0.011",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - B/op",
            "value": 17736837.5,
            "range": "± 15.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - allocs/op",
            "value": 324034,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current",
            "value": 29549637.5,
            "range": "± 785210.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - B/op",
            "value": 1200563,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - allocs/op",
            "value": 24024,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current",
            "value": 1841111,
            "range": "± 26383.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - B/op",
            "value": 14364001.5,
            "range": "± 15.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - allocs/op",
            "value": 348165,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current",
            "value": 15341565.5,
            "range": "± 269560.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - B/op",
            "value": 23,
            "range": "± 136.0",
            "unit": "B/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - allocs/op",
            "value": 1,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider",
            "value": 375.8,
            "range": "± 333.5",
            "unit": "ns/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider",
            "value": 170.3,
            "range": "± 6.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - B/op",
            "value": 63031.5,
            "range": "± 24.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - allocs/op",
            "value": 390,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1",
            "value": 139902.5,
            "range": "± 1982.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - B/op",
            "value": 230141.5,
            "range": "± 35.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - allocs/op",
            "value": 1745,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20",
            "value": 282471.5,
            "range": "± 3684.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - B/op",
            "value": 88451,
            "range": "± 15.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - allocs/op",
            "value": 675,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5",
            "value": 162945.5,
            "range": "± 3044.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - B/op",
            "value": 128,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - allocs/op",
            "value": 2,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules",
            "value": 95.96,
            "range": "± 6.115",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - B/op",
            "value": 128,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - allocs/op",
            "value": 2,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules",
            "value": 95.625,
            "range": "± 0.65",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - B/op",
            "value": 128,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - allocs/op",
            "value": 2,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules",
            "value": 95.985,
            "range": "± 0.99",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - B/op",
            "value": 2,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1",
            "value": 13.825,
            "range": "± 0.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - B/op",
            "value": 2,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2",
            "value": 13.83,
            "range": "± 0.135",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - B/op",
            "value": 2,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3",
            "value": 13.84,
            "range": "± 0.03",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1",
            "value": 4.3615,
            "range": "± 0.007",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2",
            "value": 4.3655,
            "range": "± 0.0065",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3",
            "value": 4.3695,
            "range": "± 0.035",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release",
            "value": 41.8,
            "range": "± 0.07",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable",
            "value": 10.29,
            "range": "± 0.14",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match",
            "value": 27.76,
            "range": "± 0.17",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only",
            "value": 30.125,
            "range": "± 0.345",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10",
            "value": 304.3,
            "range": "± 1.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100",
            "value": 3163.5,
            "range": "± 174.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50",
            "value": 1553.5,
            "range": "± 13.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel",
            "value": 18.27,
            "range": "± 2.74",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - B/op",
            "value": 32206.5,
            "range": "± 163.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - allocs/op",
            "value": 511,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10",
            "value": 2614758,
            "range": "± 6296.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - B/op",
            "value": 322980,
            "range": "± 1260.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - allocs/op",
            "value": 5102,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100",
            "value": 26126956.5,
            "range": "± 92611.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - B/op",
            "value": 1611737,
            "range": "± 9363.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - allocs/op",
            "value": 25506.5,
            "range": "± 2.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500",
            "value": 130802332.5,
            "range": "± 793156.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - B/op",
            "value": 3136,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - allocs/op",
            "value": 13,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1",
            "value": 3288.5,
            "range": "± 151.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - B/op",
            "value": 26616,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - allocs/op",
            "value": 58,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10",
            "value": 24926.5,
            "range": "± 262.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - B/op",
            "value": 13440,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - allocs/op",
            "value": 33,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5",
            "value": 12986.5,
            "range": "± 84.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - B/op",
            "value": 654257.5,
            "range": "± 7.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - allocs/op",
            "value": 10,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues",
            "value": 2247741,
            "range": "± 83704.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - B/op",
            "value": 96116816,
            "range": "± 2.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - allocs/op",
            "value": 14,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues",
            "value": 45297514.5,
            "range": "± 493096.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - B/op",
            "value": 24019024,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - allocs/op",
            "value": 14,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues",
            "value": 6201951.5,
            "range": "± 69715.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - B/op",
            "value": 307335565,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - allocs/op",
            "value": 716,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate",
            "value": 67201181,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - B/op",
            "value": 799918396,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - allocs/op",
            "value": 1472,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits",
            "value": 255918654,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - B/op",
            "value": 192013,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - allocs/op",
            "value": 141,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset",
            "value": 649447,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0",
            "value": 1223,
            "range": "± 2.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1",
            "value": 1223.5,
            "range": "± 2.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2",
            "value": 1224,
            "range": "± 4.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3",
            "value": 1223.5,
            "range": "± 2.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B",
            "value": 363.25,
            "range": "± 0.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B",
            "value": 364.1,
            "range": "± 0.75",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - B/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - allocs/op",
            "value": 0,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B",
            "value": 84.625,
            "range": "± 0.375",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - B/op",
            "value": 41064,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - allocs/op",
            "value": 27,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates",
            "value": 36175.5,
            "range": "± 266.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - B/op",
            "value": 7800,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - allocs/op",
            "value": 8,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates",
            "value": 4205,
            "range": "± 22.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - B/op",
            "value": 20456,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - allocs/op",
            "value": 16,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates",
            "value": 13585.5,
            "range": "± 124.0",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - B/op",
            "value": 6528,
            "range": "± 0.0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - allocs/op",
            "value": 1,
            "range": "± 0.0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset",
            "value": 7732,
            "range": "± 218.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "name": "Christopher Plieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "committer": {
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "574eeace065e5f0976bc8b856426534542d9919f",
          "message": "fix(deps): update go dependencies (#877)",
          "timestamp": "2026-09-01T23:20:44Z",
          "url": "https://github.com/cplieger/subflux/commit/574eeace065e5f0976bc8b856426534542d9919f"
        },
        "date": 1788310343197,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "BenchmarkActivityLog_StartEnd - B/op",
            "value": 31,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd",
            "value": 1619,
            "range": "± 37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlign/200 - B/op",
            "value": 1269733,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/200 - allocs/op",
            "value": 5457,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/200",
            "value": 2950079,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/50 - B/op",
            "value": 191188,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/50 - allocs/op",
            "value": 1301,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/50",
            "value": 560993,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/500 - B/op",
            "value": 4651108,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/500 - allocs/op",
            "value": 14075,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/500",
            "value": 10098669,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - B/op",
            "value": 38387761.5,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500",
            "value": 7246987,
            "range": "± 234499",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - B/op",
            "value": 95985728,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000",
            "value": 41449505.5,
            "range": "± 736830.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - B/op",
            "value": 23986232.5,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500",
            "value": 5234487,
            "range": "± 187888",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - B/op",
            "value": 163880.5,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - allocs/op",
            "value": 6,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50",
            "value": 539378.5,
            "range": "± 6861",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignWithSplits - B/op",
            "value": 374894,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlignWithSplits - allocs/op",
            "value": 54,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlignWithSplits",
            "value": 15409554,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char",
            "value": 408.9,
            "range": "± 4.15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty",
            "value": 1.693,
            "range": "± 0.0145",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char",
            "value": 523.25,
            "range": "± 3.15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char",
            "value": 359.85,
            "range": "± 2.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - B/op",
            "value": 128056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - allocs/op",
            "value": 8002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues",
            "value": 239680.5,
            "range": "± 1576.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - B/op",
            "value": 12848,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - allocs/op",
            "value": 801,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues",
            "value": 24622.5,
            "range": "± 511",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - B/op",
            "value": 64056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - allocs/op",
            "value": 4002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues",
            "value": 122454.5,
            "range": "± 1639.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild",
            "value": 29.8,
            "range": "± 0.925",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - B/op",
            "value": 160,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles",
            "value": 731.4,
            "range": "± 7.8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle",
            "value": 73.295,
            "range": "± 0.63",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - B/op",
            "value": 800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - allocs/op",
            "value": 50,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles",
            "value": 3639,
            "range": "± 22",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - B/op",
            "value": 328,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1",
            "value": 497.5,
            "range": "± 4.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - B/op",
            "value": 936,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10",
            "value": 1064.5,
            "range": "± 7.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - B/op",
            "value": 648,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5",
            "value": 817.4,
            "range": "± 10.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit",
            "value": 71.38,
            "range": "± 0.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - B/op",
            "value": 944,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss",
            "value": 534.15,
            "range": "± 4.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup",
            "value": 66.6,
            "range": "± 0.63",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - B/op",
            "value": 7,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent",
            "value": 101.85,
            "range": "± 5.125",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - B/op",
            "value": 1256,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates",
            "value": 13483.5,
            "range": "± 271.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - B/op",
            "value": 2612713,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - allocs/op",
            "value": 49,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues",
            "value": 10734818,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - B/op",
            "value": 384340120,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues",
            "value": 163937593,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - B/op",
            "value": 96047268,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues",
            "value": 21686346,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCountNonText - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText",
            "value": 332.8,
            "range": "± 3.25",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - B/op",
            "value": 11233,
            "range": "± 5678.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000",
            "value": 4214019.5,
            "range": "± 44374",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - B/op",
            "value": 257009,
            "range": "± 136777",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000",
            "value": 22416729,
            "range": "± 232493",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - B/op",
            "value": 2769.5,
            "range": "± 1375.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000",
            "value": 2046135.5,
            "range": "± 35449.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - B/op",
            "value": 107,
            "range": "± 8.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000",
            "value": 119339,
            "range": "± 2417",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - B/op",
            "value": 9952,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100",
            "value": 9193.5,
            "range": "± 82",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - B/op",
            "value": 76384,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000",
            "value": 335236,
            "range": "± 2410",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - B/op",
            "value": 40928,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500",
            "value": 141137,
            "range": "± 1268.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode",
            "value": 29.79,
            "range": "± 0.205",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024",
            "value": 17058.5,
            "range": "± 130",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256",
            "value": 3584.5,
            "range": "± 36",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096",
            "value": 86689.5,
            "range": "± 961.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - B/op",
            "value": 272,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10",
            "value": 419.9,
            "range": "± 3.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - B/op",
            "value": 6832,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - allocs/op",
            "value": 89,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200",
            "value": 7089,
            "range": "± 135.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - B/op",
            "value": 1456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - allocs/op",
            "value": 21,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50",
            "value": 1621.5,
            "range": "± 19",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - B/op",
            "value": 5296,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - allocs/op",
            "value": 100,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles",
            "value": 8020.5,
            "range": "± 122",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - B/op",
            "value": 5080,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10",
            "value": 1642,
            "range": "± 72.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - B/op",
            "value": 93008,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - allocs/op",
            "value": 209,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200",
            "value": 25329,
            "range": "± 583",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - B/op",
            "value": 21496,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50",
            "value": 6214.5,
            "range": "± 57",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - B/op",
            "value": 5360,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10",
            "value": 3477,
            "range": "± 172.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - B/op",
            "value": 46259,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - allocs/op",
            "value": 109,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100",
            "value": 29498.5,
            "range": "± 1320.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - B/op",
            "value": 22896,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50",
            "value": 15535.5,
            "range": "± 733",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - B/op",
            "value": 107155.5,
            "range": "± 25",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - allocs/op",
            "value": 858,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler",
            "value": 122587.5,
            "range": "± 1301",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired",
            "value": 488.45,
            "range": "± 4.75",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides",
            "value": 432.95,
            "range": "± 4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides",
            "value": 438.3,
            "range": "± 2.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit",
            "value": 25.12,
            "range": "± 0.37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown",
            "value": 265.65,
            "range": "± 1.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - B/op",
            "value": 25724,
            "range": "± 13",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - allocs/op",
            "value": 309,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath",
            "value": 393894.5,
            "range": "± 162072.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - B/op",
            "value": 98206,
            "range": "± 237.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - allocs/op",
            "value": 1052,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024",
            "value": 373848.5,
            "range": "± 2854",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - B/op",
            "value": 11281.5,
            "range": "± 9.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - allocs/op",
            "value": 150,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128",
            "value": 38488,
            "range": "± 287",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - B/op",
            "value": 23635.5,
            "range": "± 29",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - allocs/op",
            "value": 280,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256",
            "value": 82149.5,
            "range": "± 1381.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - B/op",
            "value": 48391.5,
            "range": "± 40",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512",
            "value": 174331,
            "range": "± 1451",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - B/op",
            "value": 5618,
            "range": "± 3.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - allocs/op",
            "value": 84,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64",
            "value": 18658.5,
            "range": "± 223.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - B/op",
            "value": 41301,
            "range": "± 108.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - allocs/op",
            "value": 254,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024",
            "value": 2340596,
            "range": "± 47176.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - B/op",
            "value": 5117,
            "range": "± 10.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - allocs/op",
            "value": 47,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128",
            "value": 246563,
            "range": "± 5204.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - B/op",
            "value": 10055,
            "range": "± 17",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - allocs/op",
            "value": 77,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256",
            "value": 520772,
            "range": "± 7686",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - B/op",
            "value": 20159,
            "range": "± 53",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - allocs/op",
            "value": 137,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512",
            "value": 1125865,
            "range": "± 24409.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - B/op",
            "value": 2784,
            "range": "± 11",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - allocs/op",
            "value": 31,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64",
            "value": 109722.5,
            "range": "± 3273",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - B/op",
            "value": 504.5,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024",
            "value": 44222,
            "range": "± 710",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - B/op",
            "value": 499,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128",
            "value": 6458,
            "range": "± 43",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - B/op",
            "value": 500,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256",
            "value": 11944.5,
            "range": "± 219.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - B/op",
            "value": 501.5,
            "range": "± 2.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512",
            "value": 22809.5,
            "range": "± 307.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - B/op",
            "value": 500,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64",
            "value": 3747.5,
            "range": "± 46.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - B/op",
            "value": 53874,
            "range": "± 56",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - allocs/op",
            "value": 705,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024",
            "value": 196757.5,
            "range": "± 1647.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - B/op",
            "value": 9930.5,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - allocs/op",
            "value": 105,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128",
            "value": 24745,
            "range": "± 268.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - B/op",
            "value": 20896,
            "range": "± 18",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - allocs/op",
            "value": 191,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256",
            "value": 48729.5,
            "range": "± 325.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - B/op",
            "value": 42930,
            "range": "± 40.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - allocs/op",
            "value": 365,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512",
            "value": 102392.5,
            "range": "± 882.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - B/op",
            "value": 4900,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - allocs/op",
            "value": 59,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64",
            "value": 12768.5,
            "range": "± 138.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - B/op",
            "value": 6718,
            "range": "± 13.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - allocs/op",
            "value": 110,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName",
            "value": 450048.5,
            "range": "± 4279.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - B/op",
            "value": 195125.5,
            "range": "± 104.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - allocs/op",
            "value": 1516,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT",
            "value": 101210.5,
            "range": "± 1247.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - B/op",
            "value": 317699,
            "range": "± 398",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - allocs/op",
            "value": 8402,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess",
            "value": 915866.5,
            "range": "± 4790",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes",
            "value": 2.4735,
            "range": "± 0.0315",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - B/op",
            "value": 17736848,
            "range": "± 8",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - allocs/op",
            "value": 324034,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current",
            "value": 23667615,
            "range": "± 287599.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - B/op",
            "value": 1200563,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - allocs/op",
            "value": 24024,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current",
            "value": 1508208.5,
            "range": "± 13607",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - B/op",
            "value": 14364009.5,
            "range": "± 24",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - allocs/op",
            "value": 348165,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current",
            "value": 12180023,
            "range": "± 478504.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - B/op",
            "value": 23,
            "range": "± 71",
            "unit": "B/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - allocs/op",
            "value": 1,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider",
            "value": 441,
            "range": "± 311.5",
            "unit": "ns/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider",
            "value": 282.3,
            "range": "± 1.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - B/op",
            "value": 63044,
            "range": "± 26.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - allocs/op",
            "value": 390,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1",
            "value": 87331.5,
            "range": "± 1521.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - B/op",
            "value": 230131.5,
            "range": "± 41",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - allocs/op",
            "value": 1745,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20",
            "value": 202664.5,
            "range": "± 5149",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - B/op",
            "value": 88485.5,
            "range": "± 20.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - allocs/op",
            "value": 675,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5",
            "value": 107070,
            "range": "± 1985",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules",
            "value": 79.785,
            "range": "± 4.65",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules",
            "value": 80.875,
            "range": "± 0.815",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules",
            "value": 79.53,
            "range": "± 2.085",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1",
            "value": 10.56,
            "range": "± 0.235",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2",
            "value": 10.52,
            "range": "± 0.105",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3",
            "value": 10.515,
            "range": "± 0.08",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1",
            "value": 3.7095,
            "range": "± 0.1125",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2",
            "value": 3.7725,
            "range": "± 0.0795",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3",
            "value": 3.7155,
            "range": "± 0.1175",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release",
            "value": 27.175,
            "range": "± 0.385",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable",
            "value": 7.462,
            "range": "± 0.132",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match",
            "value": 18.885,
            "range": "± 0.375",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only",
            "value": 19.95,
            "range": "± 0.28",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10",
            "value": 207.45,
            "range": "± 2.55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100",
            "value": 2276,
            "range": "± 37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50",
            "value": 1114.5,
            "range": "± 35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel",
            "value": 21.7,
            "range": "± 6.7095",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - B/op",
            "value": 32256.5,
            "range": "± 172.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - allocs/op",
            "value": 511,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10",
            "value": 2206378,
            "range": "± 25140",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - B/op",
            "value": 322782,
            "range": "± 1908.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - allocs/op",
            "value": 5102,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100",
            "value": 22111054,
            "range": "± 191583.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - B/op",
            "value": 1612138,
            "range": "± 7516.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - allocs/op",
            "value": 25507,
            "range": "± 2.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500",
            "value": 110978719,
            "range": "± 1178196.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - B/op",
            "value": 3136,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1",
            "value": 2942,
            "range": "± 58",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - B/op",
            "value": 26616,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - allocs/op",
            "value": 58,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10",
            "value": 21190,
            "range": "± 154.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - B/op",
            "value": 13440,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - allocs/op",
            "value": 33,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5",
            "value": 11604.5,
            "range": "± 331",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - B/op",
            "value": 654257.5,
            "range": "± 7",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues",
            "value": 2303348,
            "range": "± 69948",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - B/op",
            "value": 96116816,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues",
            "value": 41538866.5,
            "range": "± 356338",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - B/op",
            "value": 24019025,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues",
            "value": 5191046,
            "range": "± 140466",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - B/op",
            "value": 307335562,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - allocs/op",
            "value": 716,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate",
            "value": 66855646,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - B/op",
            "value": 799918395,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - allocs/op",
            "value": 1472,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits",
            "value": 248639829,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - B/op",
            "value": 192009,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - allocs/op",
            "value": 141,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset",
            "value": 616989,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0",
            "value": 891.95,
            "range": "± 8.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1",
            "value": 890.6,
            "range": "± 7.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2",
            "value": 892.65,
            "range": "± 9.55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3",
            "value": 888.2,
            "range": "± 6.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B",
            "value": 406.55,
            "range": "± 13.8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B",
            "value": 364.6,
            "range": "± 6.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B",
            "value": 105.2,
            "range": "± 2.65",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - B/op",
            "value": 41064,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - allocs/op",
            "value": 27,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates",
            "value": 30579,
            "range": "± 362.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - B/op",
            "value": 7800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates",
            "value": 3727,
            "range": "± 55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - B/op",
            "value": 20456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - allocs/op",
            "value": 16,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates",
            "value": 11760.5,
            "range": "± 141.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - B/op",
            "value": 6528,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset",
            "value": 6974.5,
            "range": "± 412",
            "unit": "ns/op",
            "extra": "10 samples, median"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "name": "Christopher Plieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "committer": {
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "c7c2a56c10b85a4e0b5b155f46d75b4906546b04",
          "message": "chore(deps): update cplieger/ci digest to a2bb34b (#580)",
          "timestamp": "2026-09-09T00:02:08Z",
          "url": "https://github.com/cplieger/ci/commit/c7c2a56c10b85a4e0b5b155f46d75b4906546b04"
        },
        "date": 1788915576345,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "BenchmarkActivityLog_StartEnd - B/op",
            "value": 31,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd",
            "value": 3084.5,
            "range": "± 8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlign/200 - B/op",
            "value": 1266870,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/200 - allocs/op",
            "value": 5456,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/200",
            "value": 3128430,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/50 - B/op",
            "value": 190521,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/50 - allocs/op",
            "value": 1301,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/50",
            "value": 616061,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/500 - B/op",
            "value": 4652247,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/500 - allocs/op",
            "value": 14076,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/500",
            "value": 10658718,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - B/op",
            "value": 38387761,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500",
            "value": 8316772.5,
            "range": "± 41688",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - B/op",
            "value": 95985728,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000",
            "value": 44436363.5,
            "range": "± 2119399.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - B/op",
            "value": 23986232,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500",
            "value": 6490324.5,
            "range": "± 29663.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - B/op",
            "value": 163880,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - allocs/op",
            "value": 6,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50",
            "value": 517700.5,
            "range": "± 1226.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignWithSplits - B/op",
            "value": 374737,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlignWithSplits - allocs/op",
            "value": 53,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlignWithSplits",
            "value": 22467293,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char",
            "value": 575.2,
            "range": "± 2.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty",
            "value": 2.494,
            "range": "± 0.003",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char",
            "value": 689.15,
            "range": "± 6.65",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char",
            "value": 434.7,
            "range": "± 5.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - B/op",
            "value": 128056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - allocs/op",
            "value": 8002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues",
            "value": 303210,
            "range": "± 1136",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - B/op",
            "value": 12848,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - allocs/op",
            "value": 801,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues",
            "value": 30423.5,
            "range": "± 146.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - B/op",
            "value": 64056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - allocs/op",
            "value": 4002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues",
            "value": 151513,
            "range": "± 606.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild",
            "value": 38.065,
            "range": "± 0.795",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - B/op",
            "value": 160,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles",
            "value": 884.3,
            "range": "± 36.9",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle",
            "value": 88.86,
            "range": "± 3.66",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - B/op",
            "value": 800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - allocs/op",
            "value": 50,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles",
            "value": 4399.5,
            "range": "± 194",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - B/op",
            "value": 328,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1",
            "value": 564.55,
            "range": "± 2.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - B/op",
            "value": 936,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10",
            "value": 1187,
            "range": "± 6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - B/op",
            "value": 648,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5",
            "value": 917.35,
            "range": "± 10.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit",
            "value": 79.515,
            "range": "± 0.045",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - B/op",
            "value": 944,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss",
            "value": 560.95,
            "range": "± 5.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup",
            "value": 76.46,
            "range": "± 0.13",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - B/op",
            "value": 7,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent",
            "value": 78.31,
            "range": "± 6.105",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - B/op",
            "value": 1256,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates",
            "value": 16758,
            "range": "± 46.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - B/op",
            "value": 2612417,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - allocs/op",
            "value": 48,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues",
            "value": 10959941,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - B/op",
            "value": 384340120,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues",
            "value": 175836973,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - B/op",
            "value": 96047261,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues",
            "value": 25133822,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCountNonText - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText",
            "value": 345.5,
            "range": "± 0.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - B/op",
            "value": 10707,
            "range": "± 6026.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000",
            "value": 4052239.5,
            "range": "± 263488",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - B/op",
            "value": 213437,
            "range": "± 8902",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000",
            "value": 19113718,
            "range": "± 117245",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - B/op",
            "value": 2564,
            "range": "± 1258.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000",
            "value": 1877468.5,
            "range": "± 30061",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - B/op",
            "value": 106,
            "range": "± 9",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000",
            "value": 118955,
            "range": "± 6164",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - B/op",
            "value": 9952,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100",
            "value": 8633.5,
            "range": "± 88.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - B/op",
            "value": 76384,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000",
            "value": 280969,
            "range": "± 4747.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - B/op",
            "value": 40928,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500",
            "value": 119833.5,
            "range": "± 484",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode",
            "value": 36.92,
            "range": "± 0.215",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024",
            "value": 16316,
            "range": "± 67",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256",
            "value": 3517,
            "range": "± 19.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096",
            "value": 79572,
            "range": "± 94",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - B/op",
            "value": 272,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10",
            "value": 527.9,
            "range": "± 6.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - B/op",
            "value": 6832,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - allocs/op",
            "value": 89,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200",
            "value": 8658,
            "range": "± 79.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - B/op",
            "value": 1456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - allocs/op",
            "value": 21,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50",
            "value": 1965,
            "range": "± 11",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - B/op",
            "value": 5296,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - allocs/op",
            "value": 100,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles",
            "value": 9101,
            "range": "± 45.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - B/op",
            "value": 5080,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10",
            "value": 1611.5,
            "range": "± 96",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - B/op",
            "value": 93008,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - allocs/op",
            "value": 209,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200",
            "value": 26644.5,
            "range": "± 421",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - B/op",
            "value": 21496,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50",
            "value": 6516.5,
            "range": "± 61",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - B/op",
            "value": 5360,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10",
            "value": 3680,
            "range": "± 245.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - B/op",
            "value": 46259,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - allocs/op",
            "value": 109,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100",
            "value": 32680.5,
            "range": "± 726",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - B/op",
            "value": 22896,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50",
            "value": 16638.5,
            "range": "± 70",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - B/op",
            "value": 107162,
            "range": "± 34.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - allocs/op",
            "value": 858,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler",
            "value": 179150.5,
            "range": "± 3952.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired",
            "value": 526.95,
            "range": "± 2.6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides",
            "value": 604.45,
            "range": "± 6.95",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides",
            "value": 607.9,
            "range": "± 4.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit",
            "value": 28.105,
            "range": "± 0.11",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown",
            "value": 336,
            "range": "± 1.8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - B/op",
            "value": 25724,
            "range": "± 11",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - allocs/op",
            "value": 309,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath",
            "value": 458166.5,
            "range": "± 297798",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - B/op",
            "value": 98151.5,
            "range": "± 163",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - allocs/op",
            "value": 1052,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024",
            "value": 520311,
            "range": "± 5076",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - B/op",
            "value": 11275,
            "range": "± 18.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - allocs/op",
            "value": 150,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128",
            "value": 50924,
            "range": "± 469",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - B/op",
            "value": 23605.5,
            "range": "± 18.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - allocs/op",
            "value": 280,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256",
            "value": 109262,
            "range": "± 632.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - B/op",
            "value": 48339.5,
            "range": "± 38",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512",
            "value": 238880.5,
            "range": "± 1321.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - B/op",
            "value": 5612.5,
            "range": "± 7.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - allocs/op",
            "value": 84,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64",
            "value": 23827.5,
            "range": "± 293",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - B/op",
            "value": 41250,
            "range": "± 50.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - allocs/op",
            "value": 254,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024",
            "value": 3061819.5,
            "range": "± 36673.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - B/op",
            "value": 5119,
            "range": "± 14.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - allocs/op",
            "value": 47,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128",
            "value": 310839.5,
            "range": "± 3353.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - B/op",
            "value": 10061,
            "range": "± 21.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - allocs/op",
            "value": 77,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256",
            "value": 681210.5,
            "range": "± 12497.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - B/op",
            "value": 20169.5,
            "range": "± 46",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - allocs/op",
            "value": 137,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512",
            "value": 1452938,
            "range": "± 23185",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - B/op",
            "value": 2783.5,
            "range": "± 10",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - allocs/op",
            "value": 31,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64",
            "value": 136983,
            "range": "± 2211",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - B/op",
            "value": 503,
            "range": "± 10",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024",
            "value": 54015,
            "range": "± 592",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - B/op",
            "value": 500,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128",
            "value": 7819,
            "range": "± 51",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - B/op",
            "value": 501,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256",
            "value": 14440,
            "range": "± 86",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - B/op",
            "value": 501,
            "range": "± 2.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512",
            "value": 27722.5,
            "range": "± 238",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - B/op",
            "value": 499,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64",
            "value": 4532,
            "range": "± 32",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - B/op",
            "value": 53745,
            "range": "± 61.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - allocs/op",
            "value": 705,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024",
            "value": 256919.5,
            "range": "± 958.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - B/op",
            "value": 9911,
            "range": "± 7",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - allocs/op",
            "value": 105,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128",
            "value": 30120.5,
            "range": "± 510",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - B/op",
            "value": 20860.5,
            "range": "± 21",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - allocs/op",
            "value": 191,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256",
            "value": 59107,
            "range": "± 404.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - B/op",
            "value": 42891.5,
            "range": "± 45.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - allocs/op",
            "value": 365,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512",
            "value": 126051,
            "range": "± 544.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - B/op",
            "value": 4895,
            "range": "± 5.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - allocs/op",
            "value": 59,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64",
            "value": 15326.5,
            "range": "± 73.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - B/op",
            "value": 6708,
            "range": "± 26.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - allocs/op",
            "value": 110,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName",
            "value": 572685.5,
            "range": "± 3374",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - B/op",
            "value": 194980,
            "range": "± 86.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - allocs/op",
            "value": 1516,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT",
            "value": 112600.5,
            "range": "± 693",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - B/op",
            "value": 317508.5,
            "range": "± 564.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - allocs/op",
            "value": 8402,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess",
            "value": 1115923.5,
            "range": "± 5390.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes",
            "value": 4.0555,
            "range": "± 0.0105",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - B/op",
            "value": 17736827,
            "range": "± 12",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - allocs/op",
            "value": 324034,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current",
            "value": 29494521.5,
            "range": "± 367044.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - B/op",
            "value": 1200562,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - allocs/op",
            "value": 24024,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current",
            "value": 1749437,
            "range": "± 4920.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - B/op",
            "value": 14363995,
            "range": "± 13",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - allocs/op",
            "value": 348165,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current",
            "value": 15814939,
            "range": "± 548794",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - B/op",
            "value": 23,
            "range": "± 69",
            "unit": "B/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - allocs/op",
            "value": 1,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider",
            "value": 401,
            "range": "± 244.15",
            "unit": "ns/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider",
            "value": 168.5,
            "range": "± 7.55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - B/op",
            "value": 63033.5,
            "range": "± 13",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - allocs/op",
            "value": 390,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1",
            "value": 137840,
            "range": "± 3297.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - B/op",
            "value": 230143.5,
            "range": "± 34",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - allocs/op",
            "value": 1745,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20",
            "value": 277082.5,
            "range": "± 7854",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - B/op",
            "value": 88458,
            "range": "± 26",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - allocs/op",
            "value": 675,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5",
            "value": 159591.5,
            "range": "± 3283",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules",
            "value": 94.915,
            "range": "± 5.105",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules",
            "value": 96.45,
            "range": "± 0.855",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules",
            "value": 93.93,
            "range": "± 0.99",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1",
            "value": 12.86,
            "range": "± 0.15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2",
            "value": 12.78,
            "range": "± 0.06",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3",
            "value": 12.835,
            "range": "± 0.255",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1",
            "value": 4.368,
            "range": "± 0.003",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2",
            "value": 4.364,
            "range": "± 0.018",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3",
            "value": 4.368,
            "range": "± 0.0255",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release",
            "value": 41.485,
            "range": "± 0.215",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable",
            "value": 9.686,
            "range": "± 0.0235",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match",
            "value": 26.84,
            "range": "± 0.035",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only",
            "value": 28.24,
            "range": "± 0.28",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10",
            "value": 286.9,
            "range": "± 3.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100",
            "value": 3178.5,
            "range": "± 178",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50",
            "value": 1519,
            "range": "± 21.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel",
            "value": 18.135,
            "range": "± 2.325",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - B/op",
            "value": 32302.5,
            "range": "± 178.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - allocs/op",
            "value": 511,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10",
            "value": 2803896.5,
            "range": "± 21655.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - B/op",
            "value": 323152.5,
            "range": "± 1765",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - allocs/op",
            "value": 5102,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100",
            "value": 27989838.5,
            "range": "± 218176.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - B/op",
            "value": 1614079,
            "range": "± 7042",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - allocs/op",
            "value": 25507,
            "range": "± 1.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500",
            "value": 140339172.5,
            "range": "± 777103.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - B/op",
            "value": 3136,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1",
            "value": 3267,
            "range": "± 156",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - B/op",
            "value": 26616,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - allocs/op",
            "value": 58,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10",
            "value": 24299,
            "range": "± 189",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - B/op",
            "value": 13440,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - allocs/op",
            "value": 33,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5",
            "value": 12753,
            "range": "± 139.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - B/op",
            "value": 654257,
            "range": "± 6.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues",
            "value": 2250645.5,
            "range": "± 49579.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - B/op",
            "value": 96116816,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues",
            "value": 45433474,
            "range": "± 183887",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - B/op",
            "value": 24019024,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues",
            "value": 6470523.5,
            "range": "± 49862.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - B/op",
            "value": 307335586,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - allocs/op",
            "value": 716,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate",
            "value": 74639881,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - B/op",
            "value": 799918396,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - allocs/op",
            "value": 1472,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits",
            "value": 255674371,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - B/op",
            "value": 191994,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - allocs/op",
            "value": 141,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset",
            "value": 637321,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0",
            "value": 1223,
            "range": "± 1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1",
            "value": 1224,
            "range": "± 2.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2",
            "value": 1223,
            "range": "± 2.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3",
            "value": 1224,
            "range": "± 6.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B",
            "value": 406.85,
            "range": "± 2.9",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B",
            "value": 329.5,
            "range": "± 0.8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B",
            "value": 108.8,
            "range": "± 0.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - B/op",
            "value": 41064,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - allocs/op",
            "value": 27,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates",
            "value": 34200.5,
            "range": "± 152",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - B/op",
            "value": 7800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates",
            "value": 3923.5,
            "range": "± 21.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - B/op",
            "value": 20456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - allocs/op",
            "value": 16,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates",
            "value": 12760,
            "range": "± 91.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - B/op",
            "value": 6528,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset",
            "value": 7768,
            "range": "± 251",
            "unit": "ns/op",
            "extra": "10 samples, median"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "name": "Christopher Plieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "committer": {
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "14a5fb7caf1b4c15928c7d4cb50f1d579dbb6ab4",
          "message": "chore(sync): synced file(s) with cplieger/ci (#968)\n\nCo-authored-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>",
          "timestamp": "2026-09-15T11:22:56Z",
          "url": "https://github.com/cplieger/subflux/commit/14a5fb7caf1b4c15928c7d4cb50f1d579dbb6ab4"
        },
        "date": 1789520500730,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "BenchmarkActivityLog_StartEnd - B/op",
            "value": 31,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd",
            "value": 1532.5,
            "range": "± 162",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlign/200 - B/op",
            "value": 1272379,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/200 - allocs/op",
            "value": 5457,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/200",
            "value": 2776615,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/50 - B/op",
            "value": 191569,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/50 - allocs/op",
            "value": 1301,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/50",
            "value": 523283,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/500 - B/op",
            "value": 4653414,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/500 - allocs/op",
            "value": 14076,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/500",
            "value": 9399007,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - B/op",
            "value": 38387762.5,
            "range": "± 2.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500",
            "value": 6299364,
            "range": "± 88074",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - B/op",
            "value": 95985729.5,
            "range": "± 9",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000",
            "value": 32159001.5,
            "range": "± 856192.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - B/op",
            "value": 23986232,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500",
            "value": 4659494,
            "range": "± 107489",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - B/op",
            "value": 163880.5,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - allocs/op",
            "value": 6,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50",
            "value": 507847.5,
            "range": "± 722.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignWithSplits - B/op",
            "value": 374832,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlignWithSplits - allocs/op",
            "value": 54,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlignWithSplits",
            "value": 14489725,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char",
            "value": 382.05,
            "range": "± 1.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty",
            "value": 1.603,
            "range": "± 0.0125",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char",
            "value": 486.6,
            "range": "± 1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char",
            "value": 347.45,
            "range": "± 0.8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - B/op",
            "value": 128056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - allocs/op",
            "value": 8002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues",
            "value": 223361,
            "range": "± 489.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - B/op",
            "value": 12848,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - allocs/op",
            "value": 801,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues",
            "value": 22437,
            "range": "± 45.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - B/op",
            "value": 64056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - allocs/op",
            "value": 4002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues",
            "value": 111725,
            "range": "± 276.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild",
            "value": 29.445,
            "range": "± 0.71",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - B/op",
            "value": 160,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles",
            "value": 685.75,
            "range": "± 1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle",
            "value": 68.935,
            "range": "± 0.415",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - B/op",
            "value": 800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - allocs/op",
            "value": 50,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles",
            "value": 3426.5,
            "range": "± 6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - B/op",
            "value": 328,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1",
            "value": 445.85,
            "range": "± 2.15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - B/op",
            "value": 936,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10",
            "value": 971.25,
            "range": "± 4.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - B/op",
            "value": 648,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5",
            "value": 745.6,
            "range": "± 3.9",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit",
            "value": 67.04,
            "range": "± 0.055",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - B/op",
            "value": 944,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss",
            "value": 505.5,
            "range": "± 3.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup",
            "value": 62.69,
            "range": "± 0.11",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - B/op",
            "value": 7,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent",
            "value": 98.965,
            "range": "± 8.29",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - B/op",
            "value": 1256,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates",
            "value": 12703.5,
            "range": "± 68",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - B/op",
            "value": 2612715,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - allocs/op",
            "value": 49,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues",
            "value": 9984763,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - B/op",
            "value": 384340120,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues",
            "value": 138044552,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - B/op",
            "value": 96047263,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues",
            "value": 18334606,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCountNonText - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText",
            "value": 231.35,
            "range": "± 1.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - B/op",
            "value": 10531,
            "range": "± 5314",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000",
            "value": 3975009.5,
            "range": "± 20639.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - B/op",
            "value": 228962.5,
            "range": "± 128403.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000",
            "value": 21262140,
            "range": "± 794000.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - B/op",
            "value": 2568,
            "range": "± 1264.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000",
            "value": 1884155,
            "range": "± 8743.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - B/op",
            "value": 105,
            "range": "± 8",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000",
            "value": 110642.5,
            "range": "± 1738",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - B/op",
            "value": 9952,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100",
            "value": 8524.5,
            "range": "± 37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - B/op",
            "value": 76384,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000",
            "value": 316336.5,
            "range": "± 599.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - B/op",
            "value": 40928,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500",
            "value": 133218.5,
            "range": "± 292",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode",
            "value": 28.625,
            "range": "± 0.125",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024",
            "value": 16200.5,
            "range": "± 68",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256",
            "value": 3377,
            "range": "± 8.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096",
            "value": 81051,
            "range": "± 160",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - B/op",
            "value": 272,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10",
            "value": 390.8,
            "range": "± 3.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - B/op",
            "value": 6832,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - allocs/op",
            "value": 89,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200",
            "value": 6586,
            "range": "± 44.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - B/op",
            "value": 1456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - allocs/op",
            "value": 21,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50",
            "value": 1506.5,
            "range": "± 5.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - B/op",
            "value": 5296,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - allocs/op",
            "value": 100,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles",
            "value": 7366,
            "range": "± 43",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - B/op",
            "value": 5080,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10",
            "value": 1524.5,
            "range": "± 66.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - B/op",
            "value": 93008,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - allocs/op",
            "value": 209,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200",
            "value": 23267.5,
            "range": "± 211.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - B/op",
            "value": 21496,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50",
            "value": 5769.5,
            "range": "± 105.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - B/op",
            "value": 5360,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10",
            "value": 3188,
            "range": "± 121",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - B/op",
            "value": 46259,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - allocs/op",
            "value": 109,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100",
            "value": 28238,
            "range": "± 1083.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - B/op",
            "value": 22896,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50",
            "value": 14316,
            "range": "± 176.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - B/op",
            "value": 115182.5,
            "range": "± 12",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - allocs/op",
            "value": 1005,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler",
            "value": 132185.5,
            "range": "± 1447.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired",
            "value": 461.65,
            "range": "± 2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides",
            "value": 406.75,
            "range": "± 0.75",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides",
            "value": 409.6,
            "range": "± 0.6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit",
            "value": 23.47,
            "range": "± 0.44",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown",
            "value": 258.25,
            "range": "± 1.8",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - B/op",
            "value": 25724,
            "range": "± 14",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - allocs/op",
            "value": 309,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath",
            "value": 361021,
            "range": "± 148235",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - B/op",
            "value": 98188.5,
            "range": "± 242",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - allocs/op",
            "value": 1052,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024",
            "value": 349777.5,
            "range": "± 779",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - B/op",
            "value": 11281.5,
            "range": "± 12.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - allocs/op",
            "value": 150,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128",
            "value": 35978.5,
            "range": "± 169",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - B/op",
            "value": 23632.5,
            "range": "± 16.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - allocs/op",
            "value": 280,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256",
            "value": 76244,
            "range": "± 208.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - B/op",
            "value": 48386.5,
            "range": "± 48",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512",
            "value": 163504.5,
            "range": "± 605",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - B/op",
            "value": 5617.5,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - allocs/op",
            "value": 84,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64",
            "value": 17459,
            "range": "± 80.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - B/op",
            "value": 41221.5,
            "range": "± 170",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - allocs/op",
            "value": 254,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024",
            "value": 2157919,
            "range": "± 6651.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - B/op",
            "value": 5116,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - allocs/op",
            "value": 47,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128",
            "value": 222673,
            "range": "± 629.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - B/op",
            "value": 10054,
            "range": "± 15",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - allocs/op",
            "value": 77,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256",
            "value": 483716.5,
            "range": "± 2379.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - B/op",
            "value": 20155,
            "range": "± 48.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - allocs/op",
            "value": 137,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512",
            "value": 1037304.5,
            "range": "± 5018",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - B/op",
            "value": 2782,
            "range": "± 6",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - allocs/op",
            "value": 31,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64",
            "value": 98814.5,
            "range": "± 661.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - B/op",
            "value": 504,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024",
            "value": 40814,
            "range": "± 906",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - B/op",
            "value": 499,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128",
            "value": 6057,
            "range": "± 18.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - B/op",
            "value": 500,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256",
            "value": 11016.5,
            "range": "± 24",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - B/op",
            "value": 502,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512",
            "value": 20950,
            "range": "± 119",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - B/op",
            "value": 499,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64",
            "value": 3506,
            "range": "± 18.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - B/op",
            "value": 53851,
            "range": "± 39",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - allocs/op",
            "value": 705,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024",
            "value": 183220,
            "range": "± 349",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - B/op",
            "value": 9932.5,
            "range": "± 8.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - allocs/op",
            "value": 105,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128",
            "value": 23107.5,
            "range": "± 103.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - B/op",
            "value": 20896,
            "range": "± 15.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - allocs/op",
            "value": 191,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256",
            "value": 45292.5,
            "range": "± 70",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - B/op",
            "value": 42931,
            "range": "± 21.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - allocs/op",
            "value": 365,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512",
            "value": 94638,
            "range": "± 195.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - B/op",
            "value": 4899.5,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - allocs/op",
            "value": 59,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64",
            "value": 11886,
            "range": "± 21.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - B/op",
            "value": 6709.5,
            "range": "± 13",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - allocs/op",
            "value": 110,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName",
            "value": 422470.5,
            "range": "± 1368.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - B/op",
            "value": 195376,
            "range": "± 159",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - allocs/op",
            "value": 1516,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT",
            "value": 96250.5,
            "range": "± 1024.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - B/op",
            "value": 317760.5,
            "range": "± 441",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - allocs/op",
            "value": 8402,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess",
            "value": 861683,
            "range": "± 2511.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes",
            "value": 2.25,
            "range": "± 0.014",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - B/op",
            "value": 17736844,
            "range": "± 22",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - allocs/op",
            "value": 324034,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current",
            "value": 21694631.5,
            "range": "± 571635",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - B/op",
            "value": 1200563.5,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - allocs/op",
            "value": 24024,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current",
            "value": 1360920.5,
            "range": "± 10704",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - B/op",
            "value": 14364023.5,
            "range": "± 20",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - allocs/op",
            "value": 348165,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current",
            "value": 11579117,
            "range": "± 58525",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - B/op",
            "value": 23,
            "range": "± 110",
            "unit": "B/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - allocs/op",
            "value": 1,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider",
            "value": 363.6,
            "range": "± 374.05",
            "unit": "ns/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider",
            "value": 268.1,
            "range": "± 2.9",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - B/op",
            "value": 80602,
            "range": "± 14.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1",
            "value": 100786.5,
            "range": "± 815",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - B/op",
            "value": 236129,
            "range": "± 49",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - allocs/op",
            "value": 1892,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20",
            "value": 214104,
            "range": "± 5138.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - B/op",
            "value": 108210.5,
            "range": "± 25.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - allocs/op",
            "value": 823,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5",
            "value": 121522,
            "range": "± 2138.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules",
            "value": 70.97,
            "range": "± 2.855",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules",
            "value": 75.03,
            "range": "± 0.63",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules",
            "value": 70.875,
            "range": "± 0.37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1",
            "value": 10.02,
            "range": "± 0.17",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2",
            "value": 10.01,
            "range": "± 0.045",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3",
            "value": 10.065,
            "range": "± 0.06",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1",
            "value": 3.428,
            "range": "± 0.0445",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2",
            "value": 3.4515,
            "range": "± 0.054",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3",
            "value": 3.455,
            "range": "± 0.0535",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release",
            "value": 24.19,
            "range": "± 0.31",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable",
            "value": 7.0015,
            "range": "± 0.0115",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match",
            "value": 16.575,
            "range": "± 0.04",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only",
            "value": 17.02,
            "range": "± 0.465",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10",
            "value": 173.9,
            "range": "± 0.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100",
            "value": 1930.5,
            "range": "± 41.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50",
            "value": 926.75,
            "range": "± 16.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel",
            "value": 22.325,
            "range": "± 7.37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - B/op",
            "value": 32292.5,
            "range": "± 120",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - allocs/op",
            "value": 511,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10",
            "value": 1906853.5,
            "range": "± 3386",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - B/op",
            "value": 322590,
            "range": "± 926",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - allocs/op",
            "value": 5102,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100",
            "value": 19080323.5,
            "range": "± 31666.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - B/op",
            "value": 1612475.5,
            "range": "± 6248",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - allocs/op",
            "value": 25506.5,
            "range": "± 1.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500",
            "value": 95420758.5,
            "range": "± 151183.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - B/op",
            "value": 3136,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1",
            "value": 2711.5,
            "range": "± 89.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - B/op",
            "value": 26616,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - allocs/op",
            "value": 58,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10",
            "value": 19624.5,
            "range": "± 195.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - B/op",
            "value": 13440,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - allocs/op",
            "value": 33,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5",
            "value": 10640.5,
            "range": "± 145",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - B/op",
            "value": 654258,
            "range": "± 6.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues",
            "value": 2151084,
            "range": "± 37227",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - B/op",
            "value": 96116819,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues",
            "value": 32013801.5,
            "range": "± 1219053.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - B/op",
            "value": 24019024,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues",
            "value": 4607100,
            "range": "± 76732.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - B/op",
            "value": 307335571,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - allocs/op",
            "value": 716,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate",
            "value": 58255859,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - B/op",
            "value": 799918360,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - allocs/op",
            "value": 1472,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits",
            "value": 198614931,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - B/op",
            "value": 192011,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - allocs/op",
            "value": 141,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset",
            "value": 585966,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0",
            "value": 842.4,
            "range": "± 3.3",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1",
            "value": 842.5,
            "range": "± 1.3",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2",
            "value": 842.15,
            "range": "± 2.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3",
            "value": 843.45,
            "range": "± 2.65",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B",
            "value": 349.35,
            "range": "± 0.55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B",
            "value": 286.45,
            "range": "± 0.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B",
            "value": 86.915,
            "range": "± 1.06",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - B/op",
            "value": 41064,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - allocs/op",
            "value": 27,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates",
            "value": 29428,
            "range": "± 101.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - B/op",
            "value": 7800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates",
            "value": 3577.5,
            "range": "± 52.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - B/op",
            "value": 20456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - allocs/op",
            "value": 16,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates",
            "value": 11527.5,
            "range": "± 120",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - B/op",
            "value": 6528,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset",
            "value": 6749.5,
            "range": "± 168",
            "unit": "ns/op",
            "extra": "10 samples, median"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "name": "Christopher Plieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "committer": {
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "5e1f5660476f9543d343911dd5aeab48cc2aab16",
          "message": "chore(sync): synced file(s) with cplieger/ci (#969)\n\nCo-authored-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>",
          "timestamp": "2026-09-16T10:16:36Z",
          "url": "https://github.com/cplieger/subflux/commit/5e1f5660476f9543d343911dd5aeab48cc2aab16"
        },
        "date": 1789570366831,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "BenchmarkActivityLog_StartEnd - B/op",
            "value": 31,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd",
            "value": 1989.5,
            "range": "± 222",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlign/200 - B/op",
            "value": 1268875,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/200 - allocs/op",
            "value": 5456,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/200",
            "value": 2456259,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/50 - B/op",
            "value": 191313,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/50 - allocs/op",
            "value": 1301,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/50",
            "value": 479612,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/500 - B/op",
            "value": 4651132,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/500 - allocs/op",
            "value": 14075,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/500",
            "value": 8058733,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - B/op",
            "value": 38387768,
            "range": "± 6",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500",
            "value": 5963179,
            "range": "± 84617.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - B/op",
            "value": 95985732.5,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000",
            "value": 32311224,
            "range": "± 151358",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - B/op",
            "value": 23986232.5,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500",
            "value": 4532842,
            "range": "± 86140",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - B/op",
            "value": 163881,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - allocs/op",
            "value": 6,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50",
            "value": 400371,
            "range": "± 1912.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignWithSplits - B/op",
            "value": 374822,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlignWithSplits - allocs/op",
            "value": 53,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlignWithSplits",
            "value": 19412975,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char",
            "value": 416.1,
            "range": "± 3.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty",
            "value": 1.9115,
            "range": "± 0.027",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char",
            "value": 494.45,
            "range": "± 3.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char",
            "value": 302.4,
            "range": "± 3.9",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - B/op",
            "value": 128056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - allocs/op",
            "value": 8002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues",
            "value": 223858,
            "range": "± 1193.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - B/op",
            "value": 12848,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - allocs/op",
            "value": 801,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues",
            "value": 22486.5,
            "range": "± 144.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - B/op",
            "value": 64056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - allocs/op",
            "value": 4002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues",
            "value": 112240,
            "range": "± 1537.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild",
            "value": 27.79,
            "range": "± 1.075",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - B/op",
            "value": 160,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles",
            "value": 991.05,
            "range": "± 57.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle",
            "value": 100.35,
            "range": "± 4.63",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - B/op",
            "value": 800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - allocs/op",
            "value": 50,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles",
            "value": 4927,
            "range": "± 79.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - B/op",
            "value": 328,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1",
            "value": 390.05,
            "range": "± 4.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - B/op",
            "value": 936,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10",
            "value": 878.1,
            "range": "± 18.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - B/op",
            "value": 648,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5",
            "value": 650.2,
            "range": "± 3.15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit",
            "value": 66.12,
            "range": "± 0.08",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - B/op",
            "value": 944,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss",
            "value": 429,
            "range": "± 4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup",
            "value": 64.705,
            "range": "± 0.23",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - B/op",
            "value": 7,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent",
            "value": 60.375,
            "range": "± 4.07",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - B/op",
            "value": 1256,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates",
            "value": 14886.5,
            "range": "± 402",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - B/op",
            "value": 2612673,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - allocs/op",
            "value": 49,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues",
            "value": 8709013,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - B/op",
            "value": 384340134,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues",
            "value": 129408692,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - B/op",
            "value": 96047257,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues",
            "value": 18900156,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCountNonText - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText",
            "value": 298.15,
            "range": "± 6.7",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - B/op",
            "value": 8932,
            "range": "± 269",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000",
            "value": 3340646,
            "range": "± 99735",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - B/op",
            "value": 167876.5,
            "range": "± 3495.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000",
            "value": 15746576,
            "range": "± 71467.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - B/op",
            "value": 2118,
            "range": "± 1027",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000",
            "value": 1543157,
            "range": "± 20066",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - B/op",
            "value": 105,
            "range": "± 5.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000",
            "value": 107112,
            "range": "± 3037",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - B/op",
            "value": 9952,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100",
            "value": 6645.5,
            "range": "± 87",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - B/op",
            "value": 76384,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000",
            "value": 250570.5,
            "range": "± 12320",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - B/op",
            "value": 40928,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500",
            "value": 100888.5,
            "range": "± 716.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode",
            "value": 26.985,
            "range": "± 0.305",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024",
            "value": 13642.5,
            "range": "± 23.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256",
            "value": 2931,
            "range": "± 4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096",
            "value": 65713.5,
            "range": "± 367.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - B/op",
            "value": 272,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10",
            "value": 360.2,
            "range": "± 4.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - B/op",
            "value": 6832,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - allocs/op",
            "value": 89,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200",
            "value": 5803,
            "range": "± 115",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - B/op",
            "value": 1456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - allocs/op",
            "value": 21,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50",
            "value": 1309.5,
            "range": "± 13.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - B/op",
            "value": 5296,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - allocs/op",
            "value": 100,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles",
            "value": 6930,
            "range": "± 35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - B/op",
            "value": 5080,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10",
            "value": 1291,
            "range": "± 81",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - B/op",
            "value": 93008,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - allocs/op",
            "value": 209,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200",
            "value": 20017,
            "range": "± 324.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - B/op",
            "value": 21496,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50",
            "value": 5015.5,
            "range": "± 36.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - B/op",
            "value": 5360,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10",
            "value": 2705.5,
            "range": "± 142.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - B/op",
            "value": 46259,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - allocs/op",
            "value": 109,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100",
            "value": 25159.5,
            "range": "± 649.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - B/op",
            "value": 22896,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50",
            "value": 12361.5,
            "range": "± 105.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - B/op",
            "value": 115184.5,
            "range": "± 19.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - allocs/op",
            "value": 1005,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler",
            "value": 141654.5,
            "range": "± 2159",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired",
            "value": 403.4,
            "range": "± 2.15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides",
            "value": 435.1,
            "range": "± 20.3",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides",
            "value": 440.8,
            "range": "± 22.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit",
            "value": 20.655,
            "range": "± 0.56",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown",
            "value": 230.8,
            "range": "± 3.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - B/op",
            "value": 25724,
            "range": "± 8",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - allocs/op",
            "value": 309,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath",
            "value": 399219.5,
            "range": "± 154361.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - B/op",
            "value": 98061,
            "range": "± 149.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - allocs/op",
            "value": 1052,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024",
            "value": 393159,
            "range": "± 6373.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - B/op",
            "value": 11275.5,
            "range": "± 11",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - allocs/op",
            "value": 150,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128",
            "value": 38445.5,
            "range": "± 350.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - B/op",
            "value": 23607,
            "range": "± 31",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - allocs/op",
            "value": 280,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256",
            "value": 84202,
            "range": "± 2194.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - B/op",
            "value": 48319,
            "range": "± 45",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512",
            "value": 180835.5,
            "range": "± 3576.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - B/op",
            "value": 5608,
            "range": "± 6",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - allocs/op",
            "value": 84,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64",
            "value": 17928.5,
            "range": "± 545",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - B/op",
            "value": 41299,
            "range": "± 147",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - allocs/op",
            "value": 254,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024",
            "value": 2301962,
            "range": "± 19870.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - B/op",
            "value": 5124,
            "range": "± 11",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - allocs/op",
            "value": 47,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128",
            "value": 231923,
            "range": "± 4957",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - B/op",
            "value": 10055,
            "range": "± 16",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - allocs/op",
            "value": 77,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256",
            "value": 506330,
            "range": "± 9724",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - B/op",
            "value": 20157.5,
            "range": "± 34.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - allocs/op",
            "value": 137,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512",
            "value": 1098534,
            "range": "± 15029",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - B/op",
            "value": 2778,
            "range": "± 7.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - allocs/op",
            "value": 31,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64",
            "value": 101476,
            "range": "± 2339.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - B/op",
            "value": 506,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024",
            "value": 44458,
            "range": "± 860.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - B/op",
            "value": 499,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128",
            "value": 6245.5,
            "range": "± 84.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - B/op",
            "value": 500,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256",
            "value": 11691,
            "range": "± 112.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - B/op",
            "value": 501.5,
            "range": "± 2.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512",
            "value": 22552.5,
            "range": "± 213",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - B/op",
            "value": 499,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64",
            "value": 3549.5,
            "range": "± 21",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - B/op",
            "value": 53805.5,
            "range": "± 48",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - allocs/op",
            "value": 705,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024",
            "value": 179473.5,
            "range": "± 1895.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - B/op",
            "value": 9919.5,
            "range": "± 12",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - allocs/op",
            "value": 105,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128",
            "value": 20813,
            "range": "± 236.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - B/op",
            "value": 20879.5,
            "range": "± 15",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - allocs/op",
            "value": 191,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256",
            "value": 40545,
            "range": "± 204",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - B/op",
            "value": 42869,
            "range": "± 43",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - allocs/op",
            "value": 365,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512",
            "value": 86877.5,
            "range": "± 1191",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - B/op",
            "value": 4897.5,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - allocs/op",
            "value": 59,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64",
            "value": 10559.5,
            "range": "± 70",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - B/op",
            "value": 6716,
            "range": "± 26",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - allocs/op",
            "value": 110,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName",
            "value": 412618,
            "range": "± 3840.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - B/op",
            "value": 195019.5,
            "range": "± 118",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - allocs/op",
            "value": 1516,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT",
            "value": 86314,
            "range": "± 1813",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - B/op",
            "value": 317170,
            "range": "± 455.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - allocs/op",
            "value": 8402,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess",
            "value": 808222,
            "range": "± 5196",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes",
            "value": 3.55,
            "range": "± 0.0205",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - B/op",
            "value": 17736840.5,
            "range": "± 19.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - allocs/op",
            "value": 324034,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current",
            "value": 21731884.5,
            "range": "± 399015",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - B/op",
            "value": 1200564,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - allocs/op",
            "value": 24024,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current",
            "value": 1295559,
            "range": "± 38047.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - B/op",
            "value": 14364015.5,
            "range": "± 30",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - allocs/op",
            "value": 348165,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current",
            "value": 11295220,
            "range": "± 830313",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - B/op",
            "value": 23,
            "range": "± 6.5",
            "unit": "B/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - allocs/op",
            "value": 1,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider",
            "value": 398.3,
            "range": "± 52.45",
            "unit": "ns/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider",
            "value": 142.9,
            "range": "± 2.3",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - B/op",
            "value": 80563.5,
            "range": "± 27",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1",
            "value": 114173.5,
            "range": "± 1799.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - B/op",
            "value": 236126,
            "range": "± 18",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - allocs/op",
            "value": 1892,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20",
            "value": 212002.5,
            "range": "± 2150.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - B/op",
            "value": 108192.5,
            "range": "± 16.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - allocs/op",
            "value": 823,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5",
            "value": 131471,
            "range": "± 1583",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules",
            "value": 66.9,
            "range": "± 3.76",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules",
            "value": 74.62,
            "range": "± 0.37",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules",
            "value": 66.61,
            "range": "± 0.475",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1",
            "value": 9.9445,
            "range": "± 0.291",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2",
            "value": 9.9545,
            "range": "± 0.092",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3",
            "value": 9.9795,
            "range": "± 0.092",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1",
            "value": 3.2845,
            "range": "± 0.032",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2",
            "value": 3.2805,
            "range": "± 0.0295",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3",
            "value": 3.28,
            "range": "± 0.0215",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release",
            "value": 35.805,
            "range": "± 0.17",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable",
            "value": 8.1275,
            "range": "± 0.1345",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match",
            "value": 23.48,
            "range": "± 0.075",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only",
            "value": 23.76,
            "range": "± 0.01",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10",
            "value": 243.45,
            "range": "± 3.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100",
            "value": 2407.5,
            "range": "± 16",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50",
            "value": 1207.5,
            "range": "± 15",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel",
            "value": 15.185,
            "range": "± 2.13",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - B/op",
            "value": 32202.5,
            "range": "± 150",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - allocs/op",
            "value": 511,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10",
            "value": 1894853,
            "range": "± 44307.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - B/op",
            "value": 323108,
            "range": "± 1468.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - allocs/op",
            "value": 5102,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100",
            "value": 18851021,
            "range": "± 198390",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - B/op",
            "value": 1610930,
            "range": "± 6255",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - allocs/op",
            "value": 25506,
            "range": "± 1.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500",
            "value": 94722416,
            "range": "± 693065.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - B/op",
            "value": 3136,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1",
            "value": 2361.5,
            "range": "± 80.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - B/op",
            "value": 26616,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - allocs/op",
            "value": 58,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10",
            "value": 17352.5,
            "range": "± 136",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - B/op",
            "value": 13440,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - allocs/op",
            "value": 33,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5",
            "value": 9061,
            "range": "± 123.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - B/op",
            "value": 654259,
            "range": "± 5.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues",
            "value": 1871628,
            "range": "± 19214.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - B/op",
            "value": 96116819,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues",
            "value": 32395438,
            "range": "± 416259",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - B/op",
            "value": 24019024,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues",
            "value": 4540444.5,
            "range": "± 69944",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - B/op",
            "value": 307335562,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - allocs/op",
            "value": 716,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate",
            "value": 57184546,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - B/op",
            "value": 799918369,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - allocs/op",
            "value": 1472,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits",
            "value": 245464283,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - B/op",
            "value": 191997,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - allocs/op",
            "value": 141,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset",
            "value": 512361,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0",
            "value": 943.2,
            "range": "± 2.75",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1",
            "value": 942.65,
            "range": "± 1.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2",
            "value": 942.75,
            "range": "± 1.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3",
            "value": 943.1,
            "range": "± 1.25",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B",
            "value": 338.3,
            "range": "± 3.05",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B",
            "value": 276.65,
            "range": "± 1.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B",
            "value": 72.9,
            "range": "± 0.34",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - B/op",
            "value": 41064,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - allocs/op",
            "value": 27,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates",
            "value": 28198.5,
            "range": "± 189.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - B/op",
            "value": 7800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates",
            "value": 3139.5,
            "range": "± 43.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - B/op",
            "value": 20456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - allocs/op",
            "value": 16,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates",
            "value": 10310.5,
            "range": "± 58.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - B/op",
            "value": 6528,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset",
            "value": 6059.5,
            "range": "± 310.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "name": "Christopher Plieger",
            "username": "cplieger",
            "email": "917744+cplieger@users.noreply.github.com"
          },
          "committer": {
            "name": "GitHub",
            "username": "web-flow",
            "email": "noreply@github.com"
          },
          "id": "f9577db6c2f2096d9cc325c89450a48686d66346",
          "message": "chore(deps): update cplieger/ci digest to aa0a018 (#649)",
          "timestamp": "2026-09-20T08:02:03Z",
          "url": "https://github.com/cplieger/ci/commit/f9577db6c2f2096d9cc325c89450a48686d66346"
        },
        "date": 1790125870346,
        "tool": "customSmallerIsBetter",
        "benches": [
          {
            "name": "BenchmarkActivityLog_StartEnd - B/op",
            "value": 31,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkActivityLog_StartEnd",
            "value": 1306.5,
            "range": "± 85.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlign/200 - B/op",
            "value": 1272191,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/200 - allocs/op",
            "value": 5457,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/200",
            "value": 2315562,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/50 - B/op",
            "value": 191473,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/50 - allocs/op",
            "value": 1301,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/50",
            "value": 417191,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlign/500 - B/op",
            "value": 4653825,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlign/500 - allocs/op",
            "value": 14076,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlign/500",
            "value": 7845936,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - B/op",
            "value": 38387763.5,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/asymmetric_100x1500",
            "value": 8100642,
            "range": "± 2462771",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - B/op",
            "value": 95985728,
            "range": "± 4.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/large_2000",
            "value": 88034529,
            "range": "± 4797981.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - B/op",
            "value": 23986233.5,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500 - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/medium_500",
            "value": 8844564,
            "range": "± 1584755.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - B/op",
            "value": 163880.5,
            "range": "± 1",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50 - allocs/op",
            "value": 6,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignConstantOffset/small_50",
            "value": 424374,
            "range": "± 5508.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlignWithSplits - B/op",
            "value": 374932,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkAlignWithSplits - allocs/op",
            "value": 54,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkAlignWithSplits",
            "value": 11841533,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/already_2char",
            "value": 335.8,
            "range": "± 16.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/empty",
            "value": 1.177,
            "range": "± 0.0575",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/known_3char",
            "value": 429.35,
            "range": "± 18.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAlpha2FromAlpha3/unknown_3char",
            "value": 294.7,
            "range": "± 27.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - B/op",
            "value": 128056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues - allocs/op",
            "value": 8002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/1000_cues",
            "value": 182068.5,
            "range": "± 13942.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - B/op",
            "value": 12848,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues - allocs/op",
            "value": 801,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/100_cues",
            "value": 18134.5,
            "range": "± 231.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - B/op",
            "value": 64056,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues - allocs/op",
            "value": 4002,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkAudioSync/500_cues",
            "value": 90938.5,
            "range": "± 5976.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuild",
            "value": 24.78,
            "range": "± 1.565",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - B/op",
            "value": 160,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/10_subtitles",
            "value": 587.85,
            "range": "± 50.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/1_subtitle",
            "value": 59.3,
            "range": "± 0.765",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - B/op",
            "value": 800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles - allocs/op",
            "value": 50,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildMatches/50_subtitles",
            "value": 2941.5,
            "range": "± 150.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - B/op",
            "value": 328,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=1",
            "value": 369.3,
            "range": "± 4.55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - B/op",
            "value": 936,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=10",
            "value": 820.8,
            "range": "± 2.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - B/op",
            "value": 648,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkBuildSearchKey/providers=5",
            "value": 617.6,
            "range": "± 15.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/hit",
            "value": 57.555,
            "range": "± 2.23",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - B/op",
            "value": 944,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_GetOrFetch/miss",
            "value": 425.85,
            "range": "± 12.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_Lookup",
            "value": 53.73,
            "range": "± 2.395",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - B/op",
            "value": 7,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCache_concurrent",
            "value": 96.86,
            "range": "± 5.845",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - B/op",
            "value": 1256,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkClusterCandidates",
            "value": 10845.5,
            "range": "± 370",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - B/op",
            "value": 2612621,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues - allocs/op",
            "value": 48,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/100_cues",
            "value": 8976606,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - B/op",
            "value": 384340157,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/2000_cues",
            "value": 373129873,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - B/op",
            "value": 96047256,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues - allocs/op",
            "value": 61,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkCorrectFramerate/500_cues",
            "value": 38219540,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkCountNonText - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCountNonText",
            "value": 200.05,
            "range": "± 12.95",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - B/op",
            "value": 8827,
            "range": "± 5043",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Asymmetric_5000x50000",
            "value": 3337766,
            "range": "± 234795.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - B/op",
            "value": 160405,
            "range": "± 89878.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Long_100000",
            "value": 15349180,
            "range": "± 914885",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - B/op",
            "value": 2212.5,
            "range": "± 1170.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Medium_10000",
            "value": 1583744,
            "range": "± 104303.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - B/op",
            "value": 96,
            "range": "± 3.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000 - allocs/op",
            "value": 5,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkCrossCorrelateEdges/Short_1000",
            "value": 92165.5,
            "range": "± 1316.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - B/op",
            "value": 5856,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/100",
            "value": 7014,
            "range": "± 116.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - B/op",
            "value": 76384,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/1000",
            "value": 270981,
            "range": "± 32112",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - B/op",
            "value": 40928,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500 - allocs/op",
            "value": 12,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkDPAlign/500",
            "value": 114702,
            "range": "± 11754",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - B/op",
            "value": 24,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkEpisode",
            "value": 23.59,
            "range": "± 0.715",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/1024",
            "value": 13006,
            "range": "± 34",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/256",
            "value": 2732.5,
            "range": "± 45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFFT/4096",
            "value": 65531,
            "range": "± 2566",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - B/op",
            "value": 272,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10 - allocs/op",
            "value": 7,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_10",
            "value": 331.1,
            "range": "± 49.1",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - B/op",
            "value": 6832,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200 - allocs/op",
            "value": 89,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_200",
            "value": 5618,
            "range": "± 207",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - B/op",
            "value": 1456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50 - allocs/op",
            "value": 21,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity/subs_50",
            "value": 1233.5,
            "range": "± 46",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - B/op",
            "value": 5296,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles - allocs/op",
            "value": 100,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterByIdentity_manyTitles",
            "value": 6210.5,
            "range": "± 326",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - B/op",
            "value": 5080,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=10",
            "value": 1426,
            "range": "± 65.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - B/op",
            "value": 93008,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200 - allocs/op",
            "value": 209,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=200",
            "value": 21425.5,
            "range": "± 1054.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - B/op",
            "value": 21496,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSearchResults/n=50",
            "value": 5400,
            "range": "± 189",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - B/op",
            "value": 5360,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10 - allocs/op",
            "value": 15,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=10",
            "value": 2981,
            "range": "± 121.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - B/op",
            "value": 46259,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100 - allocs/op",
            "value": 109,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=100",
            "value": 23820.5,
            "range": "± 1349",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - B/op",
            "value": 22896,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50 - allocs/op",
            "value": 57,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkFilterSubtitleData/items=50",
            "value": 12268.5,
            "range": "± 389",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - B/op",
            "value": 115177.5,
            "range": "± 26",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler - allocs/op",
            "value": 1005,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkHandler",
            "value": 116246.5,
            "range": "± 8787",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - B/op",
            "value": 16,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkIsHearingImpaired",
            "value": 392.6,
            "range": "± 27.35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_no_overrides",
            "value": 353.35,
            "range": "± 6.35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - B/op",
            "value": 228,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides - allocs/op",
            "value": 3,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/known_with_overrides",
            "value": 358.3,
            "range": "± 15.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/override_hit",
            "value": 17.135,
            "range": "± 0.98",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - B/op",
            "value": 240,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown - allocs/op",
            "value": 4,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkLookupLangName/unknown",
            "value": 212.25,
            "range": "± 14.85",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - B/op",
            "value": 25723.5,
            "range": "± 2",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath - allocs/op",
            "value": 309,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkOpenFastPath",
            "value": 53485,
            "range": "± 28790",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - B/op",
            "value": 98210.5,
            "range": "± 241.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024 - allocs/op",
            "value": 1052,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=1024",
            "value": 301480.5,
            "range": "± 22147.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - B/op",
            "value": 11278.5,
            "range": "± 12",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128 - allocs/op",
            "value": 150,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=128",
            "value": 30624,
            "range": "± 119.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - B/op",
            "value": 23633.5,
            "range": "± 23",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256 - allocs/op",
            "value": 280,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=256",
            "value": 64791.5,
            "range": "± 1266",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - B/op",
            "value": 48420,
            "range": "± 43",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=512",
            "value": 140051.5,
            "range": "± 11922.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - B/op",
            "value": 5615,
            "range": "± 5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64 - allocs/op",
            "value": 84,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/assertion_scan_cost/n=64",
            "value": 14849.5,
            "range": "± 383.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - B/op",
            "value": 41267,
            "range": "± 82",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024 - allocs/op",
            "value": 254,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=1024",
            "value": 1779772,
            "range": "± 55373",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - B/op",
            "value": 5121,
            "range": "± 10",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128 - allocs/op",
            "value": 47,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=128",
            "value": 182549,
            "range": "± 7608.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - B/op",
            "value": 10051.5,
            "range": "± 25",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256 - allocs/op",
            "value": 77,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=256",
            "value": 400393,
            "range": "± 13192",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - B/op",
            "value": 20149,
            "range": "± 14",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512 - allocs/op",
            "value": 137,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=512",
            "value": 851260.5,
            "range": "± 23908.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - B/op",
            "value": 2779,
            "range": "± 5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64 - allocs/op",
            "value": 31,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/flagship_release_group/n=64",
            "value": 80637.5,
            "range": "± 2741.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - B/op",
            "value": 504,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=1024",
            "value": 31494,
            "range": "± 596",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - B/op",
            "value": 499,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=128",
            "value": 4801,
            "range": "± 383",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - B/op",
            "value": 500,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=256",
            "value": 8520,
            "range": "± 696",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - B/op",
            "value": 500,
            "range": "± 3",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=512",
            "value": 16307,
            "range": "± 1088",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - B/op",
            "value": 499,
            "range": "± 0.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64 - allocs/op",
            "value": 9,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/retry_worst_case/n=64",
            "value": 2767.5,
            "range": "± 94",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - B/op",
            "value": 53856,
            "range": "± 37.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024 - allocs/op",
            "value": 705,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=1024",
            "value": 152496.5,
            "range": "± 9905",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - B/op",
            "value": 9935,
            "range": "± 6",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128 - allocs/op",
            "value": 105,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=128",
            "value": 18939.5,
            "range": "± 1315",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - B/op",
            "value": 20899,
            "range": "± 13.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256 - allocs/op",
            "value": 191,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=256",
            "value": 37197,
            "range": "± 1754",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - B/op",
            "value": 42935,
            "range": "± 23",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512 - allocs/op",
            "value": 365,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=512",
            "value": 76324,
            "range": "± 3412.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - B/op",
            "value": 4902.5,
            "range": "± 3",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64 - allocs/op",
            "value": 59,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPCREScaling/witness_nested_branch/n=64",
            "value": 9784,
            "range": "± 835",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - B/op",
            "value": 6706.5,
            "range": "± 19",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName - allocs/op",
            "value": 110,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseName",
            "value": 356255,
            "range": "± 24755.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - B/op",
            "value": 195325.5,
            "range": "± 82.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT - allocs/op",
            "value": 1516,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkParseSRT",
            "value": 80618.5,
            "range": "± 1577.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - B/op",
            "value": 317605.5,
            "range": "± 395.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess - allocs/op",
            "value": 8402,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcess",
            "value": 720292.5,
            "range": "± 58665",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkPostProcessBytes",
            "value": 1.9595,
            "range": "± 0.093",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - B/op",
            "value": 17736845.5,
            "range": "± 18",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current - allocs/op",
            "value": 324034,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/episodes/current",
            "value": 18844922,
            "range": "± 1026838.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - B/op",
            "value": 1200564,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current - allocs/op",
            "value": 24024,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/HistoryMediaIDs/movies/current",
            "value": 1204166.5,
            "range": "± 24178",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - B/op",
            "value": 14363998.5,
            "range": "± 27",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current - allocs/op",
            "value": 348165,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkQuadIndexQueriesAtScale/ManualLocks/current",
            "value": 10017017,
            "range": "± 508368",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - B/op",
            "value": 23,
            "range": "± 121.5",
            "unit": "B/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider - allocs/op",
            "value": 1,
            "range": "± 0.5",
            "unit": "allocs/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/new_provider",
            "value": 467.8,
            "range": "± 334.3",
            "unit": "ns/op",
            "extra": "9 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRecordSearch/registered_provider",
            "value": 440.45,
            "range": "± 16.45",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - B/op",
            "value": 80596.5,
            "range": "± 20",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1 - allocs/op",
            "value": 538,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_1",
            "value": 92743,
            "range": "± 5416",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - B/op",
            "value": 236111.5,
            "range": "± 35.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20 - allocs/op",
            "value": 1892,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_20",
            "value": 192832,
            "range": "± 6158",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - B/op",
            "value": 108204.5,
            "range": "± 15",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5 - allocs/op",
            "value": 823,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRender/providers_5",
            "value": 110180.5,
            "range": "± 9906.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/1_rules",
            "value": 62.365,
            "range": "± 2.39",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/20_rules",
            "value": 68.92,
            "range": "± 3.19",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - B/op",
            "value": 128,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules - allocs/op",
            "value": 2,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkResolveTargetsWithFallback/5_rules",
            "value": 62.06,
            "range": "± 1.085",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=1",
            "value": 7.855,
            "range": "± 0.55",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=2",
            "value": 7.8085,
            "range": "± 0.3205",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - B/op",
            "value": 2,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3 - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Download/attempts=3",
            "value": 7.818,
            "range": "± 0.2615",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=1",
            "value": 2.625,
            "range": "± 0.1055",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=2",
            "value": 2.622,
            "range": "± 0.1745",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkRetryProvider/Search/attempts=3",
            "value": 2.6,
            "range": "± 0.22",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/full_release",
            "value": 22.17,
            "range": "± 1.745",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/hash_verifiable",
            "value": 6.0285,
            "range": "± 0.29",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/no_match",
            "value": 14.71,
            "range": "± 1.195",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScore/source_only",
            "value": 16.865,
            "range": "± 1.01",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_10",
            "value": 165.1,
            "range": "± 1.35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_100",
            "value": 1900,
            "range": "± 116.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreBatch/candidates_50",
            "value": 921.6,
            "range": "± 51.6",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreParallel",
            "value": 25.43,
            "range": "± 1.34",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - B/op",
            "value": 32230,
            "range": "± 122",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10 - allocs/op",
            "value": 511,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_10",
            "value": 1549770.5,
            "range": "± 1942.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - B/op",
            "value": 323087.5,
            "range": "± 996",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100 - allocs/op",
            "value": 5102,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_100",
            "value": 15507475.5,
            "range": "± 93372",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - B/op",
            "value": 1615358,
            "range": "± 8083",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500 - allocs/op",
            "value": 25507,
            "range": "± 2.5",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkScoreResults/subs_500",
            "value": 77503977.5,
            "range": "± 189716.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - B/op",
            "value": 3136,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1 - allocs/op",
            "value": 13,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=1",
            "value": 2398,
            "range": "± 72",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - B/op",
            "value": 26616,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10 - allocs/op",
            "value": 58,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=10",
            "value": 17948.5,
            "range": "± 346.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - B/op",
            "value": 13440,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5 - allocs/op",
            "value": 33,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSearchProviders/providers=5",
            "value": 10463.5,
            "range": "± 611",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - B/op",
            "value": 654258,
            "range": "± 5.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues - allocs/op",
            "value": 10,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/100_cues",
            "value": 1851944.5,
            "range": "± 43567",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - B/op",
            "value": 96116816,
            "range": "± 4",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/2000_cues",
            "value": 88016457,
            "range": "± 1412015",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - B/op",
            "value": 24019025,
            "range": "± 1.5",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues - allocs/op",
            "value": 14,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncCues/500_cues",
            "value": 10167654,
            "range": "± 1689667",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - B/op",
            "value": 307335585,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate - allocs/op",
            "value": 716,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/200_cues_framerate",
            "value": 81292256,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - B/op",
            "value": 799917717,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits - allocs/op",
            "value": 1470,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/500_cues_splits",
            "value": 373382855,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - B/op",
            "value": 192017,
            "unit": "B/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset - allocs/op",
            "value": 141,
            "unit": "allocs/op"
          },
          {
            "name": "BenchmarkSyncWithOptions/50_cues_offset",
            "value": 529449,
            "unit": "ns/op"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_0",
            "value": 749.95,
            "range": "± 38.25",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_1",
            "value": 737,
            "range": "± 47.2",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_2",
            "value": 729.75,
            "range": "± 26.25",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3 - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVADProcessFrame/mode_3",
            "value": 729.35,
            "range": "± 21.35",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/4096B",
            "value": 311.9,
            "range": "± 14",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/512B",
            "value": 252.4,
            "range": "± 12.4",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - B/op",
            "value": 0,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B - allocs/op",
            "value": 0,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkValidate/64B",
            "value": 83.82,
            "range": "± 2.975",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - B/op",
            "value": 41064,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates - allocs/op",
            "value": 27,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/10_candidates",
            "value": 25178,
            "range": "± 552",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - B/op",
            "value": 7800,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates - allocs/op",
            "value": 8,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/2_candidates",
            "value": 3076,
            "range": "± 63.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - B/op",
            "value": 20456,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates - allocs/op",
            "value": 16,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkVoteOnCandidates/5_candidates",
            "value": 9896,
            "range": "± 311.5",
            "unit": "ns/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - B/op",
            "value": 6528,
            "range": "± 0",
            "unit": "B/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset - allocs/op",
            "value": 1,
            "range": "± 0",
            "unit": "allocs/op",
            "extra": "10 samples, median"
          },
          {
            "name": "BenchmarkWeightedMedianOffset",
            "value": 5699,
            "range": "± 426",
            "unit": "ns/op",
            "extra": "10 samples, median"
          }
        ]
      }
    ]
  }
}