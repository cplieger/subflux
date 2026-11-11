// Package search is subflux's search engine: it plans each run of C4's work
// queue, asks the enabled providers through the call funnel, checks and
// ranks their candidates, fetches the best through timing-sync's aligner and
// delivers one subtitle per wanted (item, target, variant). Its settings are
// the search section here and the providers section, provider.ParseConfigs;
// both parsers run every check a user's values can fail, so New, after a
// clean parse, fails only on a registry fault.
package search
