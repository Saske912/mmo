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
