package statedb

import "github.com/cplieger/bolttable"

// deleteInstance is the one way an instance is deleted: it first touches the
// written subject of each of its items' roots, so the file records its
// cascade removes reach Written, then deletes it with its cascade. The
// roots come from the stored row, so a caller that emptied the row first
// touches the roots of the keys it removed. The caller has checked the
// instance is not pinned. It returns the deleted row, and false when none
// was stored.
func (s *Store) deleteInstance(tx *bolttable.Tx, id InstanceID) (instanceRow, bool, error) {
	row, found, err := s.t.instances.Get(tx.Read(), id)
	if err != nil || !found {
		return instanceRow{}, false, err
	}
	for _, k := range row.Items {
		tx.Touch(rootSubject(subjectWritten, k.k.Root()))
	}
	if _, err := s.t.instances.Delete(tx, id); err != nil {
		return instanceRow{}, false, err
	}
	return row, true, nil
}
