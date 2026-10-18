package status

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
