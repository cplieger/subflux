window.BENCHMARK_DATA = {
  "lastUpdate": 1788310344722,
  "repoUrl": "https://github.com/cplieger/subflux",
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
      }
    ]
  }
}