package cellsvc

import (
	"math"

	gamev1 "mmo/gen/gamev1"
	"mmo/internal/ecs"
)

// Биты input_mask (демо WASD): 1=вперёд +Z, 2=назад −Z, 4=влево −X, 8=вправо +X.
const (
	InputForward = 1 << 0
	InputBack    = 1 << 1
	InputLeft    = 1 << 2
	InputRight   = 1 << 3
)

// velocityFromClientInput строит скорость в XZ. aim_yaw_deg (градусы) поворачивает
// итоговый вектор движения вокруг оси Y против часовой (вид сверху).
func velocityFromClientInput(in *gamev1.ClientInput) ecs.Velocity {
	if in == nil {
		return ecs.Velocity{}
	}
	const speed = 6.0
	m := in.GetInputMask()
	var vx, vz float64
	if m&InputForward != 0 {
		vz += speed
	}
	if m&InputBack != 0 {
		vz -= speed
	}
	if m&InputLeft != 0 {
		vx -= speed
	}
	if m&InputRight != 0 {
		vx += speed
	}
	yaw := float64(in.GetAimYawDeg()) * math.Pi / 180
	if vx != 0 || vz != 0 {
		c, s := math.Cos(yaw), math.Sin(yaw)
		vx, vz = vx*c-vz*s, vx*s+vz*c
	}
	return ecs.Velocity{VX: vx, VY: 0, VZ: vz}
}
