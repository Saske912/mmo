#!/usr/bin/env bash
# Генерация C# для Unity из тех же .proto, что и Go (Google.Protobuf в Unity/Assets/Plugins/GoogleProtobuf).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PROTO_ROOT="$ROOT/backend/proto"
OUT="$ROOT/Unity/Assets/MMO/Generated"
mkdir -p "$OUT"
need() { command -v "$1" >/dev/null 2>&1 || { echo "нужен $1 (apt install protobuf-compiler)" >&2; exit 1; }; }
need protoc
protoc -I "$PROTO_ROOT" --csharp_out="$OUT" \
  "$PROTO_ROOT/game/v1/replication.proto" \
  "$PROTO_ROOT/cell/v1/cell.proto"
echo "OK: $OUT (Replication.cs, Cell.cs)"
