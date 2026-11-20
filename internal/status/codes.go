package status

import "github.com/cplieger/go/httpwire"

// Owner maps every sentinel the job calls the API makes (Keyed, Begin, Get,
// Cancel) return to the code the API answers for it.
var Owner = &httpwire.Owner{
	Name: "status",
	Sentinels: map[string]error{
		"ErrJobUnrecorded":  ErrJobUnrecorded,
		"ErrKeyReused":      ErrKeyReused,
		"ErrJobCapacity":    ErrJobCapacity,
		"ErrNotCancellable": ErrNotCancellable,
		"ErrUnknownJob":     ErrUnknownJob,
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrKeyReused, Code: "idempotency_key_reused"},
		{Err: ErrJobCapacity, Code: "job_capacity"},
		{Err: ErrJobUnrecorded, Code: "job_unrecorded"},
		{Err: ErrUnknownJob, Code: "job_unknown"},
		{Err: ErrNotCancellable, Code: "job_not_cancellable"},
	},
}
