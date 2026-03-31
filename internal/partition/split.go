package partition

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	cellv1 "mmo/gen/cellv1"
)

const rootCellID = "cell_root"

var pathCellIDRe = regexp.MustCompile(`^cell_(root|q[0-3](?:_q[0-3])*)$`)

// Mid возвращает середину границ (точка деления на 4 квадранта; контекст — docs/archive/stack-design-notes.md).
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

// Quadrant возвращает индекс дочерней соты 0..3 по правилу границ (docs/archive/stack-design-notes.md):
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

func RootCellID() string {
	return rootCellID
}

func ParseCellPathID(id string) ([]int, error) {
	id = strings.TrimSpace(id)
	if !pathCellIDRe.MatchString(id) {
		return nil, fmt.Errorf("invalid cell path id: %q", id)
	}
	body := strings.TrimPrefix(id, "cell_")
	if body == "root" {
		return []int{}, nil
	}
	parts := strings.Split(body, "_")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if len(p) != 2 || p[0] != 'q' {
			return nil, fmt.Errorf("invalid cell path segment: %q", p)
		}
		q, err := strconv.Atoi(p[1:])
		if err != nil || q < 0 || q > 3 {
			return nil, fmt.Errorf("invalid cell path quadrant: %q", p)
		}
		out = append(out, q)
	}
	return out, nil
}

func CellPathLevel(id string) (int32, bool) {
	path, err := ParseCellPathID(id)
	if err != nil {
		return 0, false
	}
	return int32(len(path)), true
}

func ChildIDForQuadrant(parentID string, quadrant int) (string, error) {
	if quadrant < 0 || quadrant > 3 {
		return "", fmt.Errorf("quadrant out of range: %d", quadrant)
	}
	path, err := ParseCellPathID(parentID)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(path)+1)
	for _, q := range path {
		parts = append(parts, fmt.Sprintf("q%d", q))
	}
	parts = append(parts, fmt.Sprintf("q%d", quadrant))
	return "cell_" + strings.Join(parts, "_"), nil
}

func IsDescendantPath(parentID, childID string) bool {
	parentPath, err := ParseCellPathID(parentID)
	if err != nil {
		return false
	}
	childPath, err := ParseCellPathID(childID)
	if err != nil {
		return false
	}
	if len(childPath) <= len(parentPath) {
		return false
	}
	for i := range parentPath {
		if parentPath[i] != childPath[i] {
			return false
		}
	}
	return true
}

// Contains проверяет вхождение точки в закрытый AABB (включая границы).
func Contains(b *cellv1.Bounds, x, z float64) bool {
	if b == nil {
		return false
	}
	return x >= b.XMin && x <= b.XMax && z >= b.ZMin && z <= b.ZMax
}

// ChildSpecsForSplit строит четыре дочерние спецификации так же, как gRPC PlanSplit на cell-node.
// Порядок совпадает с SplitFour: q0, q1, q2, q3.
func ChildSpecsForSplit(parentID string, parent *cellv1.Bounds, parentLevel int32) ([]*cellv1.PlanSplitResponseChild, error) {
	if parent == nil {
		return nil, nil
	}
	childBounds := SplitFour(parent)
	nextLevel := parentLevel + 1
	out := make([]*cellv1.PlanSplitResponseChild, 0, 4)
	for i := range 4 {
		childID, err := ChildIDForQuadrant(parentID, i)
		if err != nil {
			return nil, err
		}
		b := childBounds[i]
		out = append(out, &cellv1.PlanSplitResponseChild{
			Id:     childID,
			Bounds: b,
			Level:  nextLevel,
		})
	}
	return out, nil
}

func boundsEqual(a, b *cellv1.Bounds) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.XMin == b.XMin && a.XMax == b.XMax && a.ZMin == b.ZMin && a.ZMax == b.ZMax
}

// CatalogMergeChildren находит в каталоге ровно четыре дочерние соты уровня parent.Level+1,
// bounds которых совпадают с SplitFour(parent.Bounds). Порядок результата — квадранты 0..3.
func CatalogMergeChildren(parent *cellv1.CellSpec, catalog []*cellv1.CellSpec) ([]*cellv1.CellSpec, error) {
	if parent == nil || parent.GetBounds() == nil {
		return nil, fmt.Errorf("parent nil or has no bounds")
	}
	wantLevel := parent.GetLevel() + 1
	quads := SplitFour(parent.GetBounds())
	out := make([]*cellv1.CellSpec, 4)
	for qi := range 4 {
		var match *cellv1.CellSpec
		for _, c := range catalog {
			if c == nil || c.GetBounds() == nil {
				continue
			}
			if c.GetLevel() != wantLevel {
				continue
			}
			if !boundsEqual(c.GetBounds(), quads[qi]) {
				continue
			}
			if match != nil && strings.TrimSpace(match.GetId()) != strings.TrimSpace(c.GetId()) {
				return nil, fmt.Errorf("ambiguous catalog match for merge quadrant %d", qi)
			}
			match = c
		}
		if match == nil {
			return nil, fmt.Errorf("missing catalog child for merge quadrant %d", qi)
		}
		out[qi] = match
	}
	seen := make(map[string]struct{}, 4)
	for i, c := range out {
		id := strings.TrimSpace(c.GetId())
		if id == "" {
			return nil, fmt.Errorf("empty child id in merge quadrant %d", i)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("duplicate child id %s in merge quadrants", id)
		}
		seen[id] = struct{}{}
	}
	return out, nil
}

// ValidateMergeChildren проверяет, что children образуют ровно «обратный split» для parent:
// - ровно 4 child;
// - каждый child уровня parentLevel+1;
// - множество bounds совпадает с SplitFour(parent) (порядок в children произвольный).
func ValidateMergeChildren(parent *cellv1.Bounds, parentLevel int32, children []*cellv1.CellSpec) error {
	if parent == nil {
		return fmt.Errorf("parent bounds is nil")
	}
	if len(children) != 4 {
		return fmt.Errorf("need 4 children, got %d", len(children))
	}
	quads := SplitFour(parent)
	wantLevel := parentLevel + 1
	used := [4]bool{}
	seen := make(map[string]struct{}, 4)
	for _, c := range children {
		if c == nil {
			return fmt.Errorf("child spec is nil")
		}
		id := strings.TrimSpace(c.GetId())
		if id == "" {
			return fmt.Errorf("child has empty id")
		}
		if c.GetLevel() != wantLevel {
			return fmt.Errorf("child %s level=%d want=%d", id, c.GetLevel(), wantLevel)
		}
		if c.GetBounds() == nil {
			return fmt.Errorf("child %s missing bounds", id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate child id %s", id)
		}
		seen[id] = struct{}{}
		match := -1
		for qi := range 4 {
			if used[qi] {
				continue
			}
			if boundsEqual(c.GetBounds(), quads[qi]) {
				match = qi
				break
			}
		}
		if match < 0 {
			return fmt.Errorf("child %s bounds do not match any merge quadrant", id)
		}
		used[match] = true
	}
	for qi := range 4 {
		if !used[qi] {
			return fmt.Errorf("missing merge quadrant %d", qi)
		}
	}
	return nil
}
