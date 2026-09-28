//go:build !unix

package pipeline

import "math"

func freeSpace(string) (uint64, error) { return math.MaxUint64, nil }

func isUnsupported(error) bool { return true }

func isCrossDevice(error) bool { return false }

func isBusy(error) bool { return false }
