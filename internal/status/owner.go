package status

// The owners Job.Owner holds besides a user's ID.
const (
	// SystemOwner owns scheduled work; it is never a principal's owner.
	SystemOwner int64 = 0
	// BypassOwner stands for the bypass principal, whose user ID is 0 and so
	// would read as SystemOwner.
	BypassOwner int64 = -1
)

// OwnerOf is the one mapping from a principal to Job.Owner: BypassOwner for
// the bypass principal, else its user ID.
func OwnerOf(userID int64, bypass bool) int64 {
	if bypass {
		return BypassOwner
	}
	return userID
}
