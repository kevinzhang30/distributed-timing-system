package swimmer

import (
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

func TestSplitsShape(t *testing.T) {
	p := DefaultProfile(14 * time.Second)
	for _, dist := range []int{25, 50, 100, 200, 1500} {
		s, err := p.Splits(dist, rand.New(rand.NewPCG(1, 2)))
		if err != nil {
			t.Fatalf("%dm: %v", dist, err)
		}
		if len(s) != dist/LengthM {
			t.Fatalf("%dm: got %d splits, want %d", dist, len(s), dist/LengthM)
		}
		for i := 1; i < len(s); i++ {
			if s[i] <= s[i-1] {
				t.Fatalf("%dm: splits not increasing at %d: %v", dist, i, s)
			}
		}
	}
}

func TestSplitsRealistic(t *testing.T) {
	// A 14s/25m swimmer should go roughly 55-60s for a 100.
	s, _ := DefaultProfile(14*time.Second).Splits(100, rand.New(rand.NewPCG(7, 7)))
	final := s[len(s)-1]
	if final < 52*time.Second || final > 62*time.Second {
		t.Fatalf("100m final %v outside realistic range", final)
	}
	// Dive start: first length is the fastest.
	first := s[0]
	for i := 1; i < len(s); i++ {
		if length := s[i] - s[i-1]; length < first {
			t.Fatalf("length %d (%v) faster than the dive length (%v)", i+1, length, first)
		}
	}
}

func TestSplitsDeterministic(t *testing.T) {
	p := DefaultProfile(14 * time.Second)
	a, _ := p.Splits(200, rand.New(rand.NewPCG(42, 0)))
	b, _ := p.Splits(200, rand.New(rand.NewPCG(42, 0)))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different splits")
	}
}

func TestSplitsRejects(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	for _, d := range []int{0, -25, 30} {
		if _, err := DefaultProfile(time.Second).Splits(d, rng); err == nil {
			t.Errorf("distance %d: expected error", d)
		}
	}
	if _, err := DefaultProfile(0).Splits(100, rng); err == nil {
		t.Error("zero pace: expected error")
	}
}

func TestParseDistance(t *testing.T) {
	good := map[string]int{"100 free": 100, "50 fly": 50, "200m IM": 200, "1500M free": 1500, "25": 25}
	for in, want := range good {
		if got, err := ParseDistance(in); err != nil || got != want {
			t.Errorf("ParseDistance(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "free relay", "30 free", "-50 back", "0 free"} {
		if _, err := ParseDistance(in); err == nil {
			t.Errorf("ParseDistance(%q): expected error", in)
		}
	}
}
