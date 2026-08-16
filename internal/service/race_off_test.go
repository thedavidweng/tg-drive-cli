//go:build !race

package service

// raceEnabled marks race-detector builds; scale bounds relax accordingly.
const raceEnabled = false
