package ui

// groupSizes returns the per-dialog variable counts for n variables: ceil(n/5)
// balanced groups, each ≤5, filled at size ceil(n/groups). n<=0 → nil.
// e.g. 6→[3,3], 13→[5,5,3], 12→[4,4,4].
func groupSizes(n int) []int {
	if n <= 0 {
		return nil
	}
	groups := (n + 4) / 5             // ceil(n/5)
	size := (n + groups - 1) / groups // ceil(n/groups)
	var sizes []int
	for n > 0 {
		s := size
		if s > n {
			s = n
		}
		sizes = append(sizes, s)
		n -= s
	}
	return sizes
}
