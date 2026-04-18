package untested

// Max returns the larger of a and b.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Double returns n * 2.
func Double(n int) int {
	return n * 2
}

// IsEven returns true if n is divisible by 2.
func IsEven(n int) bool {
	return n%2 == 0
}
