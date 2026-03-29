package natsbus

// Subject из чеклиста Phase 0.1 (базовые топики шины).
const (
	SubjectCellEvents    = "cell.events"
	SubjectCellMigration = "cell.migration"
	// SubjectCellControl — управляющие команды lifecycle сот (controller plane).
	SubjectCellControl = "cell.control"
	SubjectGridCommands  = "grid.commands"
	// SubjectGridSplitWorkflow — события оркестрации split из grid-manager.
	SubjectGridSplitWorkflow = "grid.split.workflow"
)

// CellMigrationSubject формирует subject для дочерней соты: cell.migration.{cellID}.
func CellMigrationSubject(cellID string) string {
	return SubjectCellMigration + "." + cellID
}
