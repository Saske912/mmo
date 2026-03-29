package partition

import (
	"testing"

	cellv1 "mmo/gen/cellv1"
)

func TestSplitFour_DocExample(t *testing.T) {
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	children := SplitFour(parent)
	want := [4]struct{ xmin, xmax, zmin, zmax float64 }{
		{-1000, 0, -1000, 0},
		{0, 1000, -1000, 0},
		{-1000, 0, 0, 1000},
		{0, 1000, 0, 1000},
	}
	for i, w := range want {
		c := children[i]
		if c.XMin != w.xmin || c.XMax != w.xmax || c.ZMin != w.zmin || c.ZMax != w.zmax {
			t.Fatalf("child %d: got %+v want bounds [%v,%v]x[%v,%v]", i, c, w.xmin, w.xmax, w.zmin, w.zmax)
		}
	}
}

func TestQuadrant_OnBoundaryGoesPositive(t *testing.T) {
	b := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	if Quadrant(0, 0, b) != 3 { // (1,1) — правый верхний в индексации SplitFour[3]
		t.Fatalf("origin should map to quadrant index 3, got %d", Quadrant(0, 0, b))
	}
	if Quadrant(0, -500, b) != 1 {
		t.Fatalf("expected right-lower")
	}
}

// Паритет с gRPC PlanSplit на cell-node (см. internal/grpc/cellsvc/server_test.go).
func TestChildSpecsForSplit_matchesPlanSplitContract(t *testing.T) {
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	ch := ChildSpecsForSplit(parent, 0)
	if len(ch) != 4 {
		t.Fatalf("len=%d", len(ch))
	}
	mx, mz := Mid(parent)
	wantIDs := []string{
		ChildID(0, 0, 1),
		ChildID(1, 0, 1),
		ChildID(0, 1, 1),
		ChildID(1, 1, 1),
	}
	for i, w := range wantIDs {
		if ch[i].Id != w {
			t.Errorf("child[%d] id: got %q want %q", i, ch[i].Id, w)
		}
		if ch[i].Level != 1 {
			t.Errorf("child[%d] level=%d", i, ch[i].Level)
		}
		b := ch[i].Bounds
		if b == nil {
			t.Fatalf("child[%d] nil bounds", i)
		}
		switch i {
		case 0:
			if b.XMin != parent.XMin || b.XMax != mx || b.ZMin != parent.ZMin || b.ZMax != mz {
				t.Errorf("quad0 bounds %+v", b)
			}
		case 3:
			if b.XMin != mx || b.XMax != parent.XMax || b.ZMin != mz || b.ZMax != parent.ZMax {
				t.Errorf("quad3 bounds %+v", b)
			}
		}
	}
}

func TestChildSpecsForSplit_nilParent(t *testing.T) {
	if ChildSpecsForSplit(nil, 0) != nil {
		t.Fatal("want nil")
	}
}

func TestValidateMergeChildren_OK(t *testing.T) {
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	ch := ChildSpecsForSplit(parent, 0)
	in := make([]*cellv1.CellSpec, 0, len(ch))
	for _, c := range ch {
		in = append(in, &cellv1.CellSpec{Id: c.GetId(), Level: c.GetLevel(), Bounds: c.GetBounds()})
	}
	if err := ValidateMergeChildren(parent, 0, in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMergeChildren_BadShape(t *testing.T) {
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	ch := ChildSpecsForSplit(parent, 0)
	in := make([]*cellv1.CellSpec, 0, len(ch))
	for _, c := range ch[:3] {
		in = append(in, &cellv1.CellSpec{Id: c.GetId(), Level: c.GetLevel(), Bounds: c.GetBounds()})
	}
	if err := ValidateMergeChildren(parent, 0, in); err == nil {
		t.Fatal("expected error for len != 4")
	}
}

func TestValidateMergeChildren_BadLevel(t *testing.T) {
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	ch := ChildSpecsForSplit(parent, 0)
	in := make([]*cellv1.CellSpec, 0, len(ch))
	for i, c := range ch {
		lvl := c.GetLevel()
		if i == 0 {
			lvl = 99
		}
		in = append(in, &cellv1.CellSpec{Id: c.GetId(), Level: lvl, Bounds: c.GetBounds()})
	}
	if err := ValidateMergeChildren(parent, 0, in); err == nil {
		t.Fatal("expected error for level mismatch")
	}
}

func TestValidateMergeChildren_BadBounds(t *testing.T) {
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	ch := ChildSpecsForSplit(parent, 0)
	in := make([]*cellv1.CellSpec, 0, len(ch))
	for i, c := range ch {
		b := c.GetBounds()
		if i == 1 {
			b = &cellv1.Bounds{XMin: b.XMin + 1, XMax: b.XMax, ZMin: b.ZMin, ZMax: b.ZMax}
		}
		in = append(in, &cellv1.CellSpec{Id: c.GetId(), Level: c.GetLevel(), Bounds: b})
	}
	if err := ValidateMergeChildren(parent, 0, in); err == nil {
		t.Fatal("expected error for bounds mismatch")
	}
}
