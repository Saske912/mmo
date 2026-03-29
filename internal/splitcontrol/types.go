package splitcontrol

// ChildCellCreateRequest команда контроллеру на materialize child-cell.
type ChildCellCreateRequest struct {
	ParentCellID string        `json:"parent_cell_id"`
	RequestID    string        `json:"request_id"`
	Child        ChildCellSpec `json:"child"`
}

// ChildCellSpec минимальная спецификация child-cell для runtime create.
type ChildCellSpec struct {
	ID    string  `json:"id"`
	Level int32   `json:"level"`
	XMin  float64 `json:"x_min"`
	XMax  float64 `json:"x_max"`
	ZMin  float64 `json:"z_min"`
	ZMax  float64 `json:"z_max"`
}

// CellLifecycleEvent событие контроллера о создании/готовности/ошибке.
type CellLifecycleEvent struct {
	CellID   string `json:"cell_id"`
	Action   string `json:"action"`
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	ParentID string `json:"parent_id,omitempty"`
	AtUnixMs int64  `json:"at_unix_ms"`
}
