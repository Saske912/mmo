package registrysvc

import (
	"log/slog"

	cellv1 "mmo/gen/cellv1"
)

func logOpStart(method string, attrs ...any) {
	base := []any{"method", method}
	slog.Info("registry_op_start", append(base, attrs...)...)
}

func logOpDone(method string, attrs ...any) {
	base := []any{"method", method}
	slog.Info("registry_op_done", append(base, attrs...)...)
}

func logOpError(method string, err error, attrs ...any) {
	base := []any{"method", method, "error", err}
	slog.Error("registry_op_error", append(base, attrs...)...)
}

func updateKind(upd *cellv1.UpdateRequest) string {
	if upd == nil || upd.Payload == nil {
		return "nil"
	}
	switch upd.Payload.(type) {
	case *cellv1.UpdateRequest_Noop:
		return "noop"
	case *cellv1.UpdateRequest_SetTargetTps:
		return "set_target_tps"
	case *cellv1.UpdateRequest_SplitPrepare:
		return "split_prepare"
	case *cellv1.UpdateRequest_SetSplitDrain:
		return "set_split_drain"
	case *cellv1.UpdateRequest_ExportNpcPersist:
		return "export_npc_persist"
	case *cellv1.UpdateRequest_ImportNpcPersist:
		return "import_npc_persist"
	case *cellv1.UpdateRequest_MergePrepare:
		return "merge_prepare"
	default:
		return "unknown"
	}
}
