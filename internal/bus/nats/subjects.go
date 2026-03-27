package natsbus

// Subject из чеклиста Phase 0.1 (базовые топики шины).
const (
	SubjectCellEvents    = "cell.events"
	SubjectCellMigration = "cell.migration"
	SubjectGridCommands  = "grid.commands"
)

// CellMigrationSubject формирует subject для дочерней соты: cell.migration.{cellID}.
func CellMigrationSubject(cellID string) string {
	return SubjectCellMigration + "." + cellID
}
