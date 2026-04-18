package simple

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}

// IsPositive returns true if n > 0.
func IsPositive(n int) bool {
	if n > 0 {
		return true
	}
	return false
}

// Abs returns the absolute value of n.
func Abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Clamp restricts v to the range [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	} else {
		if v > hi {
			return hi
		}
	}
	return v
}

// CountPositives counts the positive numbers in a slice.
func CountPositives(nums []int) int {
	count := 0
	for _, n := range nums {
		if n > 0 {
			count++
		}
	}
	return count
}

// Grade returns a letter grade for a score.
func Grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	default:
		return "F"
	}
}

// Both returns true only if a and b are both true.
func Both(a, b bool) bool {
	return a && b
}

// Either returns true if a or b is true.
func Either(a, b bool) bool {
	return a || b
}
