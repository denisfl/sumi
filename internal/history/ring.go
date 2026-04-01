// internal/history/ring.go
package history

// Ring is a fixed-size circular buffer of float64 values.
// Push overwrites the oldest sample when full.
type Ring struct {
	data []float64
	head int
	size int
}

// NewRing allocates a Ring with capacity n.
func NewRing(n int) *Ring {
	if n < 1 {
		n = 1
	}
	return &Ring{data: make([]float64, n)}
}

// Push adds v to the ring, overwriting the oldest value when full.
func (r *Ring) Push(v float64) {
	r.data[r.head%len(r.data)] = v
	r.head++
	if r.size < len(r.data) {
		r.size++
	}
}

// Sparkline returns a string of Unicode block characters representing the
// last min(width, r.size) samples on a 0–100 scale.
// The oldest sample is on the left.
func (r *Ring) Sparkline(width int) string {
	if r.size == 0 || width <= 0 {
		return ""
	}
	n := len(r.data)
	// oldest sample index
	start := r.head % n
	if r.size < n {
		start = 0
	}

	// Collect samples in chronological order.
	samples := make([]float64, r.size)
	for i := 0; i < r.size; i++ {
		samples[i] = r.data[(start+i)%n]
	}

	// Use the last `width` samples.
	if len(samples) > width {
		samples = samples[len(samples)-width:]
	}

	blocks := []rune{' ', '\u2581', '\u2582', '\u2583', '\u2584', '\u2585', '\u2586', '\u2587', '\u2588'}
	out := make([]rune, len(samples))
	for i, v := range samples {
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		idx := int(v / 100.0 * float64(len(blocks)-1))
		out[i] = blocks[idx]
	}
	return string(out)
}
