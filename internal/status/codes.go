package status

import "slices"

// StartCodes pairs each sentinel Keyed and Begin return with the code api
// answers for it, so a job-start owner lists exactly what it can answer.
var StartCodes = []struct {
	Err  error
	Code string
}{
	{ErrKeyReused, "idempotency_key_reused"},
	{ErrJobCapacity, "job_capacity"},
	{ErrJobUnrecorded, "job_unrecorded"},
}

// Codes is every sentinel the job calls api makes (Keyed, Begin, Get,
// Cancel) return, with the code api answers for it, StartCodes first. The
// element type is unnamed, so status never imports api's wirespec.
var Codes = append(slices.Clip(StartCodes), []struct {
	Err  error
	Code string
}{
	{ErrUnknownJob, "job_unknown"},
	{ErrNotCancellable, "job_not_cancellable"},
}...)
