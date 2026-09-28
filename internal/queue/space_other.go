//go:build !unix

package queue

// deviceOf treats every folder as one filesystem where devices are not
// reported.
func deviceOf(string) (uint64, error) { return 0, nil }
