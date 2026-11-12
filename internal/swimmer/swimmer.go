// Package swimmer simulates a swimmer's race: when each 25m wall touch happens.
package swimmer

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// LengthM is the pool length. Touchpads fire at every wall.
const LengthM = 25

// Profile describes how a simulated swimmer races.
type Profile struct {
	// Pace25 is the swimmer's typical time for one 25m length.
	Pace25 time.Duration
	// Variance is the random +/- fraction applied to each length (0.03 = ±3%).
	Variance float64
	// Fade is how much slower each length gets than the one before (0.02 = 2%).
	Fade float64
	// DiveBonus makes the first length faster, since it starts with a dive (0.10 = 10%).
	DiveBonus float64
}

// DefaultProfile returns a realistic profile around the given pace.
func DefaultProfile(pace25 time.Duration) Profile {
	return Profile{Pace25: pace25, Variance: 0.03, Fade: 0.02, DiveBonus: 0.10}
}

// Splits returns the cumulative elapsed time at each wall touch for a race of
// distanceM meters. The last element is the finish time.
func (p Profile) Splits(distanceM int, rng *rand.Rand) ([]time.Duration, error) {
	if distanceM <= 0 || distanceM%LengthM != 0 {
		return nil, fmt.Errorf("swimmer: distance %dm is not a positive multiple of %dm", distanceM, LengthM)
	}
	if p.Pace25 <= 0 {
		return nil, fmt.Errorf("swimmer: pace must be positive")
	}
	n := distanceM / LengthM
	splits := make([]time.Duration, n)
	var elapsed time.Duration
	for i := range n {
		f := 1 + p.Fade*float64(i) + p.Variance*(2*rng.Float64()-1)
		if i == 0 {
			f -= p.DiveBonus
		}
		if f < 0.1 { // keep absurd parameters from producing zero or negative lengths
			f = 0.1
		}
		elapsed += time.Duration(float64(p.Pace25) * f)
		splits[i] = elapsed
	}
	return splits, nil
}

// ParseDistance reads the distance from an event name like "100 free" or
// "200m IM". It returns an error if there is no leading distance or it isn't a
// multiple of the pool length.
func ParseDistance(event string) (int, error) {
	fields := strings.Fields(event)
	if len(fields) == 0 {
		return 0, fmt.Errorf("swimmer: empty event name")
	}
	d, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(fields[0]), "m"))
	if err != nil {
		return 0, fmt.Errorf("swimmer: event %q has no leading distance", event)
	}
	if d <= 0 || d%LengthM != 0 {
		return 0, fmt.Errorf("swimmer: event distance %dm is not a multiple of %dm", d, LengthM)
	}
	return d, nil
}
