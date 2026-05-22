package authdb

import "database/sql"

// queryList executes a query and scans all rows using the provided scanInto
// function. Used by list methods to avoid duplicating the rows-iteration loop.
func queryList[T any](rows *sql.Rows, scanInto func(interface{ Scan(...any) error }, *T) error) ([]T, error) {
	defer rows.Close()
	var items []T
	for rows.Next() {
		var item T
		if err := scanInto(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
