package natsbus

import "testing"

func TestCellMigrationSubject(t *testing.T) {
	if got := CellMigrationSubject("c1"); got != "cell.migration.c1" {
		t.Fatal(got)
	}
}
