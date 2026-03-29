package splitcontrol

const (
	// OpDeleteRuntimeChild — удалить auto-created Deployment/Service для cell_id (cell-controller).
	OpDeleteRuntimeChild = "delete_runtime_child"
)

// Фазы JSON в Redis-ключе mmo:grid:split:retire_state:<parent_cell_id> (grid-manager).
const (
	RetireStatePhaseRetireReady        = "retire_ready"
	RetireStatePhasePreflightBlocked   = "preflight_blocked"
	RetireStatePhaseAutomationComplete = "automation_complete"
)

// События grid.split.workflow (.stage) после успешных handoff.
const (
	// StageRetireReady — parent готов к операторскому §5 / post-handoff.
	StageRetireReady = "retire_ready"
	// StageAutomationComplete — preflight прошёл, автомат control-plane зафиксирован в Redis.
	StageAutomationComplete = "automation_complete"
	// StagePostHandoffPreflightFailed — гейты не прошли; см. retire_state.preflight_blocked_reasons.
	StagePostHandoffPreflightFailed = "post_handoff_preflight_failed"
	// StageTopologySwitched — для merge path: child удалены из каталога, parent winner.
	StageTopologySwitched = "topology_switched"
)

// NextActionOperatorFinalRetire — следующий шаг только оператору: §5 runbook / Terraform baseline parent.
const NextActionOperatorFinalRetire = "operator_final_retire"

// ChildCellCreateRequest команда контроллеру на materialize child-cell.
// Сообщения без поля op трактуются как create (обратная совместимость).
type ChildCellCreateRequest struct {
	Op           string        `json:"op,omitempty"`
	ParentCellID string        `json:"parent_cell_id"`
	RequestID    string        `json:"request_id"`
	Child        ChildCellSpec `json:"child"`
}

// RuntimeCellDeleteRequest удаление runtime child workloads в Kubernetes.
type RuntimeCellDeleteRequest struct {
	Op     string `json:"op"` // delete_runtime_child
	CellID string `json:"cell_id"`
	Reason string `json:"reason"`
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
