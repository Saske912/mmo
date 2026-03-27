package partition

import (
	"fmt"

	cellv1 "mmo/gen/cellv1"
)

// Mid возвращает середину границ (точка деления на 4 квадранта, как в doc.md).
func Mid(b *cellv1.Bounds) (mx, mz float64) {
	if b == nil {
		return 0, 0
	}
	return (b.XMin + b.XMax) / 2, (b.ZMin + b.ZMax) / 2
}

// SplitFour делит родительскую соту на четыре дочерние с границами из документации.
// Порядок: (-1,-1), (1,-1), (-1,1), (1,1) в координатах квадранта (qx, qz).
func SplitFour(parent *cellv1.Bounds) [4]*cellv1.Bounds {
	mx, mz := Mid(parent)
	return [4]*cellv1.Bounds{
		{XMin: parent.XMin, XMax: mx, ZMin: parent.ZMin, ZMax: mz},
		{XMin: mx, XMax: parent.XMax, ZMin: parent.ZMin, ZMax: mz},
		{XMin: parent.XMin, XMax: mx, ZMin: mz, ZMax: parent.ZMax},
		{XMin: mx, XMax: parent.XMax, ZMin: mz, ZMax: parent.ZMax},
	}
}

// Quadrant возвращает индекс дочерней соты 0..3 по правилу границ из doc.md:
// при x == mid или z == mid относим к «положительному» квадранту (>= mid).
func Quadrant(x, z float64, b *cellv1.Bounds) int {
	mx, mz := Mid(b)
	qx := 0
	if x >= mx {
		qx = 1
	}
	qz := 0
	if z >= mz {
		qz = 1
	}
	return qz*2 + qx // 0=(-1,-1), 1=(1,-1), 2=(-1,1), 3=(1,1) — см. порядок SplitFour
}

// ChildID строит идентификатор в стиле cell_-1_-1_1.
func ChildID(qx, qz, level int) string {
	ix := -1
	if qx == 1 {
		ix = 1
	}
	iz := -1
	if qz == 1 {
		iz = 1
	}
	return fmt.Sprintf("cell_%d_%d_%d", ix, iz, level)
}

// Contains проверяет вхождение точки в закрытый AABB (включая границы).
func Contains(b *cellv1.Bounds, x, z float64) bool {
	if b == nil {
		return false
	}
	return x >= b.XMin && x <= b.XMax && z >= b.ZMin && z <= b.ZMax
}

// ChildSpecsForSplit строит четыре дочерние спецификации так же, как gRPC PlanSplit на cell-node
// (порядок совпадает с SplitFour и индексацией qx,qz в ChildID).
func ChildSpecsForSplit(parent *cellv1.Bounds, parentLevel int32) []*cellv1.PlanSplitResponseChild {
	if parent == nil {
		return nil
	}
	childBounds := SplitFour(parent)
	nextLevel := int(parentLevel) + 1
	out := make([]*cellv1.PlanSplitResponseChild, 0, 4)
	for i := range 4 {
		qx, qz := i%2, i/2
		b := childBounds[i]
		out = append(out, &cellv1.PlanSplitResponseChild{
			Id:     ChildID(qx, qz, nextLevel),
			Bounds: b,
			Level:  int32(nextLevel),
		})
	}
	return out
}
