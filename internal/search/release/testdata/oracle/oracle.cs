// Oracle runner for the subflux release PCRE layer (spec
// subflux-release-parse-fidelity R2). A .NET 10 file-based app: evaluates
// every source pattern against every corpus name under the real .NET
// backtracking Regex engine and emits the full-fidelity JSON schema —
// ALL matches per (pattern, name) in scan order, per-group participation
// {success, index, length, value}, applied RegexOptions, pinned culture,
// compile errors, and staleness keys (pattern-text SHA-256, corpus
// SHA-256, runner + runtime versions).
//
// Never uses RegexOptions.NonBacktracking (it disallows lookarounds and is
// not the semantics the layer emulates). Match timeout is explicit.
//
// Run via regen.sh (pinned .NET SDK image):
//   dotnet run oracle.cs -- patterns.json corpus.json out.json

#:property PublishAot=false
#:property JsonSerializerIsReflectionEnabledByDefault=true

using System.Globalization;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;

const string RunnerVersion = "1.0.0";

if (args.Length != 3)
{
    Console.Error.WriteLine("usage: dotnet run oracle.cs -- patterns.json corpus.json out.json");
    return 2;
}

CultureInfo.CurrentCulture = CultureInfo.InvariantCulture;

var patternsText = File.ReadAllText(args[0], Encoding.UTF8);
var corpusText = File.ReadAllText(args[1], Encoding.UTF8);

var patternsDoc = JsonSerializer.Deserialize<PatternsFile>(patternsText, JsonOpts.In)!;
var corpusDoc = JsonSerializer.Deserialize<CorpusFile>(corpusText, JsonOpts.In)!;

// Staleness keys: canonical digests over "id\u0000regex\n" lines and
// "name\n" lines (the Go comparison test recomputes both from live data).
string patternsSha = Sha256Hex(string.Join("", patternsDoc.Patterns.Select(p => p.Id + "\u0000" + p.Regex + "\n")));
string corpusSha = Sha256Hex(string.Join("", corpusDoc.Names.Select(n => n + "\n")));

var timeout = TimeSpan.FromSeconds(5);
const RegexOptions AppliedOptions = RegexOptions.IgnoreCase | RegexOptions.CultureInvariant;

var results = new List<PatternResult>();
foreach (var pat in patternsDoc.Patterns)
{
    Regex? re = null;
    string? compileError = null;
    try
    {
        re = new Regex(pat.Regex, AppliedOptions, timeout);
    }
    catch (Exception ex)
    {
        compileError = ex.Message;
    }

    var entry = new PatternResult
    {
        PatternId = pat.Id,
        Options = AppliedOptions.ToString(),
        CompileError = compileError,
        Names = new List<NameResult>(),
    };

    if (re != null)
    {
        foreach (var name in corpusDoc.Names)
        {
            List<MatchResult>? matches = null;
            string? matchError = null;
            try
            {
                foreach (Match m in re.Matches(name))
                {
                    matches ??= new List<MatchResult>();
                    var groups = new List<GroupResult>();
                    for (int gi = 0; gi < m.Groups.Count; gi++)
                    {
                        var g = m.Groups[gi];
                        groups.Add(new GroupResult
                        {
                            Success = g.Success,
                            Index = g.Success ? g.Index : -1,
                            Length = g.Success ? g.Length : -1,
                            Value = g.Success ? g.Value : null,
                        });
                    }
                    matches.Add(new MatchResult
                    {
                        Index = m.Index,
                        Length = m.Length,
                        Value = m.Value,
                        Groups = groups,
                    });
                }
            }
            catch (RegexMatchTimeoutException)
            {
                matchError = "timeout";
            }
            if (matches != null || matchError != null)
            {
                entry.Names.Add(new NameResult { Name = name, Matches = matches, Error = matchError });
            }
        }
    }
    results.Add(entry);
}

var output = new OracleFile
{
    RunnerVersion = RunnerVersion,
    RuntimeVersion = System.Runtime.InteropServices.RuntimeInformation.FrameworkDescription,
    Culture = "CultureInvariant (RegexOptions.CultureInvariant; CurrentCulture pinned to InvariantCulture)",
    Engine = "backtracking (NonBacktracking never used; lookarounds required)",
    TimeoutSeconds = timeout.TotalSeconds,
    PatternsSha256 = patternsSha,
    CorpusSha256 = corpusSha,
    Results = results,
};

File.WriteAllText(args[2], JsonSerializer.Serialize(output, JsonOpts.Out) + "\n", new UTF8Encoding(false));
Console.Error.WriteLine($"oracle: {results.Count} patterns x {corpusDoc.Names.Count} names -> {args[2]}");
return 0;

static string Sha256Hex(string s) =>
    Convert.ToHexStringLower(SHA256.HashData(Encoding.UTF8.GetBytes(s)));

static class JsonOpts
{
    public static readonly JsonSerializerOptions In = new() { PropertyNameCaseInsensitive = true };
    public static readonly JsonSerializerOptions Out = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        DefaultIgnoreCondition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull,
        Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
    };
}

record PatternsFile
{
    public required List<PatternSpec> Patterns { get; init; }
}

record PatternSpec
{
    public required string Id { get; init; }
    public required string Regex { get; init; }
}

record CorpusFile
{
    public required List<string> Names { get; init; }
}

class OracleFile
{
    public required string RunnerVersion { get; init; }
    public required string RuntimeVersion { get; init; }
    public required string Culture { get; init; }
    public required string Engine { get; init; }
    public required double TimeoutSeconds { get; init; }
    public required string PatternsSha256 { get; init; }
    public required string CorpusSha256 { get; init; }
    public required List<PatternResult> Results { get; init; }
}

class PatternResult
{
    public required string PatternId { get; init; }
    public required string Options { get; init; }
    public string? CompileError { get; init; }
    public required List<NameResult> Names { get; init; }
}

class NameResult
{
    public required string Name { get; init; }
    public List<MatchResult>? Matches { get; init; }
    public string? Error { get; init; }
}

class MatchResult
{
    public required int Index { get; init; }
    public required int Length { get; init; }
    public required string Value { get; init; }
    public required List<GroupResult> Groups { get; init; }
}

class GroupResult
{
    public required bool Success { get; init; }
    public required int Index { get; init; }
    public required int Length { get; init; }
    public string? Value { get; init; }
}
